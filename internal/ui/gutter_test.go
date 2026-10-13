package ui

import (
	"strings"
	"testing"
)

func TestWrap(t *testing.T) {
	for _, tc := range []struct {
		in    string
		width int
		want  []string
	}{
		{"", 10, []string{""}},
		{"short", 10, []string{"short"}},
		{"hello big world", 9, []string{"hello big", "world"}},
		{"abcdefghij", 4, []string{"abcd", "efgh", "ij"}},
		{"日本語です", 4, []string{"日本", "語で", "す"}},
	} {
		got := wrap(tc.in, tc.width)
		if strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Errorf("wrap(%q, %d) = %q, want %q", tc.in, tc.width, got, tc.want)
		}
	}
}

func TestClipKeepsNameEnd(t *testing.T) {
	if got := clip("a-very-long-nickname-indeed", 10); got != "…e-indeed" && stringWidth(got) > 10 {
		t.Errorf("clip = %q", got)
	}
	if got := clip("nimble-newt", 14); got != "nimble-newt" {
		t.Errorf("clip changed a short name: %q", got)
	}
}

func TestScreenMessageGutter(t *testing.T) {
	s, buf := newTestScreen(60, 160)
	s.SetPrompt("> ")
	buf.Reset()
	s.Message("nimble-newt", false, "hi")
	want := "\r\x1b[K" + "   nimble-newt │ hi" + "\r\n" + "\r\x1b[K> "
	if got := buf.String(); got != want {
		t.Errorf("Message wrote %q\nwant %q", got, want)
	}
}

func TestScreenMessageWrapsUnderText(t *testing.T) {
	s, buf := newTestScreen(40, 160) // 40-17-1 = 22 text columns
	s.Message("bob", false, "one two three four five six seven")
	rows := strings.Split(buf.String(), "\r\n")
	want := "\r\x1b[K" + strings.Repeat(" ", gutterWidth) + gutterSep + "five six seven"
	if len(rows) < 2 || rows[1] != want {
		t.Errorf("continuation row = %q, want %q", rows[1:], want)
	}
}

func TestScreenMessageNarrowAndPlain(t *testing.T) {
	s, buf := newTestScreen(20, 160)
	s.Message("bob", false, "hi")
	if got := buf.String(); !strings.HasPrefix(got, "\r\x1b[Kbob: hi\r\n") {
		t.Errorf("narrow terminal should skip the gutter: %q", got)
	}
	var pb strings.Builder
	p := NewScreen(&pb, true, func() int { return 0 }, 0)
	p.Message("bob", false, "hi")
	p.Println("* note")
	if pb.String() != "bob: hi\n* note\n" {
		t.Errorf("plain output = %q", pb.String())
	}
}

func TestScreenNoticeAndColor(t *testing.T) {
	s, buf := newTestScreen(60, 160)
	s.SetColor(true)
	s.Println("! careful")
	got := buf.String()
	if !strings.Contains(got, styleWarn+"!"+styleReset) || !strings.Contains(got, "careful") {
		t.Errorf("warning not styled: %q", got)
	}
}

func TestScreenStatusRuleAndGutterPrompt(t *testing.T) {
	s, buf := newTestScreen(40, 160)
	s.SetPrompt("[x] > ")
	buf.Reset()

	s.SetStatus("to bob", "")
	rule := strings.Repeat("─", 39)
	want := "\r\x1b[K\x1b[K" + rule + "\r\n" + "\r\x1b[K" + "           you ›    to bob\x1b[9D"
	if got := buf.String(); got != want {
		t.Errorf("first SetStatus wrote %q\nwant %q", got, want)
	}

	// Later output replaces the rule: up a row, print, then rule and prompt.
	buf.Reset()
	s.Message("bob", false, "hi")
	if got := buf.String(); !strings.HasPrefix(got, "\r\x1b[1A\r\x1b[K") || !strings.Contains(got, rule) || !strings.Contains(got, "you ›    to bob") {
		t.Errorf("Message with a rule wrote %q", got)
	}

	// A note rides on the right end of the rule, rewritten in place.
	buf.Reset()
	s.SetStatus("to bob", "bob is typing…")
	if got := buf.String(); !strings.HasPrefix(got, "\r\x1b[1A") || !strings.Contains(got, "─ bob is typing… ──") {
		t.Errorf("note change wrote %q", got)
	}

	// Typing hides the hint.
	s.SetInput("yo")
	if got := s.visibleText(); got != "           you › yo" {
		t.Errorf("prompt with input = %q", got)
	}

	// Close clears the prompt row and the rule.
	buf.Reset()
	s.Close()
	if got, want := buf.String(), "\r\x1b[K\x1b[1A\r\x1b[K"; got != want {
		t.Errorf("Close wrote %q, want %q", got, want)
	}
}

func TestScreenStatusNarrowKeepsPlainPrompt(t *testing.T) {
	s, buf := newTestScreen(20, 160)
	s.SetPrompt("[idle] > ")
	buf.Reset()
	s.SetStatus("to bob", "")
	if buf.Len() != 0 || s.visibleText() != "[idle] > " {
		t.Errorf("narrow screen drew %q, prompt %q", buf.String(), s.visibleText())
	}
}

func TestScreenWarningRows(t *testing.T) {
	s, buf := newTestScreen(60, 160)
	s.Println("! strangers may send links.\ntermchat never asks you to run anything.")
	want := "\r\x1b[K" + strings.Repeat(" ", 13) + "! │ strangers may send links.\r\n" +
		"\r\x1b[K" + strings.Repeat(" ", 14) + " │ termchat never asks you to run anything.\r\n"
	if got := buf.String(); !strings.HasPrefix(got, want) {
		t.Errorf("warning wrote %q\nwant prefix %q", got, want)
	}
}

func TestScreenBannerAndHello(t *testing.T) {
	s, buf := newTestScreen(40, 160)
	s.Banner("new chat with", "bob")
	got := buf.String()
	if !strings.Contains(got, " new chat with bob ") || strings.Count(got, "─") != 39-len(" new chat with bob ") {
		t.Errorf("banner = %q", got)
	}
	buf.Reset()
	s.Hello("nimble", "hi, nimble. hello")
	if got := buf.String(); !strings.Contains(got, "      termchat │ hi, nimble. hello") {
		t.Errorf("hello = %q", got)
	}
	buf.Reset()
	s.SetColor(true)
	s.Hello("nimble", "hi, nimble. hello")
	if got := buf.String(); !strings.Contains(got, styleBold+"nimble"+styleReset) {
		t.Errorf("nick not bold: %q", got)
	}
}
