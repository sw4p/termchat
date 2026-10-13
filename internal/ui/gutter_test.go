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
