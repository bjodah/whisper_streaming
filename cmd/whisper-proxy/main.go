package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"whisper-proxy/internal/server"
)

const defaultUpstreamBaseURL = "https://api.openai.com/v1"

func resolveUpstreamBaseURL(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if v := os.Getenv("WHISPER_STREAMING_UPSTREAM_BASE_URL"); v != "" {
		return v
	}
	if v := os.Getenv("OPENAI_BASE_URL"); v != "" {
		return v
	}
	return defaultUpstreamBaseURL
}

func resolveUpstreamAPIKey(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if v := os.Getenv("WHISPER_STREAMING_UPSTREAM_API_KEY"); v != "" {
		return v
	}
	return os.Getenv("OPENAI_API_KEY")
}

func main() {
	port := flag.Int("port", 43007, "TCP port to listen on")
	upstreamBaseURL := flag.String("upstream-base-url", "", "Upstream transcription API base URL")
	upstreamAPIKey := flag.String("upstream-api-key", "", "Upstream transcription API key")
	model := flag.String("model", "whisper-1", "Upstream transcription model name")
	language := flag.String("language", "", "Language code (e.g., 'en'). Leave empty for auto-detect.")
	minChunk := flag.Float64("min-chunk-size", 1.0, "Minimum audio chunk size in seconds")
	trimSec := flag.Float64("buffer-trimming-sec", 15.0, "Buffer trimming length threshold in seconds")
	httpTimeoutSec := flag.Float64("http-timeout-sec", 30.0, "Upstream HTTP timeout in seconds")
	maxClipSec := flag.Float64("max-clip-length-sec", 20.0, "Hard cap for upstream clip duration in seconds")
	clipOverlapSec := flag.Float64("clip-overlap-sec", 2.0, "Requested overlap between consecutive capped clip windows in seconds")
	maxConnections := flag.Int("max-connections", 10, "Maximum number of concurrent TCP connections")
	shutdownDrainSec := flag.Float64("shutdown-drain-sec", 5.0, "Graceful shutdown drain timeout in seconds")
	vadMode := flag.String("vad", "off", "Voice activity detection mode: off|rms")
	vadRMSThreshold := flag.Float64("vad-rms-threshold", 0.02, "RMS VAD speech threshold (normalized 0..1)")
	vadMinSpeechMS := flag.Int("vad-min-speech-ms", 120, "Minimum speech duration before speech start event")
	vadMinSilenceMS := flag.Int("vad-min-silence-ms", 400, "Minimum silence duration before speech end event")
	debug := flag.Bool("debug", false, "Enable debug logging")
	flag.Parse()

	// Configure logging
	logLevel := slog.LevelInfo
	if *debug {
		logLevel = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))
	slog.SetDefault(logger)

	baseURL := resolveUpstreamBaseURL(*upstreamBaseURL)
	apiKey := resolveUpstreamAPIKey(*upstreamAPIKey)
	if apiKey == "" {
		slog.Warn("upstream API key is not set; requests may fail")
	}

	cfg := server.Config{
		Port:             *port,
		OpenAIBaseURL:    baseURL,
		OpenAIAPIKey:     apiKey,
		Model:            *model,
		Language:         *language,
		MinChunkSize:     *minChunk,
		TrimSec:          *trimSec,
		HTTPTimeoutSec:   *httpTimeoutSec,
		MaxClipLengthSec: *maxClipSec,
		ClipOverlapSec:   *clipOverlapSec,
		MaxConnections:   *maxConnections,
		ShutdownDrainSec: *shutdownDrainSec,
		VADMode:          *vadMode,
		VADRMS:           *vadRMSThreshold,
		VADMinSpeechMS:   *vadMinSpeechMS,
		VADMinSilenceMS:  *vadMinSilenceMS,
	}

	srv := server.New(cfg)
	addr := fmt.Sprintf(":%d", cfg.Port)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	slog.Info("starting whisper proxy server",
		"addr", addr,
		"base_url", cfg.OpenAIBaseURL,
		"model", cfg.Model,
		"http_timeout_sec", cfg.HTTPTimeoutSec,
		"max_clip_length_sec", cfg.MaxClipLengthSec,
		"clip_overlap_sec", cfg.ClipOverlapSec,
		"max_connections", cfg.MaxConnections,
		"vad", cfg.VADMode,
	)

	if err := srv.Listen(ctx, addr); err != nil {
		slog.Error("Server failed", "error", err)
		os.Exit(1)
	}
}
