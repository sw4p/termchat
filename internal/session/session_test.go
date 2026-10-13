package session

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sw4p/termchat/internal/discovery"
	"github.com/sw4p/termchat/internal/protocol"
)

var testTimeouts = Timeouts{
	Handshake: time.Second,
	Ping:      100 * time.Millisecond,
	Idle:      2 * time.Second,
	Dial:      500 * time.Millisecond,
	RetryMin:  5 * time.Millisecond,
	RetryMax:  30 * time.Millisecond,
	RetryPeer: 50 * time.Millisecond,

	TypingEvery: 50 * time.Millisecond,
	TypingClear: 150 * time.Millisecond,
}

// ─── A fake LAN: discovery without mDNS ─────────────────────────────────────

type fakeLAN struct {
	mu    sync.Mutex
	ads   map[string]*fakeAd
	views []*fakeView
}

func newLAN() *fakeLAN { return &fakeLAN{ads: map[string]*fakeAd{}} }

type fakeAd struct {
	lan   *fakeLAN
	id    string
	addr  string
	state discovery.State
}

func (a *fakeAd) SetState(s discovery.State) {
	a.lan.mu.Lock()
	a.state = s
	a.lan.mu.Unlock()
	a.lan.notify()
}

func (a *fakeAd) Close() {
	a.lan.mu.Lock()
	if a.lan.ads[a.id] == a {
		delete(a.lan.ads, a.id)
	}
	a.lan.mu.Unlock()
	a.lan.notify()
}

func (l *fakeLAN) advertise(id, addr string, st discovery.State) *fakeAd {
	a := &fakeAd{lan: l, id: id, addr: addr, state: st}
	l.mu.Lock()
	l.ads[id] = a
	l.mu.Unlock()
	l.notify()
	return a
}

func (l *fakeLAN) notify() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, v := range l.views {
		select {
		case v.updates <- struct{}{}:
		default:
		}
	}
}

type fakeView struct {
	lan     *fakeLAN
	updates chan struct{}
}

func (l *fakeLAN) view() *fakeView {
	v := &fakeView{lan: l, updates: make(chan struct{}, 1)}
	l.mu.Lock()
	l.views = append(l.views, v)
	l.mu.Unlock()
	return v
}

// Peers lists everyone, including the caller: excluding ourselves is the
// session's job, and the real Browser only does it as a courtesy.
func (v *fakeView) Peers() []discovery.Peer {
	v.lan.mu.Lock()
	defer v.lan.mu.Unlock()
	var out []discovery.Peer
	for _, a := range v.lan.ads {
		out = append(out, discovery.Peer{ID: a.id, State: a.state, Addrs: []string{a.addr}})
	}
	return out
}

func (v *fakeView) Updates() <-chan struct{} { return v.updates }

// ─── Test output ────────────────────────────────────────────────────────────

type testOut struct {
	mu     sync.Mutex
	lines  []string
	prompt string
	bells  int
}

func (o *testOut) Bell() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.bells++
}

func (o *testOut) getPrompt() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.prompt
}

func (o *testOut) bellCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.bells
}

func (o *testOut) Println(s string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.lines = append(o.lines, s)
}

// Message records chat lines in the plain "name: text" form.
func (o *testOut) Message(name string, self bool, text string) {
	o.Println(name + ": " + text)
}

func (o *testOut) SetPrompt(p string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.prompt = p
}

func (o *testOut) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return strings.Join(o.lines, "\n")
}

func (o *testOut) count(s string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	n := 0
	for _, l := range o.lines {
		if strings.Contains(l, s) {
			n++
		}
	}
	return n
}

// ─── Peers ──────────────────────────────────────────────────────────────────

type peer struct {
	*Manager
	name string
	out  *testOut
}

func startPeer(t *testing.T, lan *fakeLAN, name string) *peer {
	t.Helper()
	return startPeerWith(t, lan, name, nil)
}

func startPeerWith(t *testing.T, lan *fakeLAN, name string, tweak func(*Config)) *peer {
	t.Helper()
	id := protocol.NewID()
	out := &testOut{}
	cfg := Config{
		ID:         id,
		Nick:       name,
		ListenAddr: "127.0.0.1:0",
		Directory:  lan.view(),
		Advertise: func(port int, st discovery.State) (Advertiser, error) {
			return lan.advertise(id, fmt.Sprintf("127.0.0.1:%d", port), st), nil
		},
		Timeouts: testTimeouts,
		Out:      out,
	}
	if tweak != nil {
		tweak(&cfg)
	}
	m := New(cfg)
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Stop)
	return &peer{Manager: m, name: name, out: out}
}

// partner returns who p is chatting with, or "".
func (p *peer) partner() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state != Chatting || p.current == nil {
		return ""
	}
	return p.current.partner
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func consistently(t *testing.T, what string, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if !cond() {
			t.Fatalf("%s stopped holding", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func paired(a, b *peer) func() bool {
	return func() bool { return a.partner() == b.name && b.partner() == a.name }
}

// ─── Tests ──────────────────────────────────────────────────────────────────

func TestBasicMatch(t *testing.T) {
	lan := newLAN()
	alice := startPeer(t, lan, "alice")
	bob := startPeer(t, lan, "bob")

	eventually(t, "alice and bob to match", paired(alice, bob))
	alice.Send("hi bob 👋")
	eventually(t, "bob to get the message", func() bool { return bob.out.count("alice: hi bob 👋") == 1 })
	if alice.out.count("you: hi bob 👋") != 1 {
		t.Errorf("alice didn't see her own message:\n%s", alice.out)
	}
	if !strings.Contains(alice.out.String(), "─── new chat with bob ───") {
		t.Errorf("no banner:\n%s", alice.out)
	}
}

func TestSimultaneousStartGivesExactlyOneChat(t *testing.T) {
	for i := 0; i < 15; i++ {
		lan := newLAN()
		var alice, bob *peer
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); alice = startPeer(t, lan, "alice") }()
		go func() { defer wg.Done(); bob = startPeer(t, lan, "bob") }()
		wg.Wait()

		eventually(t, "a match", paired(alice, bob))
		consistently(t, "the match", 100*time.Millisecond, paired(alice, bob))
		for _, p := range []*peer{alice, bob} {
			if n := p.out.count("─── new chat"); n != 1 {
				t.Fatalf("run %d: %s saw %d chats start:\n%s", i, p.name, n, p.out)
			}
			if p.out.count(" left") != 0 {
				t.Fatalf("run %d: %s saw a partner leave:\n%s", i, p.name, p.out)
			}
		}
		alice.Stop()
		bob.Stop()
	}
}

func TestThirdPeerWaitsThenMatchesAfterNext(t *testing.T) {
	lan := newLAN()
	ps := []*peer{startPeer(t, lan, "p1"), startPeer(t, lan, "p2"), startPeer(t, lan, "p3")}

	var x, y, z *peer
	eventually(t, "one pair and one waiting", func() bool {
		for i, p := range ps {
			if p.State() == Seeking {
				a, b := ps[(i+1)%3], ps[(i+2)%3]
				if paired(a, b)() {
					x, y, z = a, b, p
					return true
				}
			}
		}
		return false
	})
	consistently(t, "the lone peer waiting", 200*time.Millisecond, func() bool { return z.State() == Seeking })

	x.Next()
	eventually(t, "the lone peer to get a partner", func() bool { return z.partner() != "" })
	if p := z.partner(); p != x.name && p != y.name {
		t.Fatalf("z matched %q", p)
	}
	consistently(t, "x and y staying apart", 200*time.Millisecond, func() bool { return !paired(x, y)() })
	eventually(t, "y to hear x left", func() bool { return y.out.count("* "+x.name+" left") == 1 })
}

func TestNoRematchWithPastPartner(t *testing.T) {
	lan := newLAN()
	alice := startPeer(t, lan, "alice")
	bob := startPeer(t, lan, "bob")
	eventually(t, "a match", paired(alice, bob))

	alice.Next()
	eventually(t, "both seeking", func() bool { return alice.State() == Seeking && bob.State() == Seeking })
	consistently(t, "no rematch", 300*time.Millisecond, func() bool {
		return alice.State() == Seeking && bob.State() == Seeking
	})
	if bob.out.count("* alice left") != 1 || alice.out.count("* you left the chat") != 1 {
		t.Errorf("wrong notices:\nalice:\n%s\nbob:\n%s", alice.out, bob.out)
	}

	carol := startPeer(t, lan, "carol")
	eventually(t, "carol to match someone", func() bool { return carol.partner() != "" })
}

func TestInvisibleIsNeverMatched(t *testing.T) {
	lan := newLAN()
	alice := startPeer(t, lan, "alice")
	alice.Invisible()
	if alice.State() != Invisible {
		t.Fatalf("state = %v", alice.State())
	}
	bob := startPeer(t, lan, "bob")

	consistently(t, "nobody matching", 300*time.Millisecond, func() bool {
		return alice.State() == Invisible && bob.State() == Seeking
	})
	if n := len(lan.view().Peers()); n != 1 {
		t.Errorf("%d peers advertised, want only bob", n)
	}

	alice.Visible()
	eventually(t, "a match after /visible", paired(alice, bob))
}

func TestInvisibleLeavesTheChat(t *testing.T) {
	lan := newLAN()
	alice := startPeer(t, lan, "alice")
	bob := startPeer(t, lan, "bob")
	eventually(t, "a match", paired(alice, bob))

	alice.Invisible()
	eventually(t, "bob to hear alice left", func() bool { return bob.out.count("* alice left") == 1 })
	if alice.State() != Invisible {
		t.Errorf("alice state = %v", alice.State())
	}
}

func TestInboundGetsBusyUnlessSeeking(t *testing.T) {
	lan := newLAN()
	alice := startPeer(t, lan, "alice")

	for _, tc := range []struct {
		name string
		to   func()
	}{
		{"idle", alice.Leave},
		{"invisible", alice.Invisible},
	} {
		tc.to()
		conn, err := net.Dial("tcp", alice.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = handshakeDial(conn, protocol.NewID(), "mallory", time.Second)
		conn.Close()
		if !errors.Is(err, errBusy) {
			t.Errorf("%s: dial err = %v, want errBusy", tc.name, err)
		}
		alice.Visible()
		alice.Find()
	}
}

func TestPartnerCrashIsDetected(t *testing.T) {
	lan := newLAN()
	alice := startPeer(t, lan, "alice")
	bob := startPeer(t, lan, "bob")
	eventually(t, "a match", paired(alice, bob))

	// Bob's process dies: the socket closes with no leave message.
	bob.mu.Lock()
	bob.current.conn.Close()
	bob.mu.Unlock()

	eventually(t, "alice to notice", func() bool { return alice.out.count("* bob left") == 1 })
	eventually(t, "alice to look again", func() bool { return alice.State() == Seeking })
}

func TestLeaveThenFind(t *testing.T) {
	lan := newLAN()
	alice := startPeer(t, lan, "alice")
	bob := startPeer(t, lan, "bob")
	eventually(t, "a match", paired(alice, bob))

	alice.Leave()
	if alice.State() != Idle {
		t.Fatalf("after /leave: %v", alice.State())
	}
	carol := startPeer(t, lan, "carol")
	eventually(t, "bob and carol to match", paired(bob, carol))

	dave := startPeer(t, lan, "dave")
	consistently(t, "idle alice staying unmatched", 300*time.Millisecond, func() bool {
		return alice.State() == Idle && dave.State() == Seeking
	})
	alice.Find()
	eventually(t, "alice and dave to match", paired(alice, dave))
}

func TestStopSendsLeave(t *testing.T) {
	lan := newLAN()
	alice := startPeer(t, lan, "alice")
	bob := startPeer(t, lan, "bob")
	eventually(t, "a match", paired(alice, bob))

	alice.Stop()
	eventually(t, "bob to hear alice left", func() bool { return bob.out.count("* alice left") == 1 })
	if len(lan.view().Peers()) != 1 {
		t.Error("alice is still advertised after Stop")
	}
}

func TestSetNick(t *testing.T) {
	lan := newLAN()
	alice := startPeer(t, lan, "alice")
	if err := alice.SetNick("bad nick"); err == nil {
		t.Error("accepted an invalid nick")
	}
	if err := alice.SetNick("zed"); err != nil {
		t.Fatal(err)
	}
	bob := startPeer(t, lan, "bob")
	eventually(t, "bob to meet zed", func() bool { return bob.partner() == "zed" })
}

func TestSendWhileNotChatting(t *testing.T) {
	alice := startPeer(t, newLAN(), "alice")
	alice.Send("hello?")
	if alice.out.count("* you're not in a chat right now") != 1 {
		t.Errorf("output:\n%s", alice.out)
	}
}

func TestSendTooLong(t *testing.T) {
	lan := newLAN()
	alice := startPeer(t, lan, "alice")
	bob := startPeer(t, lan, "bob")
	eventually(t, "a match", paired(alice, bob))
	alice.Send(strings.Repeat("x", protocol.MaxMessageRunes+1))
	if alice.out.count("message too long (161/160)") != 1 {
		t.Errorf("output:\n%s", alice.out)
	}
}

func TestWho(t *testing.T) {
	lan := newLAN()
	alice := startPeer(t, lan, "alice")
	alice.Invisible() // so she doesn't match the fakes below
	lan.advertise(protocol.NewID(), "127.0.0.1:1", discovery.Available)
	lan.advertise(protocol.NewID(), "127.0.0.1:1", discovery.Available)
	lan.advertise(protocol.NewID(), "127.0.0.1:1", discovery.Busy)
	alice.Who()
	if alice.out.count("* 2 people available right now") != 1 {
		t.Errorf("output:\n%s", alice.out)
	}
}

func TestCommandsInTheWrongState(t *testing.T) {
	alice := startPeer(t, newLAN(), "alice")
	alice.Find()
	alice.Next()
	alice.Visible()
	alice.Leave()
	alice.Leave()
	alice.Invisible()
	alice.Invisible()
	alice.Next()
	alice.Find()
	for _, want := range []string{
		"* already looking", "* already visible", "* you're not in a chat; /find",
		"* already invisible", "* you're invisible; /visible",
	} {
		if alice.out.count(want) == 0 {
			t.Errorf("no %q in:\n%s", want, alice.out)
		}
	}
}

func TestPromptFollowsState(t *testing.T) {
	lan := newLAN()
	alice := startPeer(t, lan, "alice")
	prompt := func() string {
		alice.out.mu.Lock()
		defer alice.out.mu.Unlock()
		return alice.out.prompt
	}
	if prompt() != "[looking…] > " {
		t.Errorf("seeking prompt = %q", prompt())
	}
	bob := startPeer(t, lan, "bob")
	eventually(t, "a match", paired(alice, bob))
	if prompt() != "[chatting with bob] > " {
		t.Errorf("chatting prompt = %q", prompt())
	}
	alice.Leave()
	if prompt() != "[idle] > " {
		t.Errorf("idle prompt = %q", prompt())
	}
	alice.Invisible()
	if prompt() != "[invisible] > " {
		t.Errorf("invisible prompt = %q", prompt())
	}
}

func TestNoPeersHint(t *testing.T) {
	out := &testOut{}
	tm := testTimeouts
	tm.HintAfter = 50 * time.Millisecond
	m := New(Config{ID: protocol.NewID(), Nick: "alice", ListenAddr: "127.0.0.1:0",
		Directory: newLAN().view(), Timeouts: tm, Out: out})
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer m.Stop()
	eventually(t, "the hint", func() bool { return out.count(noPeersHint) == 1 })
	time.Sleep(150 * time.Millisecond)
	if n := out.count(noPeersHint); n != 1 {
		t.Errorf("hint printed %d times", n)
	}
}

func TestPendingInboundLimit(t *testing.T) {
	alice := startPeer(t, newLAN(), "alice")
	alice.Invisible()

	var idle []net.Conn
	for i := 0; i < maxPendingInbound; i++ {
		c, err := net.Dial("tcp", alice.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		idle = append(idle, c)
	}
	time.Sleep(50 * time.Millisecond) // let the server take them

	extra, err := net.Dial("tcp", alice.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer extra.Close()
	extra.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	if _, err := extra.Read(make([]byte, 1)); err == nil || isTimeout(err) {
		t.Errorf("connection over the limit wasn't closed: %v", err)
	}

	// The idle ones are dropped at the handshake deadline.
	idle[0].SetReadDeadline(time.Now().Add(3 * testTimeouts.Handshake))
	start := time.Now()
	idle[0].Read(make([]byte, 64)) // our key exchange hello arrives first
	for {
		if _, err := idle[0].Read(make([]byte, 64)); err != nil {
			if isTimeout(err) {
				t.Fatal("idle connection was never dropped")
			}
			break
		}
	}
	if d := time.Since(start); d > 2*testTimeouts.Handshake {
		t.Errorf("idle connection dropped after %v", d)
	}
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

func TestStaticPeerManualMode(t *testing.T) {
	lOut, cOut := &testOut{}, &testOut{}
	listener := New(Config{ID: protocol.NewID(), Nick: "lisa", ListenAddr: "127.0.0.1:0", Timeouts: testTimeouts, Out: lOut})
	if err := listener.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer listener.Stop()
	dialer := New(Config{ID: protocol.NewID(), Nick: "carl", Directory: StaticPeer(listener.Addr().String()),
		Verbose: true, Timeouts: testTimeouts, Out: cOut})
	if err := dialer.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer dialer.Stop()

	eventually(t, "a manual match", func() bool { return listener.State() == Chatting && dialer.State() == Chatting })
	if lOut.count("─── new chat with carl ───") != 1 || cOut.count("─── new chat with lisa ───") != 1 {
		t.Errorf("banners:\n%s\n%s", lOut, cOut)
	}
}

func TestVerboseReportsFailedDial(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close() // nothing listening there now
	out := &testOut{}
	m := New(Config{ID: protocol.NewID(), Nick: "carl", Directory: StaticPeer(addr), Verbose: true, Timeouts: testTimeouts, Out: out})
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer m.Stop()
	eventually(t, "a failure report", func() bool { return out.count("* couldn't connect to "+addr) > 0 })
}

func TestTypingIndicator(t *testing.T) {
	lan := newLAN()
	alice := startPeer(t, lan, "alice")
	bob := startPeer(t, lan, "bob")
	eventually(t, "a match", paired(alice, bob))

	alice.Typing()
	eventually(t, "bob to see alice typing", func() bool { return bob.out.getPrompt() == "[alice is typing…] > " })
	eventually(t, "the indicator to time out", func() bool { return bob.out.getPrompt() == "[chatting with alice] > " })

	alice.Typing()
	eventually(t, "typing again", func() bool { return bob.out.getPrompt() == "[alice is typing…] > " })
	alice.Send("done")
	eventually(t, "the message", func() bool { return bob.out.count("alice: done") == 1 })
	if p := bob.out.getPrompt(); p != "[chatting with alice] > " {
		t.Errorf("a message didn't clear the indicator: %q", p)
	}
}

func TestTypingIsThrottled(t *testing.T) {
	a, b := securePipe(t)
	out := &testOut{}
	m := New(Config{ID: aliceID, Nick: "alice", Timeouts: testTimeouts, Out: out})
	m.current = newConversation(a, "bob", out, testTimeouts, nil)

	frames := make(chan protocol.Type, 16)
	go func() {
		for {
			msg, err := b.ReadMessage()
			if err != nil {
				close(frames)
				return
			}
			frames <- msg.Type
		}
	}()
	count := func(d time.Duration) int {
		n := 0
		deadline := time.After(d)
		for {
			select {
			case typ := <-frames:
				if typ == protocol.TypeTyping {
					n++
				}
			case <-deadline:
				return n
			}
		}
	}

	for i := 0; i < 200; i++ { // a burst of keystrokes, well within TypingEvery
		m.Typing()
	}
	if n := count(30 * time.Millisecond); n != 1 {
		t.Errorf("200 quick keystrokes sent %d typing frames, want 1", n)
	}
	time.Sleep(testTimeouts.TypingEvery)
	m.Typing()
	if n := count(30 * time.Millisecond); n != 1 {
		t.Errorf("a keystroke after TypingEvery sent %d typing frames, want 1", n)
	}
}

func TestBell(t *testing.T) {
	lan := newLAN()
	alice := startPeer(t, lan, "alice")
	bob := startPeer(t, lan, "bob")
	eventually(t, "a match", paired(alice, bob))
	alice.Send("ding")
	eventually(t, "a bell", func() bool { return bob.out.bellCount() == 1 })
	if alice.out.bellCount() != 0 {
		t.Error("the sender's bell rang")
	}
}

func TestNoBell(t *testing.T) {
	lan := newLAN()
	alice := startPeer(t, lan, "alice")
	bob := startPeerWith(t, lan, "bob", func(c *Config) { c.NoBell = true })
	eventually(t, "a match", paired(alice, bob))
	alice.Send("ding")
	eventually(t, "the message", func() bool { return bob.out.count("alice: ding") == 1 })
	if bob.out.bellCount() != 0 {
		t.Error("bell rang with NoBell")
	}
}
