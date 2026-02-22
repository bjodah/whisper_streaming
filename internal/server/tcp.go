package server

import (
	"net"
	"log/slog"
	
	"whisper-proxy/internal/api"
	"whisper-proxy/internal/processor"
)

type Config struct {
	Port          int
	OpenAIBaseURL string
	OpenAIAPIKey  string
	Language      string
	MinChunkSize  float64
	TrimSec       float64
}

type Server struct {
	config Config
}

func New(cfg Config) *Server {
	return &Server{config: cfg}
}

func (s *Server) Listen(addr string) error {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	defer listener.Close()

	for {
		conn, err := listener.Accept()
		if err != nil {
			slog.Error("Failed to accept connection", "error", err)
			continue
		}

		slog.Info("Client connected", "remote_addr", conn.RemoteAddr())
		go s.handleConnection(conn)
	}
}

func (s *Server) handleConnection(conn net.Conn) {
	defer conn.Close()

	apiClient := api.NewClient(s.config.OpenAIBaseURL, s.config.OpenAIAPIKey, s.config.Language)
	streamProc := processor.NewStreamProcessor(conn, apiClient, s.config.MinChunkSize, s.config.TrimSec)
	
	streamProc.Run()
	slog.Info("Client disconnected", "remote_addr", conn.RemoteAddr())
}
