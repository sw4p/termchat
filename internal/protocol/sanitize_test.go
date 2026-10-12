package protocol

import (
	"testing"
	"unicode"
	"unicode/utf8"
)

func TestSanitize(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "hello, world", "hello, world"},
		{"unicode", "café señor 日本語", "café señor 日本語"},
		{"emoji", "👋🦦", "👋🦦"},
		{"zwj emoji sequence", "👩‍💻", "👩‍💻"},
		{"clear screen", "\x1b[2Jhacked", "hacked"},
		{"cursor moves", "a\x1b[10;20Hb\x1b[3Ac", "abc"},
		{"private mode csi", "a\x1b[?1049hb", "ab"},
		{"sgr colors", "\x1b[1;31mred\x1b[0m", "red"},
		{"osc title with bel", "\x1b]0;evil title\x07hi", "hi"},
		{"osc title with st", "\x1b]0;evil title\x1b\\hi", "hi"},
		{"osc 52 clipboard", "\x1b]52;c;cm0gLXJmIH4=\x07hi", "hi"},
		{"osc 8 hyperlink", "\x1b]8;;http://evil\x1b\\click\x1b]8;;\x1b\\", "click"},
		{"dcs", "\x1bPq#0;2;0;0;0\x1b\\hi", "hi"},
		{"unterminated osc", "hi\x1b]0;rest of line", "hi"},
		{"charset select", "a\x1b(Bb", "ab"},
		{"full reset", "a\x1bcb", "ab"},
		{"lone esc at end", "hi\x1b", "hi"},
		{"8-bit csi", "a\u009b2Jb", "a2Jb"},
		{"bell and backspace", "a\x07\x08b", "ab"},
		{"carriage return overwrite", "safe\rEVIL", "safeEVIL"},
		{"newlines", "line1\nline2\r\n", "line1line2"},
		{"tab", "a\tb", "a b"},
		{"del", "a\x7fb", "ab"},
		{"nul", "a\x00b", "ab"},
		{"bidi override", "abc‮evil‬", "abcevil"},
		{"bidi isolate", "⁦x⁩", "x"},
		{"line separator", "a b", "ab"},
		{"invalid utf8", "a\xffb\xc3", "ab"},
		{"empty", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Sanitize(tc.in); got != tc.want {
				t.Errorf("Sanitize(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func FuzzSanitize(f *testing.F) {
	for _, s := range []string{"hello", "\x1b[2J", "\x1b]0;x\x07", "\u009b31m", "a\xffb", "\x1b"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		out := Sanitize(in)
		if !utf8.ValidString(out) {
			t.Fatalf("Sanitize(%q) = %q: invalid UTF-8", in, out)
		}
		for _, r := range out {
			if unicode.IsControl(r) || isInvisibleFormat(r) {
				t.Fatalf("Sanitize(%q) = %q: contains %U", in, out, r)
			}
		}
		if len(out) > len(in) {
			t.Fatalf("Sanitize(%q) grew to %q", in, out)
		}
		if again := Sanitize(out); again != out {
			t.Fatalf("Sanitize not idempotent: %q → %q → %q", in, out, again)
		}
	})
}
