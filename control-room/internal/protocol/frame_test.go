package protocol

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	payloads := [][]byte{
		[]byte(`{"a":1}`),
		[]byte("x"),
		bytes.Repeat([]byte("z"), 4096),
	}
	var buf bytes.Buffer
	for _, p := range payloads {
		if err := WriteFrame(&buf, p); err != nil {
			t.Fatalf("WriteFrame: %v", err)
		}
	}
	for i, want := range payloads {
		got, err := ReadFrame(&buf)
		if err != nil {
			t.Fatalf("ReadFrame[%d]: %v", i, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("frame[%d] mismatch: got %q want %q", i, got, want)
		}
	}
	// After consuming all frames, a clean EOF is expected.
	if _, err := ReadFrame(&buf); !errors.Is(err, io.EOF) {
		t.Fatalf("expected io.EOF after last frame, got %v", err)
	}
}

func TestWriteFrameRejectsEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteFrame(&buf, nil); !errors.Is(err, ErrEmptyFrame) {
		t.Fatalf("expected ErrEmptyFrame, got %v", err)
	}
}

func TestWriteFrameRejectsOversized(t *testing.T) {
	var buf bytes.Buffer
	big := make([]byte, MaxFrameBytes+1)
	if err := WriteFrame(&buf, big); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("expected ErrFrameTooLarge, got %v", err)
	}
}

// TestReadFrameRejectsOversizedBeforeAllocation verifies the reader rejects a
// hostile length header without reading (or allocating) the declared payload.
// We provide only the 4-byte header and no payload; if the reader tried to
// allocate/read the payload it would block or error differently.
func TestReadFrameRejectsOversizedBeforeAllocation(t *testing.T) {
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(MaxFrameBytes+1))
	r := bytes.NewReader(header[:])
	_, err := ReadFrame(r)
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("expected ErrFrameTooLarge, got %v", err)
	}
	// The reader must have consumed exactly the 4 header bytes and stopped.
	if r.Len() != 0 {
		t.Fatalf("expected header fully consumed, %d bytes remain", r.Len())
	}
}

func TestReadFrameRejectsZeroLength(t *testing.T) {
	var header [4]byte // length 0
	_, err := ReadFrame(bytes.NewReader(header[:]))
	if !errors.Is(err, ErrEmptyFrame) {
		t.Fatalf("expected ErrEmptyFrame, got %v", err)
	}
}

func TestReadFrameTruncatedPayload(t *testing.T) {
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], 10)
	// Only 3 payload bytes for a declared 10.
	data := append(header[:], []byte("abc")...)
	_, err := ReadFrame(bytes.NewReader(data))
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("expected io.ErrUnexpectedEOF, got %v", err)
	}
}

func TestReadFrameTruncatedHeader(t *testing.T) {
	// Two bytes of a four-byte header.
	_, err := ReadFrame(bytes.NewReader([]byte{0x00, 0x01}))
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("expected io.ErrUnexpectedEOF on truncated header, got %v", err)
	}
}

func TestReadFrameCleanEOF(t *testing.T) {
	_, err := ReadFrame(bytes.NewReader(nil))
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected io.EOF on empty stream, got %v", err)
	}
}

// TestFrameThenParsePlan exercises the codec feeding the strict plan parser,
// which is the intended Phase 0 pipeline (one framed payload = one JSON value).
func TestFrameThenParsePlan(t *testing.T) {
	plan := validPlan()
	// Marshal via the codec path: encode plan, frame it, read it back, parse.
	raw := mustMarshalPlan(t, plan)
	var buf bytes.Buffer
	if err := WriteFrame(&buf, raw); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	got, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	parsed, err := ParsePlan(got)
	if err != nil {
		t.Fatalf("ParsePlan: %v", err)
	}
	if parsed.SessionID != plan.SessionID {
		t.Fatalf("round-trip mismatch: %q != %q", parsed.SessionID, plan.SessionID)
	}
}
