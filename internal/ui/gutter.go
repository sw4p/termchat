package ui

import "strings"

// The name gutter: each chat line is laid out as
//
//	nimble-newt │ hey! you on the 3rd floor too?
//	        you │ yep, by the printers
//
// Names are right-aligned in a fixed-width column so the messages line up,
// and long messages wrap under the text column instead of under the names.
const (
	gutterWidth = 14 // columns for the name, right-aligned
	gutterSep   = " │ "
	gutterLeft  = gutterWidth + 3 // columns before the text starts
	minTextCols = 20              // narrower than this, skip the gutter
)

// ANSI styles. Only used when colour is on. The colours are the design's
// (peer blue #5fb3e6, you orange #e8a45c, warning #e5c07b), as the nearest
// 256-colour entries, or exact RGB where the terminal supports it.
var (
	styleReset = "\x1b[0m"
	styleBold  = "\x1b[1m"
	styleDim   = "\x1b[2m"
	styleItal  = "\x1b[3m"
	stylePeer  = "\x1b[38;5;74m"
	styleYou   = "\x1b[38;5;215m"
	styleWarn  = "\x1b[38;5;180m"
)

// UseTrueColor switches the palette to the design's exact RGB values.
func UseTrueColor() {
	stylePeer = "\x1b[38;2;95;179;230m"
	styleYou = "\x1b[38;2;232;164;92m"
	styleWarn = "\x1b[38;2;229;192;123m"
}

// gutterLines lays out one row per wrapped piece of text; "\n" in text starts
// a new wrapped paragraph. name is clipped to the gutter and shown on the
// first row only; later rows get a blank gutter. nameStyle, sepStyle and
// textStyle are ANSI prefixes ("" for none). highlight, if it occurs in the
// text, is drawn bold. cols is the terminal width.
func gutterLines(name, nameStyle, sepStyle, textStyle, text, highlight string, cols int, color bool) []string {
	var pieces []string
	for _, para := range strings.Split(text, "\n") {
		pieces = append(pieces, wrap(para, cols-gutterLeft-1)...)
	}
	out := make([]string, len(pieces))
	for i, p := range pieces {
		n := ""
		if i == 0 {
			n = clip(name, gutterWidth)
		}
		pad := strings.Repeat(" ", gutterWidth-stringWidth(n))
		if color {
			if highlight != "" && strings.Contains(p, highlight) {
				p = strings.Replace(p, highlight, styleReset+styleBold+highlight+styleReset+textStyle, 1)
			}
			out[i] = pad + paint(nameStyle, n) + paint(sepStyle, gutterSep) + paint(textStyle, p)
		} else {
			out[i] = pad + n + gutterSep + p
		}
	}
	return out
}

func paint(style, s string) string {
	if style == "" || s == "" {
		return s
	}
	return style + s + styleReset
}

// clip shortens s to at most w columns, marking the cut with a leading "…"
// so the end of a name (usually the distinguishing part) survives.
func clip(s string, w int) string {
	rs := []rune(s)
	if runesWidth(rs) <= w {
		return s
	}
	cut, i := 1, len(rs)
	for i > 0 && cut+runeWidth(rs[i-1]) <= w {
		i--
		cut += runeWidth(rs[i])
	}
	return "…" + string(rs[i:])
}

// wrap splits text into pieces at most width columns wide, breaking at
// spaces where it can and mid-word where it can't. It always returns at
// least one piece.
func wrap(text string, width int) []string {
	if width < 1 {
		return []string{text}
	}
	var out []string
	var cur []rune
	curW := 0
	flush := func() {
		out = append(out, strings.TrimRight(string(cur), " "))
		cur, curW = nil, 0
	}
	for _, word := range strings.SplitAfter(text, " ") {
		if curW+stringWidth(strings.TrimRight(word, " ")) > width && curW > 0 {
			flush()
		}
		for _, r := range word {
			rw := runeWidth(r)
			if curW+rw > width && r != ' ' {
				flush()
			}
			cur = append(cur, r)
			curW += rw
		}
	}
	flush()
	return out
}
