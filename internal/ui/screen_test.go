package ui

import (
	"bytes"
	"strings"
	"testing"
)

func newTestScreen(width, limit int) (*Screen, *bytes.Buffer) {
	var buf bytes.Buffer
	return NewScreen(&buf, false, func() int { return width }, limit), &buf
}

func TestScreenPrintlnRedrawsPrompt(t *testing.T) {
	s, buf := newTestScreen(80, 160)
	s.SetPrompt("[chatting with bob] > ")
	s.SetInput("half-typed")
	buf.Reset()

	s.Println("bob: hi")
	want := "\r\x1b[K" + "bob: hi" + "\r\n" + "\r\x1b[K" + "[chatting with bob] > half-typed"
	if got := buf.String(); got != want {
		t.Errorf("Println wrote %q\nwant %q", got, want)
	}
}

func TestScreenSetInputRedraws(t *testing.T) {
	s, buf := newTestScreen(80, 160)
	s.SetPrompt("> ")
	buf.Reset()
	s.SetInput("abc")
	if got, want := buf.String(), "\r\x1b[K> abc"; got != want {
		t.Errorf("SetInput wrote %q, want %q", got, want)
	}
}

func TestScreenCounter(t *testing.T) {
	s, _ := newTestScreen(400, 160)
	s.SetPrompt("> ")
	for _, tc := range []struct {
		n    int
		want string
	}{
		{0, "> "},
		{139, "> " + strings.Repeat("x", 139)},
		{140, "> (140/160) " + strings.Repeat("x", 140)},
		{160, "> (160/160) " + strings.Repeat("x", 160)},
	} {
		s.SetInput(strings.Repeat("x", tc.n))
		if got := s.visibleText(); got != tc.want {
			t.Errorf("%d runes: prompt line = %q, want %q", tc.n, got, tc.want)
		}
	}
}

func TestScreenScrollsLongInput(t *testing.T) {
	s, _ := newTestScreen(20, 160)
	s.SetPrompt("> ") // 2 columns; 20 - 2 - 1 = 17 columns for input
	s.SetInput("abcdefghijklmnopqrstuvwxyz")
	if got, want := s.visibleText(), "> …klmnopqrstuvwxyz"; got != want {
		t.Errorf("prompt line = %q, want %q", got, want)
	}
	if w := stringWidth(s.visibleText()); w >= 20 {
		t.Errorf("prompt line is %d columns wide on a 20-column terminal", w)
	}
}

func TestScreenScrollsWideRunes(t *testing.T) {
	s, _ := newTestScreen(12, 160)
	s.SetPrompt("> ")     // 12 - 2 - 1 = 9 columns
	s.SetInput("日本語テキスト") // 7 runes, 14 columns
	got := s.visibleText()
	if w := stringWidth(got); w > 11 {
		t.Errorf("prompt line %q is %d columns, want at most 11", got, w)
	}
	if !strings.HasSuffix(got, "テキスト") || !strings.Contains(got, "…") {
		t.Errorf("prompt line = %q, want the tail after an ellipsis", got)
	}
}

func TestScreenPlain(t *testing.T) {
	var buf bytes.Buffer
	s := NewScreen(&buf, true, func() int { return 0 }, 160)
	s.SetPrompt("> ")
	s.SetInput("ignored")
	s.Println("bob: hi")
	s.Close()
	if got := buf.String(); got != "bob: hi\n" {
		t.Errorf("plain screen wrote %q", got)
	}
}

func TestScreenClose(t *testing.T) {
	s, buf := newTestScreen(80, 160)
	s.SetPrompt("> ")
	buf.Reset()
	s.Close()
	s.Println("too late")
	if got := buf.String(); got != "\r\x1b[K" {
		t.Errorf("after Close wrote %q", got)
	}
}

func TestRuneWidth(t *testing.T) {
	for _, tc := range []struct {
		r    rune
		want int
	}{
		{'a', 1}, {'é', 1}, {'日', 2}, {'한', 2}, {'🦦', 2}, {'😀', 2},
		{'́', 0}, {'‍', 0}, {'️', 0}, {'Ａ', 2},
	} {
		if got := runeWidth(tc.r); got != tc.want {
			t.Errorf("runeWidth(%q) = %d, want %d", tc.r, got, tc.want)
		}
	}
}
