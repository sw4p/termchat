package ui

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"unicode"
)

// counterMargin is how close to the limit the input has to get before the
// prompt shows a (152/160) counter.
const counterMargin = 20

// Screen draws output lines above a prompt line holding the user's input.
//
// Every output line is written as: go to column 0, clear the line (erasing the
// prompt), print the text, newline, then redraw the prompt and input. So an
// incoming message never lands in the middle of what the user is typing.
//
// In plain mode (stdout isn't a terminal) there's no prompt line: output is
// just written line by line.
type Screen struct {
	mu     sync.Mutex
	out    io.Writer
	plain  bool
	width  func() int
	limit  int
	prompt string
	input  []rune
	closed bool
	color  bool // style names and notices with ANSI colours

	// Once SetStatus has been called and the terminal is wide enough, the
	// prompt takes the gutter form: a rule, then "you › " with hint dimmed
	// after the cursor while the input is empty. note rides on the rule.
	statusSet bool
	hint      string
	note      string
	ruleShown bool // a rule row is on screen directly above the prompt row
}

// NewScreen returns a Screen writing to out. width reports the terminal width
// in columns. limit is the input's maximum length, for the counter.
func NewScreen(out io.Writer, plain bool, width func() int, limit int) *Screen {
	return &Screen{out: out, plain: plain, width: width, limit: limit}
}

// SetColor turns ANSI colours on or off for names and notices.
func (s *Screen) SetColor(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.color = on
}

// Println prints text above the prompt. text is a single line. Lines that
// start with "* " (notices) or "! " (warnings) go in the gutter's name
// column as "·" and "!"; anything else is printed as is.
func (s *Screen) Println(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if s.plain {
		fmt.Fprintln(s.out, text)
		return
	}
	if cols := s.width(); cols-gutterLeft-1 >= minTextCols {
		switch {
		case strings.HasPrefix(text, "* "):
			s.write(gutterLines("·", styleDim, styleDim, styleDim+styleItal, text[2:], "", cols, s.color))
			return
		case strings.HasPrefix(text, "! "):
			s.write(gutterLines("!", styleWarn, styleDim, styleWarn+styleItal, text[2:], "", cols, s.color))
			return
		}
	}
	s.write([]string{strings.ReplaceAll(text, "\n", " ")})
}

// Message prints a chat line: name, then text, in the gutter. self marks our
// own messages, which get a different colour from the partner's. Plain mode
// prints "name: text".
func (s *Screen) Message(name string, self bool, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if s.plain {
		fmt.Fprintln(s.out, name+": "+text)
		return
	}
	cols := s.width()
	if cols-gutterLeft-1 < minTextCols { // too narrow for a gutter
		s.write([]string{name + ": " + text})
		return
	}
	style := stylePeer + styleBold
	if self {
		style = styleYou + styleBold
	}
	s.write(gutterLines(name, style, styleDim, "", text, "", cols, s.color))
}

// Hello prints the greeting: a dim "termchat" in the name column, then text,
// with nick (which should occur in text) in bold.
func (s *Screen) Hello(nick, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if s.plain || s.width()-gutterLeft-1 < minTextCols {
		s.printPlainLocked(text)
		return
	}
	s.write(gutterLines("termchat", styleDim, styleDim, styleDim, text, nick, s.width(), s.color))
}

// Banner prints a rule with "text name" set in the middle, name in bold and
// the label in the partner's colour.
func (s *Screen) Banner(text, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	label := text + " " + name
	total := s.width() - 1
	if s.plain || total < stringWidth(label)+8 {
		s.printPlainLocked("─── " + label + " ───")
		return
	}
	left := (total - stringWidth(label) - 2) / 2
	right := total - stringWidth(label) - 2 - left
	row := strings.Repeat("─", left) + " " + label + " " + strings.Repeat("─", right)
	if s.color {
		row = paint(styleRule, strings.Repeat("─", left)) + " " +
			paint(stylePeer, text+" ") + paint(stylePeer+styleBold, name) + " " +
			paint(styleRule, strings.Repeat("─", right))
	}
	s.write([]string{row})
}

// printPlainLocked prints one unstyled line the way Println does. s.mu must
// be held.
func (s *Screen) printPlainLocked(text string) {
	if s.plain {
		fmt.Fprintln(s.out, text)
		return
	}
	s.write([]string{text})
}

// write prints each row above the prompt, then redraws the prompt. s.mu must
// be held.
func (s *Screen) write(rows []string) {
	// Raw mode has no automatic carriage return, so "\n" alone would only
	// move down a line.
	var b strings.Builder
	if s.ruleShown {
		b.WriteString("\r\x1b[1A") // up onto the rule, which the first row replaces
	}
	for _, r := range rows {
		b.WriteString("\r\x1b[K" + r + "\r\n")
	}
	if s.ruleShown {
		b.WriteString(s.rule() + "\r\n")
	}
	b.WriteString(s.render())
	io.WriteString(s.out, b.String())
}

// SetStatus sets the gutter-form prompt's extras: hint is dim placeholder text
// after the cursor while the input is empty, and note is dim text at the
// right end of the rule above the prompt. On a terminal too narrow for the
// gutter it does nothing and the plain prompt stays.
func (s *Screen) SetStatus(hint, note string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statusSet, s.hint, s.note = true, hint, note
	if s.plain || s.closed || !s.composer() {
		return
	}
	if s.ruleShown {
		io.WriteString(s.out, "\r\x1b[1A"+s.rule()+"\r\n"+s.render())
		return
	}
	io.WriteString(s.out, "\r\x1b[K"+s.rule()+"\r\n"+s.render())
	s.ruleShown = true
}

// composer reports whether the prompt is drawn in the gutter form.
func (s *Screen) composer() bool {
	return s.statusSet && s.width()-gutterLeft-1 >= minTextCols
}

// rule is the dim line above the prompt, with the note at its right end.
func (s *Screen) rule() string {
	cols := s.width() - 1 // keep the last column free
	line := strings.Repeat("─", cols)
	if s.color {
		line = paint(styleRule, line)
	}
	if s.note != "" {
		tail := " " + s.note + " "
		if w := stringWidth(tail) + 2; w < cols-4 {
			dashes := strings.Repeat("─", cols-w)
			line = dashes + tail + "──"
			if s.color {
				line = paint(styleRule, dashes) + paint(styleDim, tail) + paint(styleRule, "──")
			}
		}
	}
	return "\x1b[K" + line
}

// head returns the prompt as drawn and the same without escape codes, for
// measuring.
func (s *Screen) head() (styled, plain string) {
	if !s.composer() {
		return s.prompt, s.prompt
	}
	pad := strings.Repeat(" ", gutterWidth-3)
	plain = pad + "you › "
	if !s.color {
		return plain, plain
	}
	return pad + paint(styleYou+styleBold, "you") + " " + paint(styleYou, "›") + " ", plain
}

// Bell rings the terminal bell (BEL, "\a"). Plain mode stays silent.
func (s *Screen) Bell() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.plain && !s.closed {
		io.WriteString(s.out, "\a")
	}
}

// SetPrompt changes the prompt, like "[chatting with quiet-otter] > ".
func (s *Screen) SetPrompt(p string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prompt = p
	s.redraw()
}

// SetInput changes the input line shown after the prompt.
func (s *Screen) SetInput(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.input = []rune(line)
	s.redraw()
}

// Close erases the prompt line and stops drawing, so the shell prompt that
// comes next starts on a clean line.
func (s *Screen) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.plain && !s.closed {
		io.WriteString(s.out, "\r\x1b[K")
		if s.ruleShown {
			io.WriteString(s.out, "\x1b[1A\r\x1b[K") // and the rule above it
		}
	}
	s.closed = true
}

func (s *Screen) redraw() {
	if !s.plain && !s.closed {
		io.WriteString(s.out, s.render())
	}
}

// render returns the bytes that redraw the prompt line. It must fit on one
// terminal row: "\r\x1b[K" only clears the row the cursor is on, so a line
// that wrapped would leave its first half behind on every redraw. When the
// input is too wide, only its tail is shown, after an ellipsis.
func (s *Screen) render() string {
	head, plain := s.head()
	if n := len(s.input); s.limit > 0 && n >= s.limit-counterMargin {
		counter := fmt.Sprintf("(%d/%d) ", n, s.limit)
		head, plain = head+counter, plain+counter
	}
	avail := s.width() - stringWidth(plain) - 1 // keep the last column free
	shown := s.input
	if avail < 1 {
		shown = nil
	} else if runesWidth(shown) > avail {
		w, i := 1, len(shown) // 1 column for the ellipsis
		for i > 0 && w+runeWidth(shown[i-1]) <= avail {
			i--
			w += runeWidth(shown[i])
		}
		shown = append([]rune{'…'}, shown[i:]...)
	}
	// The hint sits two spaces past the cursor cell, as in the design.
	if len(shown) == 0 && s.composer() && s.hint != "" && avail > 4 {
		hint := "   " + clipEnd(s.hint, avail-3)
		h := hint
		if s.color {
			h = "   " + paint(styleDim, hint[3:])
		}
		// Park the cursor back at the start of the (empty) input.
		return fmt.Sprintf("\r\x1b[K%s%s\x1b[%dD", head, h, stringWidth(hint))
	}
	return "\r\x1b[K" + head + string(shown)
}

// clipEnd shortens s to at most w columns, ending with "…" if it was cut.
func clipEnd(s string, w int) string {
	if stringWidth(s) <= w {
		return s
	}
	rs := []rune(s)
	cut, i := 1, 0
	for i < len(rs) && cut+runeWidth(rs[i]) <= w {
		cut += runeWidth(rs[i])
		i++
	}
	return string(rs[:i]) + "…"
}

func stringWidth(s string) int { return runesWidth([]rune(s)) }

func runesWidth(rs []rune) int {
	w := 0
	for _, r := range rs {
		w += runeWidth(r)
	}
	return w
}

// runeWidth returns how many terminal columns r takes: 0 for combining marks
// and joiners, 2 for East Asian wide characters and most emoji, 1 otherwise.
// It's an approximation (terminals disagree on some emoji), which is why
// render leaves the last column free.
func runeWidth(r rune) int {
	switch {
	case isModifier(r) && !(r >= 0x1f3fb && r <= 0x1f3ff), unicode.Is(unicode.Cf, r):
		return 0
	case r >= 0x1100 && r <= 0x115f, // Hangul Jamo
		r >= 0x2e80 && r <= 0xa4cf && r != 0x303f,              // CJK, Kana, Yi
		r >= 0xac00 && r <= 0xd7a3,                             // Hangul syllables
		r >= 0xf900 && r <= 0xfaff,                             // CJK compatibility
		r >= 0xfe30 && r <= 0xfe4f,                             // CJK compatibility forms
		r >= 0xff00 && r <= 0xff60, r >= 0xffe0 && r <= 0xffe6, // fullwidth forms
		r >= 0x1f300 && r <= 0x1f64f, // symbols, emoticons
		r >= 0x1f900 && r <= 0x1faff, // more emoji
		r >= 0x1f680 && r <= 0x1f6ff, // transport and map
		r >= 0x20000 && r <= 0x3fffd: // CJK extensions
		return 2
	}
	return 1
}

// visibleText is the prompt line as the user sees it, without escape codes.
// It's for tests.
func (s *Screen) visibleText() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.TrimPrefix(s.render(), "\r\x1b[K")
}
