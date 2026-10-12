package ui

import (
	"fmt"
	"strings"
)

// Command is a parsed slash command such as "/nick quiet-otter".
type Command struct {
	Name string // without the slash, lowercase: "nick"
	Arg  string // "quiet-otter"; empty for commands that take none
}

type commandSpec struct {
	name, arg, help string
}

var commands = []commandSpec{
	{"next", "", "leave this chat and find someone new"},
	{"leave", "", "leave this chat and stop looking"},
	{"find", "", "start looking for someone"},
	{"invisible", "", "stop being matched (also leaves the current chat)"},
	{"visible", "", "be matchable again"},
	{"nick", "name", "change your nickname"},
	{"who", "", "show how many people are available"},
	{"help", "", "show this help"},
	{"quit", "", "leave and exit"},
}

// ParseLine splits a submitted line into either a chat message or a command.
//
//   - "hello"          → text "hello"
//   - "/nick sam"      → Command{"nick", "sam"}
//   - "//etc/hosts"    → text "/etc/hosts" (a doubled slash sends one slash)
//
// An unknown command, or the wrong number of arguments, returns an error
// that is ready to show to the user.
func ParseLine(line string) (text string, cmd Command, err error) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "/") {
		return line, Command{}, nil
	}
	if strings.HasPrefix(line, "//") {
		return line[1:], Command{}, nil
	}
	name, arg, _ := strings.Cut(line[1:], " ")
	name = strings.ToLower(name)
	arg = strings.TrimSpace(arg)

	for _, c := range commands {
		if c.name != name {
			continue
		}
		switch {
		case c.arg != "" && arg == "":
			return "", Command{}, fmt.Errorf("usage: /%s <%s>", c.name, c.arg)
		case c.arg == "" && arg != "":
			return "", Command{}, fmt.Errorf("/%s doesn't take an argument", c.name)
		}
		return "", Command{Name: name, Arg: arg}, nil
	}
	return "", Command{}, fmt.Errorf("unknown command /%s (try /help)", name)
}

// HelpLines returns the /help text, one line per command.
func HelpLines() []string {
	lines := make([]string, 0, len(commands)+1)
	for _, c := range commands {
		usage := "/" + c.name
		if c.arg != "" {
			usage += " <" + c.arg + ">"
		}
		lines = append(lines, fmt.Sprintf("  %-14s %s", usage, c.help))
	}
	return append(lines, "  //text         send a message that starts with /")
}
