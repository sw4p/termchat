package protocol

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Sanitize makes remote text safe to print on a terminal. It removes:
//   - ESC-based sequences, whole (CSI like "\x1b[2J", OSC like a title
//     change, and the rest), so none of their parameters leak through as text;
//   - all other C0 and C1 control characters, including newlines and DEL;
//     tabs become a single space;
//   - Unicode line/paragraph separators and bidi overrides, which can make
//     text display differently from what it says;
//   - invalid UTF-8 bytes.
//
// Everything else, including emoji and joined emoji sequences, is kept.
func Sanitize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1: // invalid UTF-8 byte
		case r == 0x1b:
			size = escapeLen(s[i:])
		case r == '\t':
			b.WriteByte(' ')
		case unicode.IsControl(r), isInvisibleFormat(r):
		default:
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	return b.String()
}

// escapeLen returns how many bytes of s, which starts with ESC, make up one
// escape sequence (following ECMA-48). An unterminated string sequence (OSC,
// DCS, …) runs to the end of s.
func escapeLen(s string) int {
	if len(s) < 2 {
		return len(s)
	}
	switch s[1] {
	case '[': // CSI: parameter and intermediate bytes, then one final byte
		j := 2
		for j < len(s) && s[j] >= 0x20 && s[j] <= 0x3f {
			j++
		}
		if j < len(s) && s[j] >= 0x40 && s[j] <= 0x7e {
			j++
		}
		return j
	case ']', 'P', 'X', '^', '_': // OSC, DCS, SOS, PM, APC: ends with BEL or ESC \
		for j := 2; j < len(s); j++ {
			if s[j] == 0x07 {
				return j + 1
			}
			if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
				return j + 2
			}
		}
		return len(s)
	default: // ESC, intermediates, one final byte, e.g. "\x1b(B" or "\x1bc"
		j := 1
		for j < len(s) && s[j] >= 0x20 && s[j] <= 0x2f {
			j++
		}
		if j < len(s) && s[j] >= 0x30 && s[j] <= 0x7e {
			j++
		}
		return j
	}
}

func isInvisibleFormat(r rune) bool {
	switch {
	case r == 0x061c, r == 0x200e, r == 0x200f: // bidi marks
	case r >= 0x202a && r <= 0x202e: // bidi embeddings and overrides
	case r >= 0x2066 && r <= 0x2069: // bidi isolates
	case r == 0x2028, r == 0x2029: // line and paragraph separators
	default:
		return false
	}
	return true
}
