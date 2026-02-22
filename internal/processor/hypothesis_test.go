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
