package ui

import (
	"strings"
	"unicode"
)

// EventKind is what a key press did to the editor.
type EventKind int

const (
	EventNone    EventKind = iota // nothing changed (ignored or refused key)
	EventChanged                  // the line changed; redraw it
	EventSubmit                   // Enter: the line is in the event's text, and the editor is now empty
	EventQuit                     // Ctrl-C, or Ctrl-D on an empty line
)

const zwj = '‍' // zero-width joiner, which glues emoji like 👩‍💻 together

// Editor is the line being typed. The cursor is always at the end, which is
// all a chat line needs.
//
// Handle is a pure state transition: key in, event out. There's no I/O here,
// so every editing rule is covered by plain table tests.
type Editor struct {
	buf []rune
	max int
}

// NewEditor returns an empty editor that refuses input past max runes.
func NewEditor(max int) *Editor {
	return &Editor{max: max}
}

// String returns the current line.
func (e *Editor) String() string { return string(e.buf) }

// Len returns the current line's length in runes.
func (e *Editor) Len() int { return len(e.buf) }

// Handle applies one key press. For EventSubmit, text is the submitted line
// with surrounding spaces trimmed.
func (e *Editor) Handle(k Key) (ev EventKind, text string) {
	switch k.Type {
	case KeyRune:
		r := k.Rune
		if r == '\t' {
			r = ' '
		}
		if !insertable(r) || len(e.buf) >= e.max {
			return EventNone, ""
		}
		e.buf = append(e.buf, r)
		return EventChanged, ""
	case KeyEnter:
		line := strings.TrimSpace(string(e.buf))
		e.buf = e.buf[:0]
		return EventSubmit, line
	case KeyBackspace:
		if len(e.buf) == 0 {
			return EventNone, ""
		}
		e.deleteLastChar()
		return EventChanged, ""
	case KeyCtrlU:
		if len(e.buf) == 0 {
			return EventNone, ""
		}
		e.buf = e.buf[:0]
		return EventChanged, ""
	case KeyCtrlW:
		if len(e.buf) == 0 {
			return EventNone, ""
		}
		e.deleteLastWord()
		return EventChanged, ""
	case KeyCtrlC:
		return EventQuit, ""
	case KeyCtrlD:
		if len(e.buf) == 0 {
			return EventQuit, ""
		}
	}
	return EventNone, ""
}

// insertable reports whether r may be typed: printable characters, plus the
// zero-width joiner that multi-part emoji need. Control characters, bidi
// overrides and the like are refused, so our own screen stays clean too.
func insertable(r rune) bool {
	return unicode.IsPrint(r) || r == zwj
}

// deleteLastChar removes the last character the user sees, which can be
// several runes: "é" typed as e + a combining accent, or an emoji like 👩‍💻
// (woman, joiner, laptop) or 👍🏽 (thumbs up, skin tone).
func (e *Editor) deleteLastChar() {
	for len(e.buf) > 0 {
		r := e.buf[len(e.buf)-1]
		e.buf = e.buf[:len(e.buf)-1]
		if isModifier(r) {
			continue // a modifier on its own isn't a character; take its base too
		}
		if len(e.buf) > 0 && e.buf[len(e.buf)-1] == zwj {
			e.buf = e.buf[:len(e.buf)-1]
			continue // joined to the previous emoji; take that too
		}
		return
	}
}

func isModifier(r rune) bool {
	return r == zwj ||
		(r >= 0xfe00 && r <= 0xfe0f) || // variation selectors
		(r >= 0x1f3fb && r <= 0x1f3ff) || // skin tones
		unicode.In(r, unicode.Mn, unicode.Me) // combining marks
}

// deleteLastWord implements Ctrl-W: drop trailing spaces, then everything
// back to the previous space.
func (e *Editor) deleteLastWord() {
	i := len(e.buf)
	for i > 0 && e.buf[i-1] == ' ' {
		i--
	}
	for i > 0 && e.buf[i-1] != ' ' {
		i--
	}
	e.buf = e.buf[:i]
}
