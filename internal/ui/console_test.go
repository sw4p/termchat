package ui

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func nextLine(t *testing.T, c *Console) string {
	t.Helper()
	select {
	case l := <-c.Lines():
		return l
	case <-time.After(2 * time.Second):
		t.Fatal("no line submitted")
		return ""
	}
}

func waitDone(t *testing.T, c *Console) {
	t.Helper()
	select {
	case <-c.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Done never closed")
	}
}

func TestRawConsole(t *testing.T) {
	inR, inW := io.Pipe()
	out := &syncBuffer{}
	c := newRaw(inR, out, func() int { return 80 }, 160)
	defer c.Close()
	c.SetPrompt("> ")

	io.WriteString(inW, "helo\x7flo\r")
	if got := nextLine(t, c); got != "hello" {
		t.Errorf("line = %q, want %q", got, "hello")
	}
	io.WriteString(inW, "\r   \r") // empty lines aren't delivered
	io.WriteString(inW, "caf\xc3") // é split across two writes
	io.WriteString(inW, "\xa9\r")
	if got := nextLine(t, c); got != "café" {
		t.Errorf("line = %q, want %q", got, "café")
	}

	io.WriteString(inW, "typing")
	deadline := time.Now().Add(2 * time.Second)
	for !strings.HasSuffix(out.String(), "> typing") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	c.Println("bob: hi")
	if got := out.String(); !strings.HasSuffix(got, "\r\x1b[Kbob: hi\r\n\r\x1b[K> typing") {
		t.Errorf("incoming line didn't redraw the half-typed input; output ends %q", got[max(0, len(got)-60):])
	}

	io.WriteString(inW, "\x03")
	waitDone(t, c)
}

func TestRawConsoleCtrlDOnEmptyLine(t *testing.T) {
	inR, inW := io.Pipe()
	c := newRaw(inR, &syncBuffer{}, func() int { return 80 }, 160)
	defer c.Close()
	io.WriteString(inW, "\x04")
	waitDone(t, c)
}

func TestRawConsoleEOF(t *testing.T) {
	inR, inW := io.Pipe()
	c := newRaw(inR, &syncBuffer{}, func() int { return 80 }, 160)
	defer c.Close()
	inW.Close()
	waitDone(t, c)
}

func TestPlainConsole(t *testing.T) {
	inR, inW := io.Pipe()
	out := &syncBuffer{}
	c := NewPlain(inR, out)
	defer c.Close()

	go io.WriteString(inW, "hello\nsecond line\n")
	if got := nextLine(t, c); got != "hello" {
		t.Errorf("line = %q", got)
	}
	if got := nextLine(t, c); got != "second line" {
		t.Errorf("line = %q", got)
	}
	c.SetPrompt("> ")
	c.Println("bob: hi")
	if got := out.String(); got != "bob: hi\n" {
		t.Errorf("output = %q", got)
	}
	inW.Close()
	waitDone(t, c)
}

func TestInterruptAndCloseAreIdempotent(t *testing.T) {
	inR, _ := io.Pipe()
	c := NewPlain(inR, &syncBuffer{})
	restored := 0
	c.restore = func() { restored++ }
	c.Interrupt()
	c.Interrupt()
	c.Close()
	c.Close()
	waitDone(t, c)
	if restored != 1 {
		t.Errorf("terminal restored %d times, want 1", restored)
	}
}

func typingFired(c *Console) bool {
	select {
	case <-c.Typing():
		return true
	case <-time.After(100 * time.Millisecond):
		return false
	}
}

func TestRawConsoleTypingSignal(t *testing.T) {
	inR, inW := io.Pipe()
	c := newRaw(inR, &syncBuffer{}, func() int { return 80 }, 160)
	defer c.Close()

	io.WriteString(inW, "h")
	if !typingFired(c) {
		t.Error("typing a message character didn't signal")
	}
	io.WriteString(inW, "\x7f") // backspace: not typing a new character
	if typingFired(c) {
		t.Error("backspace signalled typing")
	}
	io.WriteString(inW, "/nick")
	if typingFired(c) {
		t.Error("typing a /command signalled typing")
	}
}

func TestBellRawOnly(t *testing.T) {
	inR, _ := io.Pipe()
	raw := &syncBuffer{}
	c := newRaw(inR, raw, func() int { return 80 }, 160)
	c.Bell()
	c.Close()
	c.Bell() // after Close: silent
	if n := strings.Count(raw.String(), "\a"); n != 1 {
		t.Errorf("raw console rang %d bells, want 1", n)
	}

	plain := &syncBuffer{}
	p := NewPlain(inR, plain)
	p.Bell()
	if strings.Contains(plain.String(), "\a") {
		t.Error("plain console rang the bell")
	}
}
