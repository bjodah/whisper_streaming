package processor

import (
	"strings"
	"whisper-proxy/internal/api"
)

type HypothesisBuffer struct {
	lastCommittedTime float64
	prevWords         []api.Word
}

func NewHypothesisBuffer() *HypothesisBuffer {
	return &HypothesisBuffer{
		lastCommittedTime: 0,
		prevWords:         make([]api.Word, 0),
	}
}

// Process evaluates new API words, shifts them by bufferOffset,
// and finds overlapping agreements (LocalAgreement).
func (h *HypothesisBuffer) Process(newWords []api.Word, bufferOffset float64) []api.Word {
	// Shift times by offset and filter out older words
	var currWords []api.Word
	for _, w := range newWords {
		shiftedStart := w.Start + bufferOffset
		shiftedEnd := w.End + bufferOffset
		
		// Only consider words that are strictly near/after the last commit
		if shiftedStart >= h.lastCommittedTime-0.1 {
			currWords = append(currWords, api.Word{
				Word:  w.Word,
				Start: shiftedStart,
				End:   shiftedEnd,
			})
		}
	}

	// Find longest common prefix between prevWords and currWords
	var committed []api.Word
	matchCount := 0
	
	minLen := len(h.prevWords)
	if len(currWords) < minLen {
		minLen = len(currWords)
	}

	for i := 0; i < minLen; i++ {
		// Compare normalized text
		if norm(h.prevWords[i].Word) == norm(currWords[i].Word) {
			matchCount++
		} else {
			break
		}
	}

	if matchCount > 0 {
		committed = currWords[:matchCount]
		h.lastCommittedTime = currWords[matchCount-1].End
		h.prevWords = currWords[matchCount:]
	} else {
		// No match, just save currWords for next iteration
		h.prevWords = currWords
	}

	return committed
}

// Flush returns whatever is left uncommitted (used on stream end).
func (h *HypothesisBuffer) Flush() []api.Word {
	return h.prevWords
}

// LastCommittedTime returns the tracking time
func (h *HypothesisBuffer) LastCommittedTime() float64 {
	return h.lastCommittedTime
}

func norm(w string) string {
	return strings.TrimSpace(strings.ToLower(w))
}
