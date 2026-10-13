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

	// status, when set and the terminal is wide enough, switches the prompt
	// to the gutter form: a rule carrying the status, then "you › ".
	status    string
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
			s.write(gutterLines("·", styleDim, styleDim, styleDim, text[2:], cols, s.color))
			return
		case strings.HasPrefix(text, "! "):
			s.write(gutterLines("!", styleWarn+styleBold, styleDim, styleWarn, text[2:], cols, s.color))
			return
		}
	}
	s.write([]string{text})
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
	s.write(gutterLines(name, style, styleDim, "", text, cols, s.color))
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

// SetStatus sets the text shown in the rule above the prompt, like
// "chatting with quiet-otter". On a terminal too narrow for the gutter it
// does nothing and the plain prompt stays.
func (s *Screen) SetStatus(status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = status
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
	return s.status != "" && s.width()-gutterLeft-1 >= minTextCols
}

// rule is the dim line above the prompt, with the status set into it.
func (s *Screen) rule() string {
	cols := s.width() - 1 // keep the last column free
	label := "─── " + s.status + " "
	fill := cols - stringWidth(label)
	if fill < 0 {
		label, fill = clip(label, cols), 0
	}
	line := label + strings.Repeat("─", fill)
	if s.color {
		return "\x1b[K" + paint(styleDim, line)
	}
	return "\x1b[K" + line
}

// head returns the prompt as drawn and the same without escape codes, for
// measuring.
func (s *Screen) head() (styled, plain string) {
	if !s.composer() {
		return s.prompt, s.prompt
	}
	plain = strings.Repeat(" ", gutterWidth-3) + "you › "
	if !s.color {
		return plain, plain
	}
	pad := strings.Repeat(" ", gutterWidth-3)
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
	return "\r\x1b[K" + head + string(shown)
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
