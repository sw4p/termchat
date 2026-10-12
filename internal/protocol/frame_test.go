package protocol

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	payloads := [][]byte{
		[]byte("hello"),
		{},
		bytes.Repeat([]byte{0xff}, MaxFrameSize),
	}
	var buf bytes.Buffer
	for _, p := range payloads {
		if err := WriteFrame(&buf, p); err != nil {
			t.Fatalf("WriteFrame(%d bytes): %v", len(p), err)
		}
	}
	for _, want := range payloads {
		got, err := ReadFrame(&buf)
		if err != nil {
			t.Fatalf("ReadFrame: %v", err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("ReadFrame = %d bytes, want %d", len(got), len(want))
		}
	}
	if _, err := ReadFrame(&buf); err != io.EOF {
		t.Errorf("ReadFrame at end = %v, want io.EOF", err)
	}
}

func TestFrameWireFormat(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteFrame(&buf, []byte("hi")); err != nil {
		t.Fatal(err)
	}
	if want := []byte{0x00, 0x02, 'h', 'i'}; !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("wire bytes = % x, want % x", buf.Bytes(), want)
	}
}

func TestWriteFrameTooLarge(t *testing.T) {
	var buf bytes.Buffer
	err := WriteFrame(&buf, make([]byte, MaxFrameSize+1))
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Errorf("err = %v, want ErrFrameTooLarge", err)
	}
	if buf.Len() != 0 {
		t.Errorf("wrote %d bytes after rejecting the frame", buf.Len())
	}
}

func TestReadFrameTooLarge(t *testing.T) {
	for _, hdr := range [][]byte{{0x04, 0x01}, {0xff, 0xff}} { // 1025, 65535
		// No payload follows: ReadFrame must reject on the header alone
		// instead of waiting for (or allocating) the claimed bytes.
		_, err := ReadFrame(bytes.NewReader(hdr))
		if !errors.Is(err, ErrFrameTooLarge) {
			t.Errorf("header % x: err = %v, want ErrFrameTooLarge", hdr, err)
		}
	}
}

func TestReadFrameTruncated(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
	}{
		{"half a header", []byte{0x00}},
		{"short payload", []byte{0x00, 0x0a, 'a', 'b', 'c'}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ReadFrame(bytes.NewReader(tc.in))
			if err != io.ErrUnexpectedEOF {
				t.Errorf("err = %v, want io.ErrUnexpectedEOF", err)
			}
		})
	}
}

func TestMessageStream(t *testing.T) {
	var buf bytes.Buffer
	sent := []Message{
		{Type: TypeHello, ID: testID, Nick: "quiet-otter"},
		{Type: TypeMsg, Text: "hi 👋"},
		{Type: TypeLeave},
	}
	for _, m := range sent {
		if err := WriteMessage(&buf, m); err != nil {
			t.Fatalf("WriteMessage(%+v): %v", m, err)
		}
	}
	// A frame from a newer peer with a type we don't know: skippable.
	if err := WriteFrame(&buf, []byte(`{"t":"reaction","text":"+1"}`)); err != nil {
		t.Fatal(err)
	}

	for _, want := range sent {
		got, err := ReadMessage(&buf)
		if err != nil {
			t.Fatalf("ReadMessage: %v", err)
		}
		if got != want {
			t.Errorf("ReadMessage = %+v, want %+v", got, want)
		}
	}

	_, err := ReadMessage(&buf)
	if !errors.Is(err, ErrUnknownType) || IsFatal(err) {
		t.Errorf("unknown type: err = %v (fatal=%v), want non-fatal ErrUnknownType", err, IsFatal(err))
	}
	_, err = ReadMessage(&buf)
	if err == nil || !IsFatal(err) || !errors.Is(err, io.EOF) {
		t.Errorf("at end: err = %v (fatal=%v), want fatal io.EOF", err, IsFatal(err))
	}
}

func FuzzReadMessage(f *testing.F) {
	f.Add([]byte{0x00, 0x0c, '{', '"', 't', '"', ':', '"', 'p', 'i', 'n', 'g', '"', '}'})
	f.Add([]byte{0xff, 0xff})
	f.Fuzz(func(t *testing.T, in []byte) {
		m, err := ReadMessage(bytes.NewReader(in))
		if err != nil {
			return
		}
		if err := Validate(m); err != nil {
			t.Errorf("ReadMessage returned invalid %+v: %v", m, err)
		}
		if m.Text != Sanitize(m.Text) {
			t.Errorf("ReadMessage returned unsanitized text %q", m.Text)
		}
	})
}
