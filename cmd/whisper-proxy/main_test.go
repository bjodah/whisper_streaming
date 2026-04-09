package main

import "testing"

func TestResolveUpstreamBaseURLPrecedence(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "https://legacy.example/v1")
	t.Setenv("WHISPER_STREAMING_UPSTREAM_BASE_URL", "https://preferred.example/v1")

	if got := resolveUpstreamBaseURL(""); got != "https://preferred.example/v1" {
		t.Fatalf("expected new env var to override legacy env var, got %q", got)
	}
	if got := resolveUpstreamBaseURL("https://flag.example/v1"); got != "https://flag.example/v1" {
		t.Fatalf("expected flag to override env vars, got %q", got)
	}
}

func TestResolveUpstreamBaseURLFallsBackToLegacyAndDefault(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "https://legacy.example/v1")
	t.Setenv("WHISPER_STREAMING_UPSTREAM_BASE_URL", "")

	if got := resolveUpstreamBaseURL(""); got != "https://legacy.example/v1" {
		t.Fatalf("expected legacy env var fallback, got %q", got)
	}

	t.Setenv("OPENAI_BASE_URL", "")
	if got := resolveUpstreamBaseURL(""); got != defaultUpstreamBaseURL {
		t.Fatalf("expected default base URL fallback, got %q", got)
	}
}

func TestResolveUpstreamAPIKeyPrecedence(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "legacy-key")
	t.Setenv("WHISPER_STREAMING_UPSTREAM_API_KEY", "preferred-key")

	if got := resolveUpstreamAPIKey(""); got != "preferred-key" {
		t.Fatalf("expected new env var to override legacy env var, got %q", got)
	}
	if got := resolveUpstreamAPIKey("flag-key"); got != "flag-key" {
		t.Fatalf("expected flag to override env vars, got %q", got)
	}
}

func TestResolveUpstreamAPIKeyFallsBackToLegacy(t *testing.T) {
	t.Setenv("WHISPER_STREAMING_UPSTREAM_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "legacy-key")

	if got := resolveUpstreamAPIKey(""); got != "legacy-key" {
		t.Fatalf("expected legacy env var fallback, got %q", got)
	}
}
