package processor

import (
	"fmt"
	"slices"
	"strings"
	"whisper-proxy/internal/api"
)

type HypothesisBuffer struct {
	lastCommittedTimeSec float64
	prevHypothesis       []api.Word
	committedTail        []api.Word
	committedHistory     []api.Word
}

func NewHypothesisBuffer() *HypothesisBuffer {
	return &HypothesisBuffer{
		lastCommittedTimeSec: 0,
		prevHypothesis:       make([]api.Word, 0),
		committedTail:        make([]api.Word, 0),
		committedHistory:     make([]api.Word, 0),
	}
}

func (h *HypothesisBuffer) Process(newWords []api.Word, sendWindowOffsetSec float64) []api.Word {
	currWords := make([]api.Word, 0, len(newWords))
	for _, w := range newWords {
		shiftedStart := w.Start + sendWindowOffsetSec
		shiftedEnd := w.End + sendWindowOffsetSec

		if shiftedEnd > h.lastCommittedTimeSec-0.20 {
			currWords = append(currWords, api.Word{
				Word:  w.Word,
				Start: shiftedStart,
				End:   shiftedEnd,
			})
		}
	}
	if len(currWords) == 0 {
		h.prevHypothesis = h.prevHypothesis[:0]
		return nil
	}

	currWords = h.dropCommittedOverlapPrefix(currWords)
	if len(currWords) == 0 {
		h.prevHypothesis = h.prevHypothesis[:0]
		return nil
	}

	matchCount := commonPrefixLen(h.prevHypothesis, currWords)
	if matchCount == 0 {
		h.prevHypothesis = currWords
		return nil
	}

	committed := make([]api.Word, 0, matchCount)
	for i := 0; i < matchCount; i++ {
		candidate := currWords[i]
		if candidate.End <= h.lastCommittedTimeSec {
			continue
		}
		committed = append(committed, candidate)
	}
	if len(committed) > 0 {
		h.lastCommittedTimeSec = committed[len(committed)-1].End
		h.addCommitted(committed)
	}

	h.prevHypothesis = slices.Clone(currWords[matchCount:])

	return committed
}

func (h *HypothesisBuffer) Flush() []api.Word {
	if len(h.prevHypothesis) == 0 {
		return nil
	}

	words := h.dropCommittedOverlapPrefix(h.prevHypothesis)
	if len(words) == 0 {
		h.prevHypothesis = h.prevHypothesis[:0]
		return nil
	}

	out := make([]api.Word, 0, len(words))
	for _, w := range words {
		if w.End <= h.lastCommittedTimeSec {
			continue
		}
		out = append(out, w)
	}
	if len(out) > 0 {
		h.lastCommittedTimeSec = out[len(out)-1].End
		h.addCommitted(out)
	}
	h.prevHypothesis = h.prevHypothesis[:0]
	return out
}

func (h *HypothesisBuffer) LastCommittedTime() float64 {
	return h.lastCommittedTimeSec
}

func (h *HypothesisBuffer) HasUncommitted() bool {
	return len(h.prevHypothesis) > 0
}

func (h *HypothesisBuffer) PromptOutsideBuffer(bufferOffsetSec float64, maxChars int) string {
	if maxChars <= 0 || len(h.committedHistory) == 0 {
		return ""
	}

	parts := make([]string, 0, len(h.committedHistory))
	for _, w := range h.committedHistory {
		if w.End <= bufferOffsetSec {
			word := strings.TrimSpace(w.Word)
			if word != "" {
				parts = append(parts, word)
			}
		}
	}
	if len(parts) == 0 {
		return ""
	}

	joined := strings.Join(parts, " ")
	if len(joined) <= maxChars {
		return joined
	}

	return joined[len(joined)-maxChars:]
}

func (h *HypothesisBuffer) DiscardCommittedBefore(minKeepEndSec float64) {
	if len(h.committedHistory) == 0 {
		return
	}

	firstKeep := 0
	for i, w := range h.committedHistory {
		if w.End >= minKeepEndSec {
			firstKeep = i
			break
		}
		if i == len(h.committedHistory)-1 {
			firstKeep = len(h.committedHistory)
		}
	}
	if firstKeep > 0 {
		h.committedHistory = slices.Clone(h.committedHistory[firstKeep:])
	}
}

func (h *HypothesisBuffer) addCommitted(words []api.Word) {
	h.committedTail = append(h.committedTail, words...)
	const maxTailWords = 64
	if len(h.committedTail) > maxTailWords {
		h.committedTail = slices.Clone(h.committedTail[len(h.committedTail)-maxTailWords:])
	}

	h.committedHistory = append(h.committedHistory, words...)
	const maxHistoryWords = 4096
	if len(h.committedHistory) > maxHistoryWords {
		h.committedHistory = slices.Clone(h.committedHistory[len(h.committedHistory)-maxHistoryWords:])
	}
}

func (h *HypothesisBuffer) dropCommittedOverlapPrefix(words []api.Word) []api.Word {
	if len(words) == 0 || len(h.committedTail) == 0 {
		return words
	}

	maxK := minInt(5, len(words), len(h.committedTail))
	best := 0
	for k := maxK; k >= 1; k-- {
		tail := h.committedTail[len(h.committedTail)-k:]
		if equalNormWordSeq(tail, words[:k]) {
			best = k
			break
		}
	}
	if best == 0 {
		return words
	}
	return words[best:]
}

func commonPrefixLen(a, b []api.Word) int {
	limit := len(a)
	if len(b) < limit {
		limit = len(b)
	}

	n := 0
	for i := 0; i < limit; i++ {
		if norm(a[i].Word) != norm(b[i].Word) {
			break
		}
		n++
	}
	return n
}

func equalNormWordSeq(a, b []api.Word) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if norm(a[i].Word) != norm(b[i].Word) {
			return false
		}
	}
	return true
}

func minInt(v int, values ...int) int {
	out := v
	for _, n := range values {
		if n < out {
			out = n
		}
	}
	return out
}

func norm(w string) string {
	return strings.TrimSpace(strings.ToLower(strings.ReplaceAll(w, "\n", " ")))
}

func (h *HypothesisBuffer) String() string {
	return fmt.Sprintf("lastCommitted=%.3f prev=%d committedTail=%d committedHistory=%d",
		h.lastCommittedTimeSec, len(h.prevHypothesis), len(h.committedTail), len(h.committedHistory))
}
