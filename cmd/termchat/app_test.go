package main

import (
	"errors"
	"slices"
	"testing"
)

type fakeConsole struct{ printed []string }

func (f *fakeConsole) Println(s string)        { f.printed = append(f.printed, s) }
func (f *fakeConsole) Lines() <-chan string    { return nil }
func (f *fakeConsole) Done() <-chan struct{}   { return nil }
func (f *fakeConsole) Typing() <-chan struct{} { return nil }

type fakeSession struct{ calls []string }

func (f *fakeSession) Send(text string) { f.calls = append(f.calls, "send "+text) }
func (f *fakeSession) Next()            { f.calls = append(f.calls, "next") }
func (f *fakeSession) Leave()           { f.calls = append(f.calls, "leave") }
func (f *fakeSession) Find()            { f.calls = append(f.calls, "find") }
func (f *fakeSession) Invisible()       { f.calls = append(f.calls, "invisible") }
func (f *fakeSession) Visible()         { f.calls = append(f.calls, "visible") }
func (f *fakeSession) Who()             { f.calls = append(f.calls, "who") }
func (f *fakeSession) Typing()          { f.calls = append(f.calls, "typing") }
func (f *fakeSession) SetNick(name string) error {
	f.calls = append(f.calls, "nick "+name)
	if name == "bad" {
		return errors.New("invalid nick")
	}
	return nil
}

func TestDispatch(t *testing.T) {
	tests := []struct {
		line      string
		calls     []string
		printed   int // lines printed to the console
		keepGoing bool
	}{
		{"hello", []string{"send hello"}, 0, true},
		{"//etc", []string{"send /etc"}, 0, true},
		{"/next", []string{"next"}, 0, true},
		{"/leave", []string{"leave"}, 0, true},
		{"/find", []string{"find"}, 0, true},
		{"/invisible", []string{"invisible"}, 0, true},
		{"/visible", []string{"visible"}, 0, true},
		{"/who", []string{"who"}, 0, true},
		{"/nick sam", []string{"nick sam"}, 0, true},
		{"/nick bad", []string{"nick bad"}, 1, true},
		{"/nick", nil, 1, true},
		{"/dance", nil, 1, true},
		{"/help", nil, 10, true},
		{"/quit", nil, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.line, func(t *testing.T) {
			con, s := &fakeConsole{}, &fakeSession{}
			if got := dispatch(con, s, tc.line); got != tc.keepGoing {
				t.Errorf("dispatch returned %v", got)
			}
			if !slices.Equal(s.calls, tc.calls) {
				t.Errorf("calls = %v, want %v", s.calls, tc.calls)
			}
			if len(con.printed) != tc.printed {
				t.Errorf("printed %d lines, want %d: %q", len(con.printed), tc.printed, con.printed)
			}
		})
	}
}
