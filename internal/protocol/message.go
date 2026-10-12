// Package protocol defines the messages termchat peers exchange and how they
// are turned into bytes: JSON payloads inside length-prefixed frames.
//
// It knows nothing about sockets, encryption or terminals. It reads from an
// io.Reader and writes to an io.Writer, so the same code works over a TCP
// connection, an encrypted wrapper (M3) or a bytes.Buffer in tests.
package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/sw4p/termchat/internal/nick"
)

// MaxMessageRunes is the longest chat message, in Unicode code points.
const MaxMessageRunes = 160

// IDLen is the length of a peer id: 128 random bits as lowercase hex.
const IDLen = 32

// Version is the protocol major version. It goes in the handshake and in the
// mDNS TXT record, and peers with different majors never talk.
const Version = 1

// Type says what kind of message this is. It is the "t" field on the wire.
type Type string

const (
	TypeHello  Type = "hello"  // dialer → listener: "let's chat", with id and nick
	TypeAccept Type = "accept" // listener → dialer: "ok", with id and nick
	TypeBusy   Type = "busy"   // listener → dialer: "not now"
	TypeMsg    Type = "msg"    // a chat line
	TypeTyping Type = "typing" // the partner is typing
	TypeLeave  Type = "leave"  // the partner is ending the chat
	TypePing   Type = "ping"   // keepalive
)

// Message is one protocol message. Fields that don't apply to a Type are left
// empty and omitted from the JSON.
type Message struct {
	Type Type   `json:"t"`
	ID   string `json:"id,omitempty"`
	Nick string `json:"nick,omitempty"`
	Text string `json:"text,omitempty"`
}

var (
	ErrUnknownType = errors.New("protocol: unknown message type")
	ErrTooLong     = errors.New("protocol: message too long")
	ErrEmptyText   = errors.New("protocol: empty message")
	ErrBadID       = errors.New("protocol: invalid peer id")
	ErrBadNick     = errors.New("protocol: invalid nick")
)

// Validate checks that m is well formed for its Type.
//
// Unknown types return ErrUnknownType. Peers with the same major version may
// add new types, so callers should ignore such messages rather than hang up.
func Validate(m Message) error {
	switch m.Type {
	case TypeHello, TypeAccept:
		if !ValidID(m.ID) {
			return ErrBadID
		}
		if !nick.Valid(m.Nick) {
			return ErrBadNick
		}
	case TypeMsg:
		n := utf8.RuneCountInString(m.Text)
		if n == 0 {
			return ErrEmptyText
		}
		if n > MaxMessageRunes {
			return ErrTooLong
		}
	case TypeBusy, TypeTyping, TypeLeave, TypePing:
	default:
		return ErrUnknownType
	}
	return nil
}

// ValidID reports whether s is a well-formed peer id.
func ValidID(s string) bool {
	if len(s) != IDLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f') {
			return false
		}
	}
	return true
}

// Encode validates m and returns its JSON form, ready to be framed.
func Encode(m Message) ([]byte, error) {
	if err := Validate(m); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// Don't turn <, > and & into < etc.: this isn't HTML, and escaping
	// would triple the size of a message full of them.
	enc.SetEscapeHTML(false)
	if err := enc.Encode(m); err != nil {
		return nil, err
	}
	b := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
	if len(b) > MaxFrameSize {
		return nil, ErrFrameTooLarge
	}
	return b, nil
}

// Decode parses a JSON payload from a peer. The Text of the result has been
// passed through Sanitize, so it is safe to print.
//
// Any error means "drop this message". None of them corrupt the stream.
func Decode(b []byte) (Message, error) {
	var m Message
	if err := json.Unmarshal(b, &m); err != nil {
		return Message{}, fmt.Errorf("protocol: bad JSON: %w", err)
	}
	// Sanitize before validating, so a message made only of escape sequences
	// counts as empty. Nicks aren't sanitized: Validate rejects any nick
	// with control characters in it.
	m.Text = Sanitize(m.Text)
	if err := Validate(m); err != nil {
		return Message{}, err
	}
	return m, nil
}
