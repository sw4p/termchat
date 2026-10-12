package ui

import (
	"strings"
	"testing"
)

func TestParseLine(t *testing.T) {
	tests := []struct {
		in      string
		text    string
		cmd     Command
		wantErr string // substring; "" means no error
	}{
		{"hello", "hello", Command{}, ""},
		{"  hello  ", "hello", Command{}, ""},
		{"not /a command", "not /a command", Command{}, ""},
		{"/next", "", Command{Name: "next"}, ""},
		{"/NEXT", "", Command{Name: "next"}, ""},
		{"/quit", "", Command{Name: "quit"}, ""},
		{"/nick sam", "", Command{Name: "nick", Arg: "sam"}, ""},
		{"/nick   sam  ", "", Command{Name: "nick", Arg: "sam"}, ""},
		{"//etc/hosts", "/etc/hosts", Command{}, ""},
		{"//", "/", Command{}, ""},
		{"/nick", "", Command{}, "usage: /nick <name>"},
		{"/next now", "", Command{}, "/next doesn't take an argument"},
		{"/dance", "", Command{}, "unknown command /dance"},
		{"/", "", Command{}, "unknown command /"},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			text, cmd, err := ParseLine(tc.in)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("ParseLine(%q) err = %v, want %q", tc.in, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseLine(%q): %v", tc.in, err)
			}
			if text != tc.text || cmd != tc.cmd {
				t.Errorf("ParseLine(%q) = %q, %+v; want %q, %+v", tc.in, text, cmd, tc.text, tc.cmd)
			}
		})
	}
}

func TestHelpLinesCoverEveryCommand(t *testing.T) {
	help := strings.Join(HelpLines(), "\n")
	for _, c := range commands {
		if !strings.Contains(help, "/"+c.name) {
			t.Errorf("help doesn't mention /%s", c.name)
		}
		line := "/" + c.name
		if c.arg != "" {
			line += " x"
		}
		if _, _, err := ParseLine(line); err != nil {
			t.Errorf("/%s from the help doesn't parse: %v", c.name, err)
		}
	}
}
