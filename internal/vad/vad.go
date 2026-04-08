package vad

import (
	"fmt"
)

type EventType string

const (
	EventSpeechStart EventType = "speech_start"
	EventSpeechEnd   EventType = "speech_end"
)

type Event struct {
	Type EventType
}

type Detector interface {
	Reset()
	Process(pcm []byte) []Event
}

type Config struct {
	Mode         string
	RMSThreshold float64
	MinSpeechMS  int
	MinSilenceMS int
}

func NewDetector(cfg Config) (Detector, error) {
	switch cfg.Mode {
	case "", "off":
		return nil, nil
	case "rms":
		threshold := cfg.RMSThreshold
		if threshold <= 0 {
			threshold = 0.02
		}
		minSpeech := cfg.MinSpeechMS
		if minSpeech <= 0 {
			minSpeech = 120
		}
		minSilence := cfg.MinSilenceMS
		if minSilence <= 0 {
			minSilence = 400
		}
		return NewRMSDetector(threshold, minSpeech, minSilence), nil
	default:
		return nil, fmt.Errorf("unsupported vad mode %q (supported: off,rms)", cfg.Mode)
	}
}
