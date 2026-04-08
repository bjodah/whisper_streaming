package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"whisper-proxy/internal/api"
	"whisper-proxy/internal/processor"
	"whisper-proxy/internal/vad"
)

type Config struct {
	Port             int
	OpenAIBaseURL    string
	OpenAIAPIKey     string
	Language         string
	MinChunkSize     float64
	TrimSec          float64
	HTTPTimeoutSec   float64
	MaxClipLengthSec float64
	ClipOverlapSec   float64
	MaxConnections   int
	ShutdownDrainSec float64
	VADMode          string
	VADRMS           float64
	VADMinSpeechMS   int
	VADMinSilenceMS  int
}

type Server struct {
	config Config

	connSeq   atomic.Uint64
	wg        sync.WaitGroup
	connSlots chan struct{}

	mu          sync.Mutex
	connCancels map[string]context.CancelFunc
}

func New(cfg Config) *Server {
	maxConnections := cfg.MaxConnections
	if maxConnections <= 0 {
		maxConnections = 10
	}

	return &Server{
		config:      cfg,
		connSlots:   make(chan struct{}, maxConnections),
		connCancels: make(map[string]context.CancelFunc),
	}
}

func (s *Server) Listen(ctx context.Context, addr string) error {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	defer func() {
		_ = listener.Close()
	}()

	go func() {
		<-ctx.Done()
		_ = listener.Close()
		s.cancelAllConnections()
	}()

	for {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			if ctx.Err() != nil {
				break
			}

			if errors.Is(acceptErr, net.ErrClosed) {
				break
			}

			slog.Error("failed to accept connection", "error", acceptErr)
			continue
		}

		select {
		case s.connSlots <- struct{}{}:
		default:
			slog.Warn("connection rejected: max connections reached", "remote_addr", conn.RemoteAddr(), "max_connections", cap(s.connSlots))
			_ = conn.Close()
			continue
		}

		connID := fmt.Sprintf("c-%06d", s.connSeq.Add(1))
		s.wg.Add(1)
		go s.handleConnection(ctx, connID, conn)
	}

	waitCh := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(waitCh)
	}()

	drain := time.Duration(s.config.ShutdownDrainSec * float64(time.Second))
	if drain <= 0 {
		<-waitCh
		return nil
	}

	select {
	case <-waitCh:
	case <-time.After(drain):
		slog.Warn("shutdown drain timeout reached", "timeout_sec", s.config.ShutdownDrainSec)
	}

	return nil
}

func (s *Server) handleConnection(serverCtx context.Context, connID string, conn net.Conn) {
	defer s.wg.Done()
	defer func() { <-s.connSlots }()
	defer func() {
		_ = conn.Close()
	}()

	connCtx, cancel := context.WithCancel(serverCtx)
	defer cancel()
	s.registerCancel(connID, cancel)
	defer s.unregisterCancel(connID)

	go func() {
		<-connCtx.Done()
		_ = conn.Close()
	}()

	httpTimeout := time.Duration(s.config.HTTPTimeoutSec * float64(time.Second))
	apiClient := api.NewClient(s.config.OpenAIBaseURL, s.config.OpenAIAPIKey, s.config.Language, httpTimeout)
	vadDetector, err := vad.NewDetector(vad.Config{
		Mode:         s.config.VADMode,
		RMSThreshold: s.config.VADRMS,
		MinSpeechMS:  s.config.VADMinSpeechMS,
		MinSilenceMS: s.config.VADMinSilenceMS,
	})
	if err != nil {
		slog.Error("invalid VAD configuration", "conn_id", connID, "error", err)
		return
	}

	streamCfg := processor.StreamConfig{
		MinChunkSec:      s.config.MinChunkSize,
		TrimSec:          s.config.TrimSec,
		MaxClipLengthSec: s.config.MaxClipLengthSec,
		ClipOverlapSec:   s.config.ClipOverlapSec,
		PromptChars:      200,
		VADDetector:      vadDetector,
	}

	slog.Info("client connected", "conn_id", connID, "remote_addr", conn.RemoteAddr())
	streamProc := processor.NewStreamProcessor(conn, connID, apiClient, streamCfg, cancel)
	streamProc.Run(connCtx)
	slog.Info("client disconnected", "conn_id", connID, "remote_addr", conn.RemoteAddr())
}

func (s *Server) registerCancel(connID string, cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connCancels[connID] = cancel
}

func (s *Server) unregisterCancel(connID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.connCancels, connID)
}

func (s *Server) cancelAllConnections() {
	s.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(s.connCancels))
	for _, cancel := range s.connCancels {
		cancels = append(cancels, cancel)
	}
	s.mu.Unlock()

	for _, cancel := range cancels {
		cancel()
	}
}
