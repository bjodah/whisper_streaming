package vad

import (
	"encoding/binary"
	"math"
)

const (
	sampleRate             = 16000
	bytesPerSample         = 2
	frameMS                = 20
	frameSamples           = sampleRate * frameMS / 1000
	frameBytes             = frameSamples * bytesPerSample
	maxPCM16       float64 = 32768.0
)

type RMSDetector struct {
	threshold        float64
	minSpeechFrames  int
	minSilenceFrames int

	pending []byte

	inSpeech      bool
	speechFrames  int
	silenceFrames int
}

func NewRMSDetector(threshold float64, minSpeechMS, minSilenceMS int) *RMSDetector {
	minSpeechFrames := int(math.Ceil(float64(minSpeechMS) / frameMS))
	if minSpeechFrames < 1 {
		minSpeechFrames = 1
	}
	minSilenceFrames := int(math.Ceil(float64(minSilenceMS) / frameMS))
	if minSilenceFrames < 1 {
		minSilenceFrames = 1
	}

	return &RMSDetector{
		threshold:        threshold,
		minSpeechFrames:  minSpeechFrames,
		minSilenceFrames: minSilenceFrames,
		pending:          make([]byte, 0, frameBytes),
	}
}

func (d *RMSDetector) Reset() {
	d.pending = d.pending[:0]
	d.inSpeech = false
	d.speechFrames = 0
	d.silenceFrames = 0
}

func (d *RMSDetector) Process(pcm []byte) []Event {
	if len(pcm) == 0 {
		return nil
	}

	d.pending = append(d.pending, pcm...)
	if len(d.pending) < frameBytes {
		return nil
	}

	events := make([]Event, 0, 2)
	for len(d.pending) >= frameBytes {
		frame := d.pending[:frameBytes]
		d.pending = d.pending[frameBytes:]

		isSpeech := rms(frame) >= d.threshold
		if isSpeech {
			d.speechFrames++
			d.silenceFrames = 0
			if !d.inSpeech && d.speechFrames >= d.minSpeechFrames {
				d.inSpeech = true
				events = append(events, Event{Type: EventSpeechStart})
			}
			continue
		}

		d.silenceFrames++
		if !d.inSpeech {
			if d.speechFrames > 0 {
				d.speechFrames--
			}
			continue
		}

		if d.silenceFrames >= d.minSilenceFrames {
			d.inSpeech = false
			d.speechFrames = 0
			d.silenceFrames = 0
			events = append(events, Event{Type: EventSpeechEnd})
		}
	}

	return events
}

func rms(frame []byte) float64 {
	if len(frame) < 2 {
		return 0
	}

	sampleCount := len(frame) / 2
	if sampleCount == 0 {
		return 0
	}

	var sum float64
	for i := 0; i+1 < len(frame); i += 2 {
		v := int16(binary.LittleEndian.Uint16(frame[i : i+2]))
		n := float64(v) / maxPCM16
		sum += n * n
	}

	return math.Sqrt(sum / float64(sampleCount))
}
