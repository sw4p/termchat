package ui

import (
	"slices"
	"testing"
)

func r(c rune) Key { return Key{Type: KeyRune, Rune: c} }

var (
	enter = Key{Type: KeyEnter}
	bs    = Key{Type: KeyBackspace}
	other = Key{Type: KeyOther}
)

func TestDecoderFeed(t *testing.T) {
	tests := []struct {
		name string
		in   []string // successive reads
		want []Key
	}{
		{"ascii", []string{"hi"}, []Key{r('h'), r('i')}},
		{"enter as CR", []string{"a\r"}, []Key{r('a'), enter}},
		{"enter as LF", []string{"a\n"}, []Key{r('a'), enter}},
		{"backspace DEL and ^H", []string{"\x7f\x08"}, []Key{bs, bs}},
		{"control keys", []string{"\x03\x04\x15\x17"}, []Key{
			{Type: KeyCtrlC}, {Type: KeyCtrlD}, {Type: KeyCtrlU}, {Type: KeyCtrlW},
		}},
		{"other control", []string{"\x01\x1a"}, []Key{other, other}},
		{"tab", []string{"\t"}, []Key{r('\t')}},
		{"multibyte in one read", []string{"é🦦"}, []Key{r('é'), r('🦦')}},
		{"multibyte split across reads", []string{"\xf0\x9f", "\xa6\xa6x"}, []Key{r('🦦'), r('x')}},
		{"split three ways", []string{"\xc3", "", "\xa9"}, []Key{r('é')}},
		{"arrow keys", []string{"\x1b[A\x1b[D"}, []Key{other, other}},
		{"delete key", []string{"a\x1b[3~b"}, []Key{r('a'), other, r('b')}},
		{"ss3 arrows", []string{"\x1bOAz"}, []Key{other, r('z')}},
		{"alt+x", []string{"\x1bxy"}, []Key{other, r('y')}},
		{"lone escape", []string{"\x1b"}, []Key{other}},
		{"invalid utf8 skipped", []string{"a\xffb"}, []Key{r('a'), r('b')}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var d Decoder
			var got []Key
			for _, chunk := range tc.in {
				got = append(got, d.Feed([]byte(chunk))...)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("keys = %v, want %v", got, tc.want)
			}
		})
	}
}
