package session

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/sw4p/termchat/internal/crypto"
	"github.com/sw4p/termchat/internal/protocol"
)

// conversation is one chat over an already-handshaken connection.
//
// Two goroutines run inside it: receive prints the partner's messages, and
// pinger keeps the connection alive. The Manager sends the user's messages
// and ends the chat from its side with leave. endMu makes sure exactly one
// side ends it, so the user sees either "you left" or "<partner> left",
// never both.
type conversation struct {
	conn    *crypto.SecureConn
	partner string
	out     Output
	tm      Timeouts
	// onPartnerLeft is called (from the receive goroutine) when the chat
	// ended from the other side: they left, disconnected or went silent.
	onPartnerLeft func(*conversation)
	// onTyping, if set, is called when the partner starts or stops typing.
	onTyping func(c *conversation, on bool)
	// bell rings the terminal bell for each incoming message.
	bell bool

	typingTimer *time.Timer // only touched by the receive goroutine

	writeMu sync.Mutex // holds the write deadline and the write together

	endMu sync.Mutex
	ended bool
	done  chan struct{}
}

func newConversation(conn *crypto.SecureConn, partner string, out Output, tm Timeouts, onPartnerLeft func(*conversation)) *conversation {
	return &conversation{
		conn:          conn,
		partner:       partner,
		out:           out,
		tm:            tm,
		onPartnerLeft: onPartnerLeft,
		done:          make(chan struct{}),
	}
}

// start prints the new-chat banner and starts the background goroutines.
func (c *conversation) start() {
	c.out.Banner("new chat with", c.partner)
	c.printf("! strangers may send links or commands.\ntermchat never asks you to run anything.")
	go c.receive()
	go c.pinger()
}

func (c *conversation) printf(format string, args ...any) {
	c.out.Println(fmt.Sprintf(format, args...))
}

func (c *conversation) send(m protocol.Message) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	// A peer that stops reading must not block us forever.
	c.conn.SetWriteDeadline(time.Now().Add(c.tm.Idle))
	return c.conn.WriteMessage(m)
}

// end marks the chat as over and reports whether this call was the one that
// ended it. Only the first caller gets true.
func (c *conversation) end() bool {
	c.endMu.Lock()
	defer c.endMu.Unlock()
	if c.ended {
		return false
	}
	c.ended = true
	return true
}

// finish closes the connection and stops the goroutines. Call it only after
// end returned true.
func (c *conversation) finish() {
	c.conn.Close()
	close(c.done)
}

// leave ends the chat from our side: tell the partner, then hang up.
func (c *conversation) leave() {
	if c.end() {
		c.send(protocol.Message{Type: protocol.TypeLeave})
		c.printf("* you left the chat")
		c.finish()
	}
}

// receive prints the partner's messages until they leave, the connection
// breaks, or nothing (not even a ping) arrives for tm.Idle.
func (c *conversation) receive() {
	for {
		c.conn.SetReadDeadline(time.Now().Add(c.tm.Idle))
		m, err := c.conn.ReadMessage()
		if err != nil {
			if protocol.IsFatal(err) {
				break
			}
			if errors.Is(err, protocol.ErrTooLong) {
				c.printf("* message dropped (too long)")
			}
			continue // anything else malformed or unknown: skip it
		}
		if m.Type == protocol.TypeLeave {
			break
		}
		switch m.Type {
		case protocol.TypeMsg:
			c.setTyping(false)
			c.out.Message(c.partner, false, m.Text)
			if c.bell {
				c.out.Bell()
			}
		case protocol.TypeTyping:
			c.setTyping(true)
		}
		// ping needs no action: any frame resets the idle deadline.
	}
	c.setTyping(false)
	// The same notice whether they quit, crashed or timed out, so the reason
	// isn't revealed.
	if c.end() {
		c.printf("* %s left", c.partner)
		c.finish()
		if c.onPartnerLeft != nil {
			c.onPartnerLeft(c)
		}
	}
}

// setTyping reports the partner's typing state. "On" lasts TypingClear
// after the latest typing frame, so a partner who stops typing without
// sending anything stops showing as typing. Only receive calls it.
func (c *conversation) setTyping(on bool) {
	if c.typingTimer != nil {
		c.typingTimer.Stop()
		c.typingTimer = nil
	}
	if c.onTyping == nil {
		return
	}
	c.onTyping(c, on)
	if on {
		c.typingTimer = time.AfterFunc(c.tm.TypingClear, func() { c.onTyping(c, false) })
	}
}

func (c *conversation) pinger() {
	t := time.NewTicker(c.tm.Ping)
	defer t.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-t.C:
			c.send(protocol.Message{Type: protocol.TypePing})
		}
	}
}
