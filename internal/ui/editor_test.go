package ui

import (
	"strings"
	"testing"
)

func typeString(s string) []Key {
	var keys []Key
	for _, c := range s {
		keys = append(keys, r(c))
	}
	return keys
}

func keys(groups ...[]Key) []Key {
	var all []Key
	for _, g := range groups {
		all = append(all, g...)
	}
	return all
}

func one(k Key) []Key { return []Key{k} }

func TestEditorLine(t *testing.T) {
	tests := []struct {
		name string
		max  int
		in   []Key
		want string
	}{
		{"typing", 160, typeString("hello"), "hello"},
		{"backspace", 160, keys(typeString("helo"), one(bs), typeString("lo")), "hello"},
		{"backspace on empty", 160, keys(one(bs), typeString("a")), "a"},
		{"backspace multibyte", 160, keys(typeString("café"), one(bs)), "caf"},
		{"backspace combining accent", 160, keys(typeString("café"), one(bs)), "caf"},
		{"backspace zwj emoji", 160, keys(typeString("hi 👩‍💻"), one(bs)), "hi "},
		{"backspace skin tone", 160, keys(typeString("ok 👍🏽"), one(bs)), "ok "},
		{"backspace flag-less vs16", 160, keys(typeString("a❤️"), one(bs)), "a"},
		{"ctrl-u", 160, keys(typeString("oops"), one(Key{Type: KeyCtrlU}), typeString("ok")), "ok"},
		{"ctrl-w", 160, keys(typeString("one two  "), one(Key{Type: KeyCtrlW})), "one "},
		{"ctrl-w twice", 160, keys(typeString("one two"), one(Key{Type: KeyCtrlW}), one(Key{Type: KeyCtrlW})), ""},
		{"tab becomes space", 160, typeString("a\tb"), "a b"},
		{"arrows ignored", 160, keys(typeString("a"), one(other), typeString("b")), "ab"},
		{"control runes refused", 160, typeString("a\x07‮b"), "ab"},
		{"limit", 5, typeString("abcdefgh"), "abcde"},
		{"limit counts runes not bytes", 3, typeString("🦦🦦🦦🦦"), "🦦🦦🦦"},
		{"ctrl-d with text ignored", 160, keys(typeString("x"), one(Key{Type: KeyCtrlD})), "x"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := NewEditor(tc.max)
			for _, k := range tc.in {
				e.Handle(k)
			}
			if got := e.String(); got != tc.want {
				t.Errorf("line = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEditorEvents(t *testing.T) {
	e := NewEditor(3)
	check := func(k Key, wantEv EventKind, wantText string) {
		t.Helper()
		ev, text := e.Handle(k)
		if ev != wantEv || text != wantText {
			t.Errorf("Handle(%v) = %v %q, want %v %q", k, ev, text, wantEv, wantText)
		}
	}
	check(r('a'), EventChanged, "")
	check(other, EventNone, "")
	check(r('b'), EventChanged, "")
	check(r('c'), EventChanged, "")
	check(r('d'), EventNone, "") // refused: over the limit
	check(Key{Type: KeyCtrlD}, EventNone, "")
	check(enter, EventSubmit, "abc")
	if e.Len() != 0 {
		t.Errorf("editor not cleared after submit: %q", e.String())
	}
	check(bs, EventNone, "")
	check(r(' '), EventChanged, "")
	check(enter, EventSubmit, "") // whitespace-only submits as empty
	check(Key{Type: KeyCtrlD}, EventQuit, "")
	check(Key{Type: KeyCtrlC}, EventQuit, "")
}

func TestEditorSubmitTrims(t *testing.T) {
	e := NewEditor(160)
	for _, k := range typeString("  hi there  ") {
		e.Handle(k)
	}
	if _, text := e.Handle(enter); text != "hi there" {
		t.Errorf("submitted %q", text)
	}
}

func TestEditorPasteFullMessage(t *testing.T) {
	e := NewEditor(160)
	for _, k := range typeString(strings.Repeat("x", 200)) {
		e.Handle(k)
	}
	if e.Len() != 160 {
		t.Errorf("after pasting 200 runes, Len = %d, want 160", e.Len())
	}
}
