package processor

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"whisper-proxy/internal/api"
	"whisper-proxy/internal/audio"
)

const (
	SampleRate = 16000
	BytesPerSec = 32000 // 16000 * 1 channel * 2 bytes (16-bit)
)

type StreamProcessor struct {
	conn       net.Conn
	apiClient  *api.Client
	minChunk   float64
	trimSec    float64

	mu           sync.Mutex
	audioBuf     []byte
	bufferOffset float64 // in seconds
	hypothesis   *HypothesisBuffer

	lastEndMs float64
}

func NewStreamProcessor(conn net.Conn, apiClient *api.Client, minChunk, trimSec float64) *StreamProcessor {
	return &StreamProcessor{
		conn:       conn,
		apiClient:  apiClient,
		minChunk:   minChunk,
		trimSec:    trimSec,
		hypothesis: NewHypothesisBuffer(),
		audioBuf:   make([]byte, 0),
	}
}

func (s *StreamProcessor) Run() {
	stopChan := make(chan struct{})

	// Start reading audio from socket
	go s.readLoop(stopChan)

	// Process loop
	ticker := time.NewTicker(time.Duration(s.minChunk*1000) * time.Millisecond)
	defer ticker.Stop()

	var lastProcessedLen int

	for {
		select {
		case <-stopChan:
			s.flushRemaining()
			return
		case <-ticker.C:
			s.mu.Lock()
			currentLen := len(s.audioBuf)
			
			// Only process if we have a minimum chunk size of *new* data
			if currentLen-lastProcessedLen >= int(s.minChunk*BytesPerSec) {
				snapshot := make([]byte, currentLen)
				copy(snapshot, s.audioBuf)
				offset := s.bufferOffset
				s.mu.Unlock()

				s.processChunk(snapshot, offset)
				
				s.mu.Lock()
				lastProcessedLen = len(s.audioBuf)
				s.trimBuffer()
				s.mu.Unlock()
			} else {
				s.mu.Unlock()
			}
		}
	}
}

func (s *StreamProcessor) readLoop(stopChan chan struct{}) {
	buf := make([]byte, 4096)
	for {
		n, err := s.conn.Read(buf)
		if n > 0 {
			s.mu.Lock()
			s.audioBuf = append(s.audioBuf, buf[:n]...)
			s.mu.Unlock()
		}
		if err != nil {
			if err != io.EOF {
				slog.Error("Socket read error", "error", err)
			}
			close(stopChan)
			return
		}
	}
}

func (s *StreamProcessor) processChunk(pcm []byte, offset float64) {
	wavData := audio.ToWAV(pcm)
	
	resp, err := s.apiClient.Transcribe(wavData)
	if err != nil {
		slog.Error("Transcription failed", "error", err)
		return
	}

	committed := s.hypothesis.Process(resp.Words, offset)
	s.sendToClient(committed)
}

func (s *StreamProcessor) trimBuffer() {
	audioSec := float64(len(s.audioBuf)) / BytesPerSec
	
	if audioSec > s.trimSec {
		// Trim up to the last committed time
		cutSeconds := s.hypothesis.LastCommittedTime() - s.bufferOffset
		if cutSeconds > 0 {
			cutBytes := int(cutSeconds * BytesPerSec)
			cutBytes -= cutBytes % 2 // Align to 16-bit boundaries

			if cutBytes > 0 && cutBytes < len(s.audioBuf) {
				slog.Debug("Trimming buffer", "cutSeconds", cutSeconds, "cutBytes", cutBytes)
				s.audioBuf = s.audioBuf[cutBytes:]
				s.bufferOffset += float64(cutBytes) / BytesPerSec
			}
		}
	}
}

func (s *StreamProcessor) flushRemaining() {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	uncommitted := s.hypothesis.Flush()
	s.sendToClient(uncommitted)
}

func (s *StreamProcessor) sendToClient(words []api.Word) {
	for _, w := range words {
		begMs := w.Start * 1000.0
		endMs := w.End * 1000.0

		// Prevent overlapping timestamps (ELITR protocol requirement)
		if s.lastEndMs > 0 && begMs < s.lastEndMs {
			begMs = s.lastEndMs
		}
		s.lastEndMs = endMs

		msg := fmt.Sprintf("%.0f %.0f %s\n", begMs, endMs, strings.TrimSpace(w.Word))
		s.conn.Write([]byte(msg))
	}
}
