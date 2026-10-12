package protocol

import (
	"encoding/binary"
	"errors"
	"io"
)

// MaxFrameSize is the largest frame payload in bytes. The worst-case msg
// (160 runes that JSON escapes to 6 bytes each) plus a 16-byte AEAD tag
// still fits.
const MaxFrameSize = 1024

// ErrFrameTooLarge means a frame's length is over MaxFrameSize. When
// ReadFrame returns it, the stream is out of sync and must be closed.
var ErrFrameTooLarge = errors.New("protocol: frame too large")

// WriteFrame writes payload as a big-endian uint16 length followed by the
// bytes, in a single Write call so concurrent writers can't interleave halves
// of a frame (as long as w itself is safe for concurrent use).
func WriteFrame(w io.Writer, payload []byte) error {
	if len(payload) > MaxFrameSize {
		return ErrFrameTooLarge
	}
	buf := make([]byte, 2+len(payload))
	binary.BigEndian.PutUint16(buf, uint16(len(payload)))
	copy(buf[2:], payload)
	_, err := w.Write(buf)
	return err
}

// ReadFrame reads one frame written by WriteFrame. It returns io.EOF if the
// stream ends cleanly between frames, and io.ErrUnexpectedEOF if it ends
// partway through one.
func ReadFrame(r io.Reader) ([]byte, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint16(hdr[:])
	if n > MaxFrameSize {
		return nil, ErrFrameTooLarge
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	return buf, nil
}

// WriteMessage encodes m and writes it as one frame.
func WriteMessage(w io.Writer, m Message) error {
	b, err := Encode(m)
	if err != nil {
		return err
	}
	return WriteFrame(w, b)
}

// ReadMessage reads one frame and decodes it.
//
// Errors from ReadFrame (I/O errors, ErrFrameTooLarge) come back wrapped in a
// *StreamError and mean the connection is done. Errors from Decode mean only
// this message is bad. Use IsFatal to tell them apart.
func ReadMessage(r io.Reader) (Message, error) {
	b, err := ReadFrame(r)
	if err != nil {
		return Message{}, &StreamError{Err: err}
	}
	return Decode(b)
}

// StreamError wraps an error after which the stream can't be read any
// further: an I/O error, a bad frame length, or (in the crypto layer) a frame
// that fails authentication.
type StreamError struct{ Err error }

func (e *StreamError) Error() string { return e.Err.Error() }
func (e *StreamError) Unwrap() error { return e.Err }

// IsFatal reports whether err means the stream can't be read any further, as
// opposed to one bad message that can be skipped.
func IsFatal(err error) bool {
	var se *StreamError
	return errors.As(err, &se)
}
