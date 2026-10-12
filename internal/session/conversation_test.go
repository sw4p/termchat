package session

import (
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/sw4p/termchat/internal/crypto"
	"github.com/sw4p/termchat/internal/protocol"
)

const (
	aliceID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	bobID   = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// securePipe returns the two ends of an in-memory connection after the key
// exchange.
func securePipe(t *testing.T) (*crypto.SecureConn, *crypto.SecureConn) {
	t.Helper()
	a, b := net.Pipe()
	ch := make(chan *crypto.SecureConn, 1)
	go func() {
		sb, err := crypto.Handshake(b)
		if err != nil {
			t.Error(err)
		}
		ch <- sb
	}()
	sa, err := crypto.Handshake(a)
	if err != nil {
		t.Fatal(err)
	}
	sb := <-ch
	if sb == nil {
		t.FailNow()
	}
	t.Cleanup(func() { sa.Close(); sb.Close() })
	return sa, sb
}

// startConv runs a conversation on conn. The returned channel is closed
// when the partner ends it.
func startConv(conn *crypto.SecureConn, partner string, tm Timeouts) (*conversation, *testOut, chan struct{}) {
	out := &testOut{}
	left := make(chan struct{})
	c := newConversation(conn, partner, out, tm, func(*conversation) { close(left) })
	c.start()
	return c, out, left
}

func waitClosed(t *testing.T, ch chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func TestConversationExchangeAndLeave(t *testing.T) {
	a, b := securePipe(t)
	alice, aliceOut, aliceLeft := startConv(a, "bob", testTimeouts)
	bob, bobOut, bobLeft := startConv(b, "alice", testTimeouts)

	go alice.send(protocol.Message{Type: protocol.TypeMsg, Text: "hi bob"})
	eventually(t, "bob to get it", func() bool { return bobOut.count("alice: hi bob") == 1 })
	go bob.send(protocol.Message{Type: protocol.TypeMsg, Text: "hey"})
	eventually(t, "alice to get it", func() bool { return aliceOut.count("bob: hey") == 1 })

	alice.leave()
	waitClosed(t, bobLeft, "bob to hear alice left")
	if bobOut.count("* alice left") != 1 || aliceOut.count("* you left the chat") != 1 {
		t.Errorf("notices:\n%s\n---\n%s", aliceOut, bobOut)
	}
	select {
	case <-aliceLeft:
		t.Error("onPartnerLeft ran on the side that left")
	case <-time.After(50 * time.Millisecond):
	}
	if aliceOut.count("bob left") != 0 {
		t.Errorf("alice left but was told bob left:\n%s", aliceOut)
	}
}

func TestConversationPartnerDisconnects(t *testing.T) {
	a, b := securePipe(t)
	_, out, left := startConv(a, "bob", testTimeouts)
	b.Close() // bob's process dies without sending leave
	waitClosed(t, left, "the disconnect to be noticed")
	if out.count("* bob left") != 1 {
		t.Errorf("output:\n%s", out)
	}
}

func TestConversationIdleTimeout(t *testing.T) {
	a, _ := securePipe(t)
	tm := testTimeouts
	tm.Idle = 100 * time.Millisecond
	_, out, left := startConv(a, "bob", tm)
	// bob's end is open but silent: no messages, no pings.
	waitClosed(t, left, "the idle timeout")
	if out.count("* bob left") != 1 {
		t.Errorf("output:\n%s", out)
	}
}

func TestConversationPingsKeepItAlive(t *testing.T) {
	a, b := securePipe(t)
	tm := testTimeouts
	tm.Ping = 20 * time.Millisecond
	tm.Idle = 150 * time.Millisecond
	_, _, aliceLeft := startConv(a, "bob", tm)
	_, _, bobLeft := startConv(b, "alice", tm)

	select {
	case <-aliceLeft:
		t.Fatal("chat timed out despite pings")
	case <-bobLeft:
		t.Fatal("chat timed out despite pings")
	case <-time.After(5 * tm.Idle):
	}
}

func TestConversationHandlesBadPeerMessages(t *testing.T) {
	a, b := securePipe(t)
	_, out, _ := startConv(a, "bob", testTimeouts)

	// bob runs a modified client.
	for _, f := range []string{
		`{"t":"msg","text":"` + strings.Repeat("x", protocol.MaxMessageRunes+1) + `"}`,
		`not json`,
		`{"t":"file","text":"run me"}`,
		`{"t":"msg","text":"\u001b]0;pwned\u0007\u001b[2Jclean"}`,
	} {
		go b.WritePayload([]byte(f))
		time.Sleep(5 * time.Millisecond) // keep them in order over net.Pipe
	}
	eventually(t, "the dropped notice", func() bool { return out.count("* message dropped (too long)") == 1 })
	eventually(t, "the clean message", func() bool { return out.count("bob: clean") == 1 })
	if s := out.String(); strings.ContainsRune(s, 0x1b) || strings.Contains(s, "run me") {
		t.Errorf("unsafe content reached the screen:\n%q", s)
	}
}

// ─── Handshake ──────────────────────────────────────────────────────────────

// listenAndAccept plays the listening side: read the hello, reply accept.
func listenAndAccept(conn net.Conn, id, nick string) (protocol.Message, error) {
	sc, hello, err := readHello(conn, id, time.Second)
	if err != nil {
		return protocol.Message{}, err
	}
	return hello, sc.WriteMessage(protocol.Message{Type: protocol.TypeAccept, ID: id, Nick: nick})
}

func TestHandshake(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()

	type result struct {
		m   protocol.Message
		err error
	}
	ch := make(chan result)
	go func() {
		m, err := listenAndAccept(b, bobID, "bob")
		ch <- result{m, err}
	}()

	_, got, err := handshakeDial(a, aliceID, "alice", time.Second)
	if err != nil {
		t.Fatalf("dialer: %v", err)
	}
	if got.Nick != "bob" || got.ID != bobID {
		t.Errorf("dialer got partner %+v", got)
	}
	r := <-ch
	if r.err != nil {
		t.Fatalf("listener: %v", r.err)
	}
	if r.m.Nick != "alice" || r.m.ID != aliceID {
		t.Errorf("listener got partner %+v", r.m)
	}
}

func TestHandshakeDialBusy(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	go func() {
		sc, _, err := readHello(b, bobID, time.Second)
		if err == nil {
			sc.WriteMessage(protocol.Message{Type: protocol.TypeBusy})
		}
	}()
	if _, _, err := handshakeDial(a, aliceID, "alice", time.Second); !errors.Is(err, errBusy) {
		t.Errorf("err = %v, want errBusy", err)
	}
}

func TestReadHelloRejectsNonHello(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	go func() {
		sc, err := crypto.Handshake(a)
		if err == nil {
			sc.WriteMessage(protocol.Message{Type: protocol.TypeMsg, Text: "skip the hello"})
		}
	}()
	if _, _, err := readHello(b, bobID, time.Second); err == nil {
		t.Error("accepted a msg in place of hello")
	}
}

func TestReadHelloRejectsPlaintextPeer(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	// An old client: sends a plaintext hello frame with no key exchange.
	go protocol.WriteMessage(a, protocol.Message{Type: protocol.TypeHello, ID: aliceID, Nick: "alice"})
	go io.Copy(io.Discard, a)
	if _, _, err := readHello(b, bobID, time.Second); !errors.Is(err, crypto.ErrBadMagic) {
		t.Errorf("err = %v, want crypto.ErrBadMagic", err)
	}
}

func TestHandshakeRejectsSelf(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	go handshakeDial(a, aliceID, "alice", time.Second)
	if _, _, err := readHello(b, aliceID, time.Second); !errors.Is(err, errSelf) {
		t.Errorf("err = %v, want errSelf", err)
	}
}

func TestHandshakeTimeout(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	go io.Copy(io.Discard, a) // takes our key but never answers
	start := time.Now()
	if _, _, err := readHello(b, bobID, 50*time.Millisecond); err == nil {
		t.Fatal("handshake with a silent peer succeeded")
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("took %v to time out", d)
	}
}
