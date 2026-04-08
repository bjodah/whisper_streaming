package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTranscribeIncludesGranularitiesPromptAndModel(t *testing.T) {
	var gotPrompt string
	var gotGranularities []string
	var gotLanguage string
	var gotModel string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			t.Fatalf("ParseMultipartForm failed: %v", err)
		}

		gotPrompt = r.FormValue("prompt")
		gotLanguage = r.FormValue("language")
		gotModel = r.FormValue("model")
		gotGranularities = r.MultipartForm.Value["timestamp_granularities[]"]

		resp := TranscriptionResponse{
			Text: "ok",
			Words: []Word{
				{Word: "ok", Start: 0, End: 0.5},
			},
			Segments: []Segment{
				{Start: 0, End: 0.5, Text: "ok"},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "k", "Systran/faster-distil-whisper-large-v3", "en", 30*time.Second)
	resp, err := c.Transcribe(context.Background(), []byte{0, 1, 2}, "prior context")
	if err != nil {
		t.Fatalf("Transcribe failed: %v", err)
	}
	if len(resp.Words) != 1 || len(resp.Segments) != 1 {
		t.Fatalf("unexpected response payload: %#v", resp)
	}

	if gotPrompt != "prior context" {
		t.Fatalf("expected prompt to be sent, got %q", gotPrompt)
	}
	if gotLanguage != "en" {
		t.Fatalf("expected language en, got %q", gotLanguage)
	}
	if gotModel != "Systran/faster-distil-whisper-large-v3" {
		t.Fatalf("expected configured model, got %q", gotModel)
	}
	if len(gotGranularities) != 2 {
		t.Fatalf("expected 2 granularities, got %#v", gotGranularities)
	}

	hasWord := false
	hasSegment := false
	for _, g := range gotGranularities {
		if g == "word" {
			hasWord = true
		}
		if g == "segment" {
			hasSegment = true
		}
	}
	if !hasWord || !hasSegment {
		t.Fatalf("missing granularities word/segment: %#v", gotGranularities)
	}
}

func TestTranscribeTimeoutIsClassified(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"text":"x","words":[],"segments":[]}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "k", "", "", 3*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := c.Transcribe(ctx, []byte{0}, "")
	if err == nil {
		t.Fatal("expected timeout/cancel error")
	}
	if !IsTimeout(err) {
		t.Fatalf("expected timeout classification, got %v", err)
	}
}
