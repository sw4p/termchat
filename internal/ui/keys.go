// Package ui is termchat's terminal front end: a raw-mode line editor that
// keeps the user's half-typed line intact while messages arrive, plus the
// slash-command parser.
//
// It knows nothing about networking or sessions. The rest of the program
// talks to it through Console: print a line, set the prompt, receive the
// lines the user submits.
//
// Most of the package is pure functions and plain structs (Decoder, Editor,
// Screen, ParseLine) so it can be tested without a real terminal.
package ui

import "unicode/utf8"

// KeyType is what kind of key was pressed.
type KeyType int

const (
	KeyRune      KeyType = iota // a printable character, in Key.Rune
	KeyEnter                    // Enter / Return
	KeyBackspace                // Backspace (DEL or ^H)
	KeyCtrlC                    // quit
	KeyCtrlD                    // quit on an empty line
	KeyCtrlU                    // clear the line
	KeyCtrlW                    // delete the previous word
	KeyOther                    // anything else: arrows, function keys, Alt+x…
)

// Key is one decoded key press.
type Key struct {
	Type KeyType
	Rune rune // only for KeyRune
}

// Decoder turns raw bytes from a terminal in raw mode into key presses.
//
// A single read can end in the middle of a multi-byte UTF-8 character, so the
// Decoder keeps those leftover bytes until the next Feed.
type Decoder struct {
	pending []byte
}

// Feed decodes p, plus anything left over from the last call.
func (d *Decoder) Feed(p []byte) []Key {
	data := append(d.pending, p...)
	d.pending = nil

	var keys []Key
	for i := 0; i < len(data); {
		b := data[i]
		switch {
		case b == 0x1b:
			keys = append(keys, Key{Type: KeyOther})
			i += inputEscapeLen(data[i:])
			continue
		case b == '\r' || b == '\n':
			keys = append(keys, Key{Type: KeyEnter})
		case b == 0x7f || b == 0x08:
			keys = append(keys, Key{Type: KeyBackspace})
		case b == 0x03:
			keys = append(keys, Key{Type: KeyCtrlC})
		case b == 0x04:
			keys = append(keys, Key{Type: KeyCtrlD})
		case b == 0x15:
			keys = append(keys, Key{Type: KeyCtrlU})
		case b == 0x17:
			keys = append(keys, Key{Type: KeyCtrlW})
		case b == '\t':
			keys = append(keys, Key{Type: KeyRune, Rune: '\t'})
		case b < 0x20:
			keys = append(keys, Key{Type: KeyOther})
		case b < utf8.RuneSelf:
			keys = append(keys, Key{Type: KeyRune, Rune: rune(b)})
		default:
			if !utf8.FullRune(data[i:]) {
				d.pending = append([]byte(nil), data[i:]...)
				return keys
			}
			r, size := utf8.DecodeRune(data[i:])
			if r != utf8.RuneError || size > 1 {
				keys = append(keys, Key{Type: KeyRune, Rune: r})
			}
			i += size
			continue
		}
		i++
	}
	return keys
}

// inputEscapeLen returns the length of the key sequence starting with ESC at
// data[0]: CSI ("\x1b[A", "\x1b[3~"), SS3 ("\x1bOA"), or Alt+key ("\x1bx").
// A lone ESC at the end of a read is the Escape key itself.
func inputEscapeLen(data []byte) int {
	if len(data) < 2 {
		return 1
	}
	switch data[1] {
	case '[':
		j := 2
		for j < len(data) && data[j] >= 0x20 && data[j] <= 0x3f {
			j++
		}
		if j < len(data) && data[j] >= 0x40 && data[j] <= 0x7e {
			j++
		}
		return j
	case 'O':
		return min(3, len(data))
	case 0x1b:
		return 1 // Escape pressed twice: take them one at a time
	default:
		return 2
	}
}
