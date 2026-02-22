package audio

import (
	"encoding/binary"
	"testing"
)

func TestToWAV(t *testing.T) {
	pcm := make([]byte, 32000) // 1 second of silence
	wav := ToWAV(pcm)

	if len(wav) != 32044 {
		t.Fatalf("Expected wav length 32044, got %d", len(wav))
	}

	if string(wav[0:4]) != "RIFF" {
		t.Errorf("Expected RIFF header")
	}

	if string(wav[8:12]) != "WAVE" {
		t.Errorf("Expected WAVE format")
	}

	fileSize := binary.LittleEndian.Uint32(wav[4:8])
	if fileSize != 32036 { // 36 + 32000
		t.Errorf("Expected fileSize 32036, got %d", fileSize)
	}
}
