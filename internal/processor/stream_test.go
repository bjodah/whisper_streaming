package processor

import (
	"io"
	"net"
	"testing"
	"time"

	"whisper-proxy/internal/api"
)

func TestComputeSendWindowOffsetSecLocked(t *testing.T) {
	s := &StreamProcessor{
		cfg: StreamConfig{
			MaxClipLengthSec: 20.0,
			ClipOverlapSec:   2.0,
		},
		bufferOffsetSec:      100.0,
		lastSendWindowEndSec: 0,
	}

	got := s.computeSendWindowOffsetSecLocked(30.0)
	if got != 110.0 {
		t.Fatalf("expected capped start 110.0, got %.3f", got)
	}

	s.lastSendWindowEndSec = 126.0
	got = s.computeSendWindowOffsetSecLocked(30.0)
	if got != 124.0 {
		t.Fatalf("expected overlap start 124.0, got %.3f", got)
	}
}

func TestPenultimateSegmentCut(t *testing.T) {
	segments := []api.Segment{
		{Start: 0.0, End: 2.0},
		{Start: 2.0, End: 4.0},
		{Start: 4.0, End: 6.0},
	}
	got := penultimateSegmentCut(segments, 0, 6.5, 0.0)
	if got != 4.0 {
		t.Fatalf("expected penultimate cut at 4.0, got %.3f", got)
	}
}

func TestTrimBufferLockedCopiesRetainedSlice(t *testing.T) {
	audio := make([]byte, 10*BytesPerSec)
	for i := range audio {
		audio[i] = byte(i % 251)
	}

	s := &StreamProcessor{
		cfg: StreamConfig{
			TrimSec: 5.0,
		},
		audioBuf:                   audio,
		bufferOffsetSec:            0.0,
		lastProcessedBufferLenByte: len(audio),
		hypothesis:                 NewHypothesisBuffer(),
	}
	s.hypothesis.lastCommittedTimeSec = 6.0

	trimmed, cutBytes, cutTo := s.trimBufferLocked(nil, 0)
	if !trimmed {
		t.Fatal("expected trimming to occur")
	}
	if cutBytes != 6*BytesPerSec {
		t.Fatalf("expected cut bytes %d, got %d", 6*BytesPerSec, cutBytes)
	}
	if cutTo != 6.0 {
		t.Fatalf("expected cutTo 6.0, got %.3f", cutTo)
	}
	if len(s.audioBuf) != 4*BytesPerSec {
		t.Fatalf("expected remaining audio 4 seconds, got %d bytes", len(s.audioBuf))
	}

	audio[cutBytes] = 255
	if s.audioBuf[0] == 255 {
		t.Fatal("expected retained audio to be copied into a new backing array")
	}
}

type discardConn struct{}

func (discardConn) Read(_ []byte) (int, error)         { return 0, io.EOF }
func (discardConn) Write(p []byte) (int, error)        { return len(p), nil }
func (discardConn) Close() error                       { return nil }
func (discardConn) LocalAddr() net.Addr                { return nil }
func (discardConn) RemoteAddr() net.Addr               { return nil }
func (discardConn) SetDeadline(_ time.Time) error      { return nil }
func (discardConn) SetReadDeadline(_ time.Time) error  { return nil }
func (discardConn) SetWriteDeadline(_ time.Time) error { return nil }

func TestNextRequestSnapshotUsesPromptAndCap(t *testing.T) {
	h := NewHypothesisBuffer()
	words := []api.Word{
		{Word: "old", Start: 0.0, End: 0.4},
		{Word: "context", Start: 0.4, End: 0.8},
	}
	_ = h.Process(words, 0)
	_ = h.Process(words, 0)

	s := &StreamProcessor{
		conn:       discardConn{},
		connID:     "test",
		cfg:        StreamConfig{MinChunkSec: 1.0, MaxClipLengthSec: 2.0, PromptChars: 200},
		hypothesis: h,
		audioBuf:   make([]byte, 5*BytesPerSec),
	}
	s.bufferOffsetSec = 1.0

	req, ok := s.nextRequestSnapshot(false)
	if !ok {
		t.Fatal("expected request snapshot")
	}
	if req.sendDurationSec != 2.0 {
		t.Fatalf("expected capped send duration 2.0, got %.3f", req.sendDurationSec)
	}
	if req.prompt != "old context" {
		t.Fatalf("unexpected prompt: %q", req.prompt)
	}
}

func TestNextRequestSnapshotForceProcess(t *testing.T) {
	s := &StreamProcessor{
		conn:       discardConn{},
		connID:     "test",
		cfg:        StreamConfig{MinChunkSec: 1.0},
		audioBuf:   make([]byte, int(0.2*BytesPerSec)),
		hypothesis: NewHypothesisBuffer(),
	}
	s.forceProcess = true

	if _, ok := s.nextRequestSnapshot(false); !ok {
		t.Fatal("expected forced snapshot with sub-min chunk")
	}
}
