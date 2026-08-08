package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Frame codec.
//
// The Phase 0 agent transport uses length-prefixed JSON framing: a 4-byte
// big-endian unsigned length header followed by exactly that many bytes of
// UTF-8 JSON payload. This is chosen over newline-delimited JSON because it
// bounds a frame's size before any payload is read — the reader can reject an
// oversized frame from the header alone, without allocating a buffer sized to
// attacker-controlled input.
//
// The payload is a single JSON value. Trailing or multiple JSON values within
// one frame are rejected by the payload parser (see decodeSingleStrictJSON);
// the codec itself guarantees exactly one framed payload per ReadFrame call.

// frameHeaderBytes is the size of the length prefix.
const frameHeaderBytes = 4

// ErrFrameTooLarge indicates a frame whose declared length exceeds
// MaxFrameBytes. It is returned before any payload allocation.
var ErrFrameTooLarge = errors.New("protocol: frame exceeds maximum size")

// ErrEmptyFrame indicates a frame with a declared length of zero.
var ErrEmptyFrame = errors.New("protocol: empty frame")

// WriteFrame writes payload as a single length-prefixed frame to w.
//
// It refuses to write a frame larger than MaxFrameBytes so a local bug cannot
// emit a frame a conforming reader would then reject.
func WriteFrame(w io.Writer, payload []byte) error {
	if len(payload) == 0 {
		return ErrEmptyFrame
	}
	if len(payload) > MaxFrameBytes {
		return fmt.Errorf("%w: %d bytes", ErrFrameTooLarge, len(payload))
	}
	var header [frameHeaderBytes]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if _, err := w.Write(header[:]); err != nil {
		return fmt.Errorf("protocol: writing frame header: %w", err)
	}
	if _, err := w.Write(payload); err != nil {
		return fmt.Errorf("protocol: writing frame payload: %w", err)
	}
	return nil
}

// ReadFrame reads a single length-prefixed frame from r and returns its
// payload bytes.
//
// The declared length is validated against MaxFrameBytes before the payload
// buffer is allocated, so a hostile length header cannot force a large
// allocation. A truncated frame (EOF mid-payload) is reported as
// io.ErrUnexpectedEOF. A clean EOF before any header byte is reported as
// io.EOF so callers can distinguish "stream closed" from "malformed frame".
func ReadFrame(r io.Reader) ([]byte, error) {
	var header [frameHeaderBytes]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		if errors.Is(err, io.EOF) {
			// Distinguish a clean close (0 bytes read) from a truncated header.
			return nil, io.EOF
		}
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, io.ErrUnexpectedEOF
		}
		return nil, fmt.Errorf("protocol: reading frame header: %w", err)
	}
	length := binary.BigEndian.Uint32(header[:])
	if length == 0 {
		return nil, ErrEmptyFrame
	}
	if length > MaxFrameBytes {
		return nil, fmt.Errorf("%w: declared %d bytes", ErrFrameTooLarge, length)
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, io.ErrUnexpectedEOF
		}
		return nil, fmt.Errorf("protocol: reading frame payload: %w", err)
	}
	return payload, nil
}
