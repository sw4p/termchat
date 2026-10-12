package protocol

import (
	"errors"
	"strings"
	"testing"
)

const testID = "0123456789abcdef0123456789abcdef"

func TestEncodeDecodeRoundTrip(t *testing.T) {
	tests := []Message{
		{Type: TypeHello, ID: testID, Nick: "quiet-otter"},
		{Type: TypeAccept, ID: testID, Nick: "café_42"},
		{Type: TypeBusy},
		{Type: TypeMsg, Text: "hello <world> & \"friends\" 👋"},
		{Type: TypeTyping},
		{Type: TypeLeave},
		{Type: TypePing},
	}
	for _, m := range tests {
		t.Run(string(m.Type), func(t *testing.T) {
			b, err := Encode(m)
			if err != nil {
				t.Fatalf("Encode(%+v): %v", m, err)
			}
			got, err := Decode(b)
			if err != nil {
				t.Fatalf("Decode(%s): %v", b, err)
			}
			if got != m {
				t.Errorf("round trip: got %+v, want %+v", got, m)
			}
		})
	}
}

func TestEncodeWireFormat(t *testing.T) {
	b, err := Encode(Message{Type: TypeMsg, Text: "a<b"})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"t":"msg","text":"a<b"}`; string(b) != want {
		t.Errorf("Encode = %s, want %s", b, want)
	}
}

func TestMessageRuneLimit(t *testing.T) {
	ok := strings.Repeat("🦦", MaxMessageRunes) // 640 bytes, 160 runes
	tooLong := strings.Repeat("a", MaxMessageRunes+1)

	if _, err := Encode(Message{Type: TypeMsg, Text: ok}); err != nil {
		t.Errorf("Encode of %d emoji: %v", MaxMessageRunes, err)
	}
	if _, err := Encode(Message{Type: TypeMsg, Text: tooLong}); !errors.Is(err, ErrTooLong) {
		t.Errorf("Encode of %d runes: err = %v, want ErrTooLong", MaxMessageRunes+1, err)
	}

	// A modified client could skip Encode's check, so Decode checks too.
	if _, err := Decode([]byte(`{"t":"msg","text":"` + ok + `"}`)); err != nil {
		t.Errorf("Decode of %d emoji: %v", MaxMessageRunes, err)
	}
	if _, err := Decode([]byte(`{"t":"msg","text":"` + tooLong + `"}`)); !errors.Is(err, ErrTooLong) {
		t.Errorf("Decode of %d runes: err = %v, want ErrTooLong", MaxMessageRunes+1, err)
	}
}

func TestWorstCaseMessageFitsFrame(t *testing.T) {
	const aeadOverhead = 16 // Poly1305 tag added in M3

	// Control characters are the most expensive to encode: "\u0001" is 6 bytes.
	for _, text := range []string{
		strings.Repeat("\x01", MaxMessageRunes),
		strings.Repeat("🦦", MaxMessageRunes),
		strings.Repeat(`"`, MaxMessageRunes),
	} {
		b, err := Encode(Message{Type: TypeMsg, Text: text})
		if err != nil {
			t.Fatalf("Encode: %v", err)
		}
		if len(b)+aeadOverhead > MaxFrameSize {
			t.Errorf("encoded %q… is %d bytes; +%d overhead exceeds MaxFrameSize %d",
				text[:4], len(b), aeadOverhead, MaxFrameSize)
		}
	}
}

func TestDecodeRejects(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want error // nil means "any error"
	}{
		{"not json", `hello`, nil},
		{"truncated json", `{"t":"msg","text":"hi`, nil},
		{"trailing garbage", `{"t":"ping"}x`, nil},
		{"wrong field type", `{"t":"msg","text":42}`, nil},
		{"empty", ``, nil},
		{"missing type", `{"text":"hi"}`, ErrUnknownType},
		{"unknown type", `{"t":"file","text":"hi"}`, ErrUnknownType},
		{"msg without text", `{"t":"msg"}`, ErrEmptyText},
		{"msg only escapes", `{"t":"msg","text":"\u001b[2J"}`, ErrEmptyText},
		{"hello without id", `{"t":"hello","nick":"quiet-otter"}`, ErrBadID},
		{"hello short id", `{"t":"hello","id":"abc","nick":"quiet-otter"}`, ErrBadID},
		{"hello uppercase id", `{"t":"hello","id":"0123456789ABCDEF0123456789ABCDEF","nick":"x"}`, ErrBadID},
		{"hello without nick", `{"t":"hello","id":"` + testID + `"}`, ErrBadNick},
		{"accept escape in nick", `{"t":"accept","id":"` + testID + `","nick":"\u001b[31mred"}`, ErrBadNick},
		{"hello long nick", `{"t":"hello","id":"` + testID + `","nick":"` + strings.Repeat("a", 25) + `"}`, ErrBadNick},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, err := Decode([]byte(tc.in))
			if err == nil {
				t.Fatalf("Decode(%q) = %+v, want error", tc.in, m)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Errorf("Decode(%q) err = %v, want %v", tc.in, err, tc.want)
			}
		})
	}
}

func TestDecodeIgnoresUnknownFields(t *testing.T) {
	m, err := Decode([]byte(`{"t":"msg","text":"hi","color":"red"}`))
	if err != nil {
		t.Fatal(err)
	}
	if m != (Message{Type: TypeMsg, Text: "hi"}) {
		t.Errorf("got %+v", m)
	}
}

func TestDecodeSanitizesText(t *testing.T) {
	m, err := Decode([]byte(`{"t":"msg","text":"\u001b]0;pwned\u0007hi\u001b[2J there"}`))
	if err != nil {
		t.Fatal(err)
	}
	if m.Text != "hi there" {
		t.Errorf("Text = %q, want %q", m.Text, "hi there")
	}
}

func TestEncodeRejectsInvalid(t *testing.T) {
	for _, m := range []Message{
		{},
		{Type: "nope"},
		{Type: TypeMsg},
		{Type: TypeHello, ID: testID, Nick: "bad nick"},
	} {
		if _, err := Encode(m); err == nil {
			t.Errorf("Encode(%+v) succeeded, want error", m)
		}
	}
}
