package processor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"whisper-proxy/internal/api"
	"whisper-proxy/internal/audio"
	"whisper-proxy/internal/vad"
)

const (
	SampleRate  = 16000
	BytesPerSec = 32000 // 16kHz * mono * 16-bit
)

type StreamConfig struct {
	MinChunkSec      float64
	TrimSec          float64
	MaxClipLengthSec float64
	ClipOverlapSec   float64
	PromptChars      int
	VADDetector      vad.Detector
}

type StreamProcessor struct {
	conn       net.Conn
	connID     string
	apiClient  *api.Client
	cfg        StreamConfig
	cancelConn context.CancelFunc

	mu                         sync.Mutex
	audioBuf                   []byte
	bufferOffsetSec            float64
	lastProcessedBufferLenByte int
	lastSendWindowEndSec       float64
	forceProcess               bool

	hypothesis *HypothesisBuffer
	lastEndMs  float64
	reqSeq     int64

	// Consecutive identical response tracking for silence suppression.
	prevResponseSig        string
	identicalResponseCount int

	audioSignal chan struct{}
	readDone    chan struct{}
	readErr     error
	closeOnce   sync.Once
}

type requestSnapshot struct {
	seq              int64
	pcm              []byte
	prompt           string
	sendWindowOffset float64
	sendDurationSec  float64
	retainedDuration float64
	retainedLenBytes int
	capApplied       bool
}

func NewStreamProcessor(
	conn net.Conn,
	connID string,
	apiClient *api.Client,
	cfg StreamConfig,
	cancelConn context.CancelFunc,
) *StreamProcessor {
	if cfg.MinChunkSec <= 0 {
		cfg.MinChunkSec = 1.0
	}
	if cfg.TrimSec <= 0 {
		cfg.TrimSec = 15.0
	}
	if cfg.PromptChars <= 0 {
		cfg.PromptChars = 200
	}

	return &StreamProcessor{
		conn:        conn,
		connID:      connID,
		apiClient:   apiClient,
		cfg:         cfg,
		cancelConn:  cancelConn,
		hypothesis:  NewHypothesisBuffer(),
		audioBuf:    make([]byte, 0, BytesPerSec*4),
		audioSignal: make(chan struct{}, 1),
		readDone:    make(chan struct{}),
	}
}

func (s *StreamProcessor) Run(ctx context.Context) {
	go s.readLoop(ctx)

	staleDur := s.staleFlushInterval()
	staleTimer := time.NewTimer(staleDur)
	defer staleTimer.Stop()

	for {
		req, ok := s.nextRequestSnapshot(false)
		if ok {
			s.executeRequest(ctx, req)
			resetTimer(staleTimer, staleDur)
			continue
		}

		select {
		case <-ctx.Done():
			s.flushRemaining()
			return
		case <-s.readDone:
			s.drainAndFlush()
			return
		case <-s.audioSignal:
			resetTimer(staleTimer, staleDur)
			continue
		case <-staleTimer.C:
			s.commitStale(ctx)
			staleTimer.Reset(staleDur)
		}
	}
}

func (s *StreamProcessor) readLoop(ctx context.Context) {
	buf := make([]byte, 4096)
	for {
		n, err := s.conn.Read(buf)
		if n > 0 {
			var events []vad.Event
			s.mu.Lock()
			s.audioBuf = append(s.audioBuf, buf[:n]...)
			if s.cfg.VADDetector != nil {
				events = s.cfg.VADDetector.Process(buf[:n])
				for _, ev := range events {
					if ev.Type == vad.EventSpeechEnd {
						s.forceProcess = true
						break
					}
				}
			}
			s.mu.Unlock()
			s.signalAudio()
		}
		if err != nil {
			isEOF := errors.Is(err, io.EOF)
			if !isEOF {
				slog.Error("socket read error", "conn_id", s.connID, "error", err)
			}
			s.closeOnce.Do(func() {
				s.readErr = err
				close(s.readDone)
			})
			// Only cancel on non-EOF errors. On EOF the main loop's
			// drainAndFlush will make final upstream requests to commit
			// remaining words before the socket is torn down.
			if !isEOF {
				s.cancelConn()
			}
			return
		}

		select {
		case <-ctx.Done():
			s.cancelConn()
			return
		default:
		}
	}
}

func (s *StreamProcessor) signalAudio() {
	select {
	case s.audioSignal <- struct{}{}:
	default:
	}
}

func (s *StreamProcessor) nextRequestSnapshot(force bool) (requestSnapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	retainedLen := len(s.audioBuf)
	if retainedLen == 0 {
		return requestSnapshot{}, false
	}

	newBytes := retainedLen - s.lastProcessedBufferLenByte
	minBytes := int(s.cfg.MinChunkSec * BytesPerSec)
	// When the upstream returns identical results repeatedly (silence/static),
	// require significantly more new audio before making another request.
	if !force && s.identicalResponseCount >= 2 {
		minBytes *= 5
	}
	if !force && !s.forceProcess && newBytes < minBytes {
		return requestSnapshot{}, false
	}
	if !force && s.forceProcess && newBytes <= 0 {
		return requestSnapshot{}, false
	}

	retainedDurationSec := float64(retainedLen) / BytesPerSec
	sendStartSec := s.computeSendWindowOffsetSecLocked(retainedDurationSec)
	startByte := int((sendStartSec - s.bufferOffsetSec) * BytesPerSec)
	if startByte < 0 {
		startByte = 0
	}
	startByte -= startByte % 2
	if startByte > retainedLen {
		startByte = retainedLen
	}

	sendPCM := make([]byte, retainedLen-startByte)
	copy(sendPCM, s.audioBuf[startByte:])

	sendDurationSec := float64(len(sendPCM)) / BytesPerSec
	s.reqSeq++
	s.forceProcess = false

	return requestSnapshot{
		seq:              s.reqSeq,
		pcm:              sendPCM,
		prompt:           s.hypothesis.PromptOutsideBuffer(s.bufferOffsetSec, s.cfg.PromptChars),
		sendWindowOffset: sendStartSec,
		sendDurationSec:  sendDurationSec,
		retainedDuration: retainedDurationSec,
		retainedLenBytes: retainedLen,
		capApplied:       startByte > 0,
	}, true
}

func (s *StreamProcessor) computeSendWindowOffsetSecLocked(retainedDurationSec float64) float64 {
	bufferStart := s.bufferOffsetSec
	bufferEnd := s.bufferOffsetSec + retainedDurationSec

	if s.cfg.MaxClipLengthSec <= 0 || retainedDurationSec <= s.cfg.MaxClipLengthSec {
		return bufferStart
	}

	start := bufferEnd - s.cfg.MaxClipLengthSec
	if s.lastSendWindowEndSec > 0 && s.cfg.ClipOverlapSec > 0 {
		overlapStart := s.lastSendWindowEndSec - s.cfg.ClipOverlapSec
		if overlapStart > start {
			start = overlapStart
		}
	}
	if start < bufferStart {
		start = bufferStart
	}

	if bufferEnd-start > s.cfg.MaxClipLengthSec {
		start = bufferEnd - s.cfg.MaxClipLengthSec
	}
	if start < bufferStart {
		start = bufferStart
	}

	return start
}

func (s *StreamProcessor) executeRequest(ctx context.Context, req requestSnapshot) {
	start := time.Now()
	wavData := audio.ToWAV(req.pcm)
	resp, err := s.apiClient.Transcribe(ctx, wavData, req.prompt)
	latency := time.Since(start)

	if err != nil {
		status := classifyRequestError(err)
		slog.Info("transcribe request",
			"conn_id", s.connID,
			"req_seq", req.seq,
			"retained_sec", req.retainedDuration,
			"sent_sec", req.sendDurationSec,
			"send_window_offset_sec", req.sendWindowOffset,
			"cap_applied", req.capApplied,
			"latency_ms", latency.Milliseconds(),
			"word_count", 0,
			"segment_count", 0,
			"status", status,
			"error", err,
		)
		s.mu.Lock()
		s.lastProcessedBufferLenByte = req.retainedLenBytes
		s.mu.Unlock()
		return
	}

	committed := s.hypothesis.Process(resp.Words, req.sendWindowOffset)
	s.updateResponseSignature(resp.Words)
	if err := s.sendToClient(committed); err != nil {
		slog.Info("transcribe request",
			"conn_id", s.connID,
			"req_seq", req.seq,
			"retained_sec", req.retainedDuration,
			"sent_sec", req.sendDurationSec,
			"send_window_offset_sec", req.sendWindowOffset,
			"cap_applied", req.capApplied,
			"latency_ms", latency.Milliseconds(),
			"word_count", len(resp.Words),
			"segment_count", len(resp.Segments),
			"status", "client_write_error",
			"error", err,
		)
		s.mu.Lock()
		s.lastProcessedBufferLenByte = req.retainedLenBytes
		s.mu.Unlock()
		s.cancelConn()
		return
	}

	s.mu.Lock()
	s.lastProcessedBufferLenByte = req.retainedLenBytes
	s.lastSendWindowEndSec = req.sendWindowOffset + req.sendDurationSec
	trimmed, cutBytes, trimTo := s.trimBufferLocked(resp.Segments, req.sendWindowOffset)
	s.hypothesis.DiscardCommittedBefore(s.bufferOffsetSec - 60.0)
	s.mu.Unlock()

	slog.Info("transcribe request",
		"conn_id", s.connID,
		"req_seq", req.seq,
		"retained_sec", req.retainedDuration,
		"sent_sec", req.sendDurationSec,
		"send_window_offset_sec", req.sendWindowOffset,
		"cap_applied", req.capApplied,
		"latency_ms", latency.Milliseconds(),
		"word_count", len(resp.Words),
		"segment_count", len(resp.Segments),
		"trimmed", trimmed,
		"trim_cut_bytes", cutBytes,
		"trim_to_sec", trimTo,
		"status", "success",
	)
}

func (s *StreamProcessor) trimBufferLocked(segments []api.Segment, sendWindowOffsetSec float64) (bool, int, float64) {
	retainedDuration := float64(len(s.audioBuf)) / BytesPerSec
	if retainedDuration <= s.cfg.TrimSec {
		return false, 0, s.bufferOffsetSec
	}

	safeCommittedCut := s.hypothesis.LastCommittedTime()
	if safeCommittedCut <= s.bufferOffsetSec {
		return false, 0, s.bufferOffsetSec
	}

	targetCut := safeCommittedCut
	if segmentCut := penultimateSegmentCut(segments, sendWindowOffsetSec, safeCommittedCut, s.bufferOffsetSec); segmentCut > 0 {
		targetCut = segmentCut
	}

	cutSec := targetCut - s.bufferOffsetSec
	cutBytes := int(cutSec * BytesPerSec)
	cutBytes -= cutBytes % 2

	if cutBytes <= 0 || cutBytes >= len(s.audioBuf) {
		return false, 0, s.bufferOffsetSec
	}

	remaining := make([]byte, len(s.audioBuf)-cutBytes)
	copy(remaining, s.audioBuf[cutBytes:])
	s.audioBuf = remaining
	s.bufferOffsetSec += float64(cutBytes) / BytesPerSec

	if s.lastProcessedBufferLenByte >= cutBytes {
		s.lastProcessedBufferLenByte -= cutBytes
	} else {
		s.lastProcessedBufferLenByte = 0
	}
	if s.lastProcessedBufferLenByte > len(s.audioBuf) {
		s.lastProcessedBufferLenByte = len(s.audioBuf)
	}

	return true, cutBytes, s.bufferOffsetSec
}

func penultimateSegmentCut(
	segments []api.Segment,
	sendWindowOffsetSec float64,
	safeCommittedCut float64,
	bufferOffsetSec float64,
) float64 {
	if len(segments) < 2 {
		return 0
	}

	eligible := make([]float64, 0, len(segments))
	for _, seg := range segments {
		absEnd := seg.End + sendWindowOffsetSec
		if absEnd <= safeCommittedCut+0.01 && absEnd > bufferOffsetSec {
			eligible = append(eligible, absEnd)
		}
	}
	if len(eligible) < 2 {
		return 0
	}
	return eligible[len(eligible)-2]
}

func (s *StreamProcessor) flushRemaining() {
	uncommitted := s.hypothesis.Flush()
	if err := s.sendToClient(uncommitted); err != nil {
		if len(uncommitted) > 0 {
			slog.Warn("flush write failed", "conn_id", s.connID, "words", len(uncommitted), "error", err)
		}
	} else if len(uncommitted) > 0 {
		slog.Info("flush", "conn_id", s.connID, "words_flushed", len(uncommitted))
	}
}

// drainAndFlush makes up to 2 forced upstream requests to commit remaining
// hypothesis words, then flushes anything still uncommitted. It uses a
// background context so that requests succeed even after the connection
// context has been canceled.
func (s *StreamProcessor) drainAndFlush() {
	drainCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for i := 0; i < 2; i++ {
		if !s.hypothesis.HasUncommitted() {
			break
		}
		req, ok := s.nextRequestSnapshot(true)
		if !ok {
			break
		}
		s.executeRequest(drainCtx, req)
	}

	s.flushRemaining()
}

// commitStale makes a single forced request when no new audio has arrived
// for a while but hypothesis words remain uncommitted. This ensures tail
// words are committed and written to the client while the connection is
// still open.
func (s *StreamProcessor) commitStale(ctx context.Context) {
	if !s.hypothesis.HasUncommitted() {
		return
	}
	slog.Debug("stale audio, forcing commit", "conn_id", s.connID)
	req, ok := s.nextRequestSnapshot(true)
	if ok {
		s.executeRequest(ctx, req)
	}
}

func (s *StreamProcessor) staleFlushInterval() time.Duration {
	d := time.Duration(s.cfg.MinChunkSec * 1.5 * float64(time.Second))
	if d < 2*time.Second {
		d = 2 * time.Second
	}
	return d
}

func resetTimer(t *time.Timer, d time.Duration) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
	t.Reset(d)
}

func (s *StreamProcessor) updateResponseSignature(words []api.Word) {
	sig := responseSignature(words)
	s.mu.Lock()
	if sig == s.prevResponseSig {
		s.identicalResponseCount++
	} else {
		s.identicalResponseCount = 0
	}
	s.prevResponseSig = sig
	s.mu.Unlock()
}

func responseSignature(words []api.Word) string {
	var sb strings.Builder
	for _, w := range words {
		sb.WriteString(strings.TrimSpace(strings.ToLower(w.Word)))
		sb.WriteByte(' ')
	}
	return sb.String()
}

func (s *StreamProcessor) sendToClient(words []api.Word) error {
	for _, w := range words {
		begMs := w.Start * 1000.0
		endMs := w.End * 1000.0

		if s.lastEndMs > 0 && begMs < s.lastEndMs {
			begMs = s.lastEndMs
		}
		if endMs < begMs {
			endMs = begMs
		}
		s.lastEndMs = endMs

		msg := fmt.Sprintf("%.0f %.0f %s\n", begMs, endMs, strings.TrimSpace(w.Word))
		if _, err := s.conn.Write([]byte(msg)); err != nil {
			return err
		}
	}
	return nil
}

func classifyRequestError(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case api.IsTimeout(err):
		return "timeout"
	default:
		return "upstream_error"
	}
}
