package main

import (
	"context"

	"github.com/sw4p/termchat/internal/ui"
)

// console is what the command loop needs from the terminal.
type console interface {
	Println(text string)
	Lines() <-chan string
	Typing() <-chan struct{}
	Done() <-chan struct{}
}

// commander is what the command loop drives. *session.Manager provides it.
type commander interface {
	Send(text string)
	Next()
	Leave()
	Find()
	Invisible()
	Visible()
	SetNick(name string) error
	Who()
	Typing()
}

// runCommands feeds each line the user submits to the session, until they
// quit (/quit, Ctrl-C, Ctrl-D, end of input) or ctx is cancelled.
func runCommands(ctx context.Context, con console, m commander) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-con.Done():
			return
		case line := <-con.Lines():
			if !dispatch(con, m, line) {
				return
			}
		case <-con.Typing():
			m.Typing()
		}
	}
}

// dispatch acts on one line. It returns false for /quit.
func dispatch(con console, m commander, line string) bool {
	text, cmd, err := ui.ParseLine(line)
	if err != nil {
		con.Println("* " + err.Error())
		return true
	}
	switch cmd.Name {
	case "":
		if text != "" {
			m.Send(text)
		}
	case "quit":
		return false
	case "help":
		for _, l := range ui.HelpLines() {
			con.Println(l)
		}
	case "next":
		m.Next()
	case "leave":
		m.Leave()
	case "find":
		m.Find()
	case "invisible":
		m.Invisible()
	case "visible":
		m.Visible()
	case "who":
		m.Who()
	case "nick":
		if err := m.SetNick(cmd.Arg); err != nil {
			con.Println("* " + err.Error())
		}
	}
	return true
}
