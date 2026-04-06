package vad

import (
	"encoding/binary"
	"testing"
)

func TestRMSDetectorSpeechStartAndEnd(t *testing.T) {
	d := NewRMSDetector(0.02, 40, 60)

	speech := makePCMFrames(4, 5000)
	silence := makePCMFrames(4, 0)

	events := d.Process(speech)
	if len(events) == 0 || events[0].Type != EventSpeechStart {
		t.Fatalf("expected speech start, got %#v", events)
	}

	events = d.Process(silence)
	foundEnd := false
	for _, ev := range events {
		if ev.Type == EventSpeechEnd {
			foundEnd = true
			break
		}
	}
	if !foundEnd {
		t.Fatalf("expected speech end event, got %#v", events)
	}
}

func makePCMFrames(n int, amp int16) []byte {
	out := make([]byte, n*frameBytes)
	for i := 0; i+1 < len(out); i += 2 {
		binary.LittleEndian.PutUint16(out[i:i+2], uint16(amp))
	}
	return out
}
