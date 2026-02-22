package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"

	"whisper-proxy/internal/server"
)

func main() {
	port := flag.Int("port", 43007, "TCP port to listen on")
	language := flag.String("language", "", "Language code (e.g., 'en'). Leave empty for auto-detect.")
	minChunk := flag.Float64("min-chunk-size", 1.0, "Minimum audio chunk size in seconds")
	trimSec := flag.Float64("buffer-trimming-sec", 15.0, "Buffer trimming length threshold in seconds")
	debug := flag.Bool("debug", false, "Enable debug logging")
	flag.Parse()

	// Configure logging
	logLevel := slog.LevelInfo
	if *debug {
		logLevel = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))
	slog.SetDefault(logger)

	// Environment variables
	baseURL := os.Getenv("OPENAI_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		slog.Warn("OPENAI_API_KEY is not set. API calls may fail.")
	}

	cfg := server.Config{
		Port:          *port,
		OpenAIBaseURL: baseURL,
		OpenAIAPIKey:  apiKey,
		Language:      *language,
		MinChunkSize:  *minChunk,
		TrimSec:       *trimSec,
	}

	srv := server.New(cfg)
	addr := fmt.Sprintf(":%d", cfg.Port)
	slog.Info("Starting Whisper Proxy Server", "addr", addr, "baseURL", baseURL)

	if err := srv.Listen(addr); err != nil {
		slog.Error("Server failed", "error", err)
		os.Exit(1)
	}
}
