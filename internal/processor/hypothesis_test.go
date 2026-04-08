package processor

import (
	"testing"

	"whisper-proxy/internal/api"
)

func TestHypothesisBuffer(t *testing.T) {
	h := NewHypothesisBuffer()

	// Simulation: API call 1
	words1 := []api.Word{
		{Word: "Hello", Start: 0.0, End: 0.5},
		{Word: "world", Start: 0.5, End: 1.0},
		{Word: "this", Start: 1.0, End: 1.5},
	}
	res1 := h.Process(words1, 0)
	if len(res1) != 0 {
		t.Errorf("First iteration should commit nothing, got %v", res1)
	}

	// Simulation: API call 2 (Overlapping)
	words2 := []api.Word{
		{Word: "Hello", Start: 0.0, End: 0.5},
		{Word: "world", Start: 0.5, End: 1.0},
		{Word: "is", Start: 1.0, End: 1.2},
	}
	res2 := h.Process(words2, 0)

	// We expect "Hello" and "world" to match and be committed.
	if len(res2) != 2 {
		t.Fatalf("Expected 2 committed words, got %d", len(res2))
	}
	if res2[0].Word != "Hello" || res2[1].Word != "world" {
		t.Errorf("Unexpected committed words: %v", res2)
	}

	// The last committed time should be the end of "world" (1.0)
	if h.LastCommittedTime() != 1.0 {
		t.Errorf("Expected LastCommittedTime 1.0, got %f", h.LastCommittedTime())
	}
}

func TestHypothesisBufferTimestampNormalization(t *testing.T) {
	h := NewHypothesisBuffer()

	base := []api.Word{
		{Word: "one", Start: 0.0, End: 0.4},
		{Word: "two", Start: 0.4, End: 0.8},
	}

	if got := h.Process(base, 10.0); len(got) != 0 {
		t.Fatalf("expected no commit on first pass, got %d", len(got))
	}

	got := h.Process(base, 10.0)
	if len(got) != 2 {
		t.Fatalf("expected 2 committed words, got %d", len(got))
	}
	if got[0].Start != 10.0 || got[1].End != 10.8 {
		t.Fatalf("unexpected absolute timestamps: %#v", got)
	}
}

func TestHypothesisBufferDropsCommittedOverlap(t *testing.T) {
	h := NewHypothesisBuffer()

	pass1 := []api.Word{
		{Word: "alpha", Start: 0.0, End: 0.4},
		{Word: "beta", Start: 0.4, End: 0.8},
	}
	_ = h.Process(pass1, 0)
	committed := h.Process(pass1, 0)
	if len(committed) != 2 {
		t.Fatalf("expected initial commit of 2 words, got %d", len(committed))
	}

	pass3 := []api.Word{
		{Word: "beta", Start: 0.4, End: 0.8},
		{Word: "gamma", Start: 0.8, End: 1.2},
	}
	if got := h.Process(pass3, 0); len(got) != 0 {
		t.Fatalf("expected no commit while hypothesis stabilizes, got %d", len(got))
	}

	pass4 := []api.Word{
		{Word: "beta", Start: 0.4, End: 0.8},
		{Word: "gamma", Start: 0.8, End: 1.2},
		{Word: "delta", Start: 1.2, End: 1.6},
	}
	got := h.Process(pass4, 0)
	if len(got) != 1 || got[0].Word != "gamma" {
		t.Fatalf("expected gamma to commit after overlap dedup, got %#v", got)
	}
}

func TestHypothesisPromptOutsideBuffer(t *testing.T) {
	h := NewHypothesisBuffer()
	words := []api.Word{
		{Word: "this", Start: 0, End: 0.3},
		{Word: "is", Start: 0.3, End: 0.6},
		{Word: "context", Start: 0.6, End: 1.0},
	}
	_ = h.Process(words, 0)
	_ = h.Process(words, 0)

	prompt := h.PromptOutsideBuffer(1.0, 200)
	if prompt != "this is context" {
		t.Fatalf("unexpected prompt: %q", prompt)
	}

	short := h.PromptOutsideBuffer(1.0, 4)
	if short != "text" {
		t.Fatalf("expected right-trimmed prompt, got %q", short)
	}
}
