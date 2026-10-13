package ui

import (
	"bufio"
	"io"
	"os"
	"strings"
	"sync"

	"golang.org/x/term"
)

// Console is the program's whole view of the terminal: print lines, set the
// prompt, and receive what the user submits.
//
// Lines delivers each submitted line. Done is closed when the user wants out
// (Ctrl-C, Ctrl-D on an empty line, end of input, or Interrupt).
type Console struct {
	screen *Screen
	lines  chan string
	typing chan struct{}
	done   chan struct{}
	once   sync.Once

	restoreMu sync.Mutex
	restore   func() // puts the terminal back; nil in plain mode or once done
}

// Open starts a Console on stdin/stdout. When both are terminals it switches
// stdin to raw mode, clears the screen and runs the line editor. Otherwise
// (piped input, output redirected to a file) it falls back to plain
// line-by-line I/O.
//
// Always call Close, which restores the terminal.
func Open(limit int) (*Console, error) {
	inFd, outFd := int(os.Stdin.Fd()), int(os.Stdout.Fd())
	if !term.IsTerminal(inFd) || !term.IsTerminal(outFd) {
		return NewPlain(os.Stdin, os.Stdout), nil
	}
	old, err := term.MakeRaw(inFd)
	if err != nil {
		return nil, err
	}
	// Start on a clean screen: home the cursor and clear what's visible.
	// Scrollback is left alone, so the user's earlier output isn't lost.
	os.Stdout.WriteString("\x1b[H\x1b[2J")
	width := func() int {
		w, _, err := term.GetSize(outFd)
		if err != nil || w <= 0 {
			return 80
		}
		return w
	}
	c := newRaw(os.Stdin, os.Stdout, width, limit)
	c.screen.SetColor(os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb")
	if ct := os.Getenv("COLORTERM"); ct == "truecolor" || ct == "24bit" {
		UseTrueColor()
	}
	c.restore = func() { term.Restore(inFd, old) }
	return c, nil
}

// newRaw runs the line editor over in, which must already be in raw mode.
func newRaw(in io.Reader, out io.Writer, width func() int, limit int) *Console {
	c := &Console{
		screen: NewScreen(out, false, width, limit),
		// Buffered and sent to without blocking (see readKeys), so a burst of
		// pasted lines can't stall the key loop.
		lines:  make(chan string, 16),
		typing: make(chan struct{}, 1),
		done:   make(chan struct{}),
	}
	go c.readKeys(in, NewEditor(limit))
	return c
}

// NewPlain returns a Console that reads whole lines from in and writes plain
// lines to out, with no prompt and no editing.
func NewPlain(in io.Reader, out io.Writer) *Console {
	c := &Console{
		screen: NewScreen(out, true, func() int { return 0 }, 0),
		lines:  make(chan string),
		typing: make(chan struct{}), // never fires: no keystrokes in plain mode
		done:   make(chan struct{}),
	}
	go c.readLines(in)
	return c
}

func (c *Console) readKeys(in io.Reader, ed *Editor) {
	var dec Decoder
	buf := make([]byte, 256)
	for {
		n, err := in.Read(buf)
		for _, k := range dec.Feed(buf[:n]) {
			ev, text := ed.Handle(k)
			switch ev {
			case EventChanged:
				c.screen.SetInput(ed.String())
				if k.Type == KeyRune && !strings.HasPrefix(ed.String(), "/") {
					select { // typing a message (not a /command)
					case c.typing <- struct{}{}:
					default:
					}
				}
			case EventSubmit:
				c.screen.SetInput("")
				if text != "" {
					select {
					case c.lines <- text:
					default: // nobody is keeping up; drop rather than freeze the keyboard
					}
				}
			case EventQuit:
				c.Interrupt()
				return
			}
		}
		if err != nil {
			c.Interrupt()
			return
		}
	}
}

func (c *Console) readLines(in io.Reader) {
	sc := bufio.NewScanner(in)
	for sc.Scan() {
		select {
		case c.lines <- sc.Text():
		case <-c.done:
			return
		}
	}
	c.Interrupt()
}

// Lines delivers each line the user submits. Empty lines are skipped in raw
// mode.
func (c *Console) Lines() <-chan string { return c.lines }

// Typing receives a value (at most one pending) whenever the user types a
// character of a message. It never fires in plain mode.
func (c *Console) Typing() <-chan struct{} { return c.typing }

// Done is closed when the user asks to quit or input ends.
func (c *Console) Done() <-chan struct{} { return c.done }

// Interrupt closes Done as if the user pressed Ctrl-C. It is safe to call from
// a signal handler goroutine, and more than once.
func (c *Console) Interrupt() {
	c.once.Do(func() { close(c.done) })
}

// Println prints one line of output above the prompt.
func (c *Console) Println(text string) { c.screen.Println(text) }

// Message prints a chat message from name above the prompt. self is true for
// the user's own messages.
func (c *Console) Message(name string, self bool, text string) {
	c.screen.Message(name, self, text)
}

// Bell rings the terminal bell. It does nothing in plain mode, where output
// is probably going to a file or another program.
func (c *Console) Bell() { c.screen.Bell() }

// SetPrompt changes the prompt shown before the input line.
func (c *Console) SetPrompt(p string) { c.screen.SetPrompt(p) }

// SetStatus sets the hint shown after the empty prompt and the note on the
// rule above it. See Screen.SetStatus.
func (c *Console) SetStatus(hint, note string) { c.screen.SetStatus(hint, note) }

// Hello prints the greeting, with nick in bold.
func (c *Console) Hello(nick, text string) { c.screen.Hello(nick, text) }

// Banner prints a rule with "text name" in the middle.
func (c *Console) Banner(text, name string) { c.screen.Banner(text, name) }

// Close erases the prompt and restores the terminal to how it was. It's safe
// to call more than once.
func (c *Console) Close() {
	c.Interrupt()
	c.screen.Close()
	c.restoreMu.Lock()
	defer c.restoreMu.Unlock()
	if c.restore != nil {
		c.restore()
		c.restore = nil
	}
}
