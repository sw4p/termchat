// Package session is termchat's brain: the state machine that decides who
// you're chatting with.
//
//	          /find, /visible            match (inbound or outbound)
//	IDLE ───────────────────▶ SEEKING ─────────────────────────▶ CHATTING
//	 ▲  ◀─────────────────────   ▲   ◀──── partner left, /next ──────┘
//	 │          /leave           │
//	 └──────── /leave ───────────┼────────────────────────────────────┘
//	                             │ /visible
//	INVISIBLE ◀── /invisible (from any state; leaves the chat) ──
//
// One mutex guards every transition. The Manager runs two loops: an accept
// loop answering inbound hellos, and a seek loop dialing random available
// peers while SEEKING. It never touches the terminal directly. It prints
// through Output, and the program calls its methods (Next, Leave, …) for
// the user's commands.
package session

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/sw4p/termchat/internal/crypto"
	"github.com/sw4p/termchat/internal/discovery"
	"github.com/sw4p/termchat/internal/nick"
	"github.com/sw4p/termchat/internal/protocol"
)

// State is where the session is in its lifecycle. Go has no enum keyword;
// a named integer type plus iota constants is the idiom.
type State int

const (
	Idle      State = iota // not in a chat, not looking; advertised busy
	Seeking                // looking for a partner; advertised available
	Chatting               // in a chat; advertised busy
	Invisible              // not advertised at all; every hello gets busy
)

func (s State) String() string {
	switch s {
	case Idle:
		return "idle"
	case Seeking:
		return "seeking"
	case Chatting:
		return "chatting"
	case Invisible:
		return "invisible"
	}
	return fmt.Sprintf("State(%d)", int(s))
}

// Output is where the session prints. *ui.Console provides it.
type Output interface {
	Println(text string)
	// Message prints a chat line from name; self is true for our own.
	Message(name string, self bool, text string)
	SetPrompt(p string)
	// Banner prints a rule with "text name" set in it, for a new chat.
	Banner(text, name string)
	// SetStatus sets the dim hint after the empty prompt and a note on the
	// rule above it (for example, that the partner is typing).
	SetStatus(hint, note string)
	Bell()
}

// Directory lists the peers on the network. *discovery.Browser provides it.
type Directory interface {
	Peers() []discovery.Peer
	Updates() <-chan struct{}
}

// Advertiser announces us on the network. *discovery.Advertiser provides it.
type Advertiser interface {
	SetState(discovery.State)
	Close()
}

// Timeouts are grouped so tests can shrink them all to milliseconds.
type Timeouts struct {
	Handshake time.Duration // key exchange + hello/accept must finish within this
	Ping      time.Duration // send a ping this often during a chat
	Idle      time.Duration // hang up if nothing arrives for this long
	Dial      time.Duration // TCP connect timeout per address
	RetryMin  time.Duration // random pause between match attempts:
	RetryMax  time.Duration //   somewhere in [RetryMin, RetryMax)
	RetryPeer time.Duration // don't redial a peer that failed or was busy for this long
	HintAfter time.Duration // print the "no peers found" hint after this long alone (0: never)

	TypingEvery time.Duration // send a typing frame at most this often
	TypingClear time.Duration // "X is typing…" disappears this long after the last one
}

// DefaultTimeouts are the values termchat runs with.
var DefaultTimeouts = Timeouts{
	Handshake: 5 * time.Second,
	Ping:      10 * time.Second,
	Idle:      30 * time.Second,
	Dial:      2 * time.Second,
	RetryMin:  500 * time.Millisecond,
	RetryMax:  2 * time.Second,
	RetryPeer: 5 * time.Second,
	HintAfter: 60 * time.Second,

	TypingEvery: 2 * time.Second,
	TypingClear: 3 * time.Second,
}

// maxPendingInbound caps connections that haven't finished the handshake
// yet, so a flood of idle connections can't pile up goroutines.
const maxPendingInbound = 4

// noPeersHint is printed once if nobody shows up for Timeouts.HintAfter.
const noPeersHint = "* no peers found — your network may block multicast; try termchat --connect <ip:port>"

// Config describes one session.
type Config struct {
	ID   string // our random session id
	Nick string

	// ListenAddr is where to accept connections, e.g. ":0" for any port.
	// Empty means don't listen (manual --connect mode).
	ListenAddr string
	// Directory lists peers to dial. Nil means never dial (manual --listen).
	Directory Directory
	// Advertise starts announcing us on port with the given state. Nil
	// means don't advertise (manual modes).
	Advertise func(port int, state discovery.State) (Advertiser, error)
	// Verbose prints failed connection attempts. Useful with --connect,
	// noise with discovery.
	Verbose bool
	// NoBell turns off the terminal bell on incoming messages.
	NoBell bool

	Timeouts Timeouts
	Out      Output
}

// Manager runs one session. Create it with New, then Start it.
type Manager struct {
	id        string
	tm        Timeouts
	out       Output
	dir       Directory
	advertise func(int, discovery.State) (Advertiser, error)
	verbose   bool
	bell      bool

	listenAddr string
	ln         net.Listener
	port       int
	wake       chan struct{} // nudges the seek loop after a state change
	slots      chan struct{} // semaphore for inbound handshakes
	cancel     context.CancelFunc
	wg         sync.WaitGroup

	mu          sync.Mutex // guards everything below
	nick        string
	state       State
	dialing     bool // an outbound attempt is in progress
	current     *conversation
	typing      bool            // the partner is typing
	lastTyping  time.Time       // when we last sent a typing frame
	seen        map[string]bool // ids we've chatted with: never matched again
	retryAfter  map[string]time.Time
	adv         Advertiser
	stopped     bool
	hinted      bool
	lonelySince time.Time
}

// New returns a Manager for cfg. Nothing runs until Start.
func New(cfg Config) *Manager {
	return &Manager{
		id:         cfg.ID,
		tm:         cfg.Timeouts,
		out:        cfg.Out,
		dir:        cfg.Directory,
		advertise:  cfg.Advertise,
		verbose:    cfg.Verbose,
		bell:       !cfg.NoBell,
		listenAddr: cfg.ListenAddr,
		nick:       cfg.Nick,
		wake:       make(chan struct{}, 1),
		slots:      make(chan struct{}, maxPendingInbound),
		seen:       map[string]bool{},
		retryAfter: map[string]time.Time{},
	}
}

// Start opens the listener, starts advertising and begins seeking.
func (m *Manager) Start(ctx context.Context) error {
	if m.listenAddr != "" {
		ln, err := net.Listen("tcp", m.listenAddr)
		if err != nil {
			return err
		}
		m.ln = ln
		m.port = ln.Addr().(*net.TCPAddr).Port
	}
	if m.advertise != nil && m.ln != nil {
		adv, err := m.advertise(m.port, discovery.Available)
		if err != nil {
			m.ln.Close()
			return fmt.Errorf("advertising on the LAN: %w", err)
		}
		m.adv = adv
	}

	ctx, m.cancel = context.WithCancel(ctx)
	m.mu.Lock()
	m.lonelySince = time.Now()
	m.setState(Seeking)
	m.mu.Unlock()

	if m.ln != nil {
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			m.acceptLoop(ctx)
		}()
	}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.seekLoop(ctx)
	}()
	return nil
}

// Stop leaves any chat politely, stops advertising and shuts everything
// down. It's safe to call more than once.
func (m *Manager) Stop() {
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return
	}
	m.stopped = true
	c := m.current
	m.current = nil
	if m.adv != nil {
		m.adv.Close()
		m.adv = nil
	}
	m.mu.Unlock()

	if c != nil {
		c.leave()
	}
	if m.cancel != nil {
		m.cancel()
	}
	if m.ln != nil {
		m.ln.Close()
	}
	m.wg.Wait()
}

// Addr returns the address we accept connections on, or nil.
func (m *Manager) Addr() net.Addr {
	if m.ln == nil {
		return nil
	}
	return m.ln.Addr()
}

// State returns the current state.
func (m *Manager) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

// setState moves to st and updates everything that mirrors the state: the
// advertisement, the prompt, and the seek loop. m.mu must be held.
func (m *Manager) setState(st State) {
	m.state = st
	switch {
	case st == Invisible && m.adv != nil:
		m.adv.Close()
		m.adv = nil
	case st == Seeking && m.adv != nil:
		m.adv.SetState(discovery.Available)
	case m.adv != nil:
		m.adv.SetState(discovery.Busy)
	}
	m.out.SetPrompt(m.prompt())
	m.out.SetStatus(m.hint())
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *Manager) prompt() string { return "[" + m.status() + "] > " }

// hint is what the prompt area shows besides the prompt: a dim hint for the
// empty input line, and a note for the rule above it.
func (m *Manager) hint() (hint, note string) {
	switch m.state {
	case Seeking:
		return "looking for someone…", ""
	case Chatting:
		if m.current != nil {
			if m.typing {
				note = m.current.partner + " is typing…"
			}
			return "to " + m.current.partner, note
		}
	case Invisible:
		return "invisible · /visible to come back", ""
	}
	return "/find to look for someone", ""
}

// status says what the user is doing: looking, chatting, and so on.
func (m *Manager) status() string {
	switch m.state {
	case Seeking:
		return "looking…"
	case Chatting:
		if m.current != nil && m.typing {
			return fmt.Sprintf("%s is typing…", m.current.partner)
		}
		if m.current != nil {
			return fmt.Sprintf("chatting with %s", m.current.partner)
		}
	case Invisible:
		return "invisible"
	}
	return "idle"
}

// begin starts a chat with partner over sc. m.mu must be held.
func (m *Manager) begin(sc *crypto.SecureConn, partner protocol.Message) {
	c := newConversation(sc, partner.Nick, m.out, m.tm, m.onPartnerLeft)
	c.onTyping = m.onTyping
	c.bell = m.bell
	m.current = c
	m.typing = false
	m.lastTyping = time.Time{}
	// Record the partner now rather than when the chat ends: the effect is
	// the same (never rematched), and it also blocks a second connection
	// from them while this chat is running.
	m.seen[partner.ID] = true
	m.setState(Chatting)
	c.start()
}

// onPartnerLeft runs when the partner ends the chat. We go back to looking.
func (m *Manager) onPartnerLeft(c *conversation) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current != c || m.stopped {
		return // we'd already moved on (e.g. /next raced with their leave)
	}
	m.current = nil
	m.typing = false
	m.setState(Seeking)
	m.out.Println("* looking for someone new…")
}

// onTyping runs when the partner starts or stops typing.
func (m *Manager) onTyping(c *conversation, on bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current != c || m.typing == on {
		return
	}
	m.typing = on
	m.out.SetPrompt(m.prompt())
	m.out.SetStatus(m.hint())
}

// Typing tells the partner we're typing. Call it on every keystroke: it
// sends at most one typing frame per Timeouts.TypingEvery.
func (m *Manager) Typing() {
	m.mu.Lock()
	c := m.current
	now := time.Now()
	due := c != nil && now.Sub(m.lastTyping) >= m.tm.TypingEvery
	if due {
		m.lastTyping = now
	}
	m.mu.Unlock()
	if due {
		c.send(protocol.Message{Type: protocol.TypeTyping})
	}
}

// leaveChat ends the current chat, if any, and then moves to state st.
func (m *Manager) leaveChat(st State) {
	m.mu.Lock()
	c := m.current
	m.current = nil // state stays Chatting meanwhile, so inbound hellos get busy
	m.typing = false
	m.mu.Unlock()

	if c != nil {
		c.leave() // network I/O: never while holding m.mu
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.stopped {
		m.setState(st)
	}
}

// ─── Commands ───────────────────────────────────────────────────────────────

// Send sends a chat message to the current partner.
func (m *Manager) Send(text string) {
	m.mu.Lock()
	c := m.current
	m.mu.Unlock()
	if c == nil {
		m.out.Println("* you're not in a chat right now")
		return
	}
	if n := utf8.RuneCountInString(text); n > protocol.MaxMessageRunes {
		m.out.Println(fmt.Sprintf("* message too long (%d/%d), not sent", n, protocol.MaxMessageRunes))
		return
	}
	// If the send fails, the connection is broken and receive will end the
	// chat, so there's nothing else to do here.
	if c.send(protocol.Message{Type: protocol.TypeMsg, Text: text}) == nil {
		m.out.Message("you", true, text)
	}
	// The partner's indicator clears when the message arrives, so the next
	// keystroke should announce typing again straight away.
	m.mu.Lock()
	m.lastTyping = time.Time{}
	m.mu.Unlock()
}

// Next leaves the current chat (if any) and looks for someone new.
func (m *Manager) Next() {
	switch m.State() {
	case Invisible:
		m.out.Println("* you're invisible; /visible to be matched again")
		return
	case Seeking:
		m.out.Println("* already looking")
		return
	}
	m.leaveChat(Seeking)
	m.out.Println("* looking for someone new…")
}

// Leave leaves the current chat (or stops looking) and goes idle.
func (m *Manager) Leave() {
	switch m.State() {
	case Idle:
		m.out.Println("* you're not in a chat; /find to look for someone")
		return
	case Invisible:
		m.out.Println("* you're invisible; /visible to be matched again")
		return
	}
	m.leaveChat(Idle)
	m.out.Println("* idle. /find to look for someone")
}

// Find starts looking for a partner.
func (m *Manager) Find() {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch m.state {
	case Seeking:
		m.out.Println("* already looking")
	case Chatting:
		m.out.Println("* you're in a chat; /next to skip to someone else")
	case Invisible:
		m.out.Println("* you're invisible; /visible to be matched again")
	case Idle:
		m.setState(Seeking)
		m.out.Println("* looking for someone…")
	}
}

// Invisible stops advertising and refuses matches, leaving any chat.
func (m *Manager) Invisible() {
	if m.State() == Invisible {
		m.out.Println("* already invisible")
		return
	}
	m.leaveChat(Invisible)
	m.out.Println("* you're invisible: nobody can find you. /visible to come back")
}

// Visible starts advertising again and looks for a partner.
func (m *Manager) Visible() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state != Invisible {
		m.out.Println("* already visible")
		return
	}
	if m.advertise != nil && m.ln != nil {
		adv, err := m.advertise(m.port, discovery.Available)
		if err != nil {
			m.out.Println(fmt.Sprintf("* couldn't advertise on the LAN: %v", err))
		} else {
			m.adv = adv
		}
	}
	m.setState(Seeking)
	m.out.Println("* you're visible again. looking for someone…")
}

// SetNick changes the nick used from the next chat on.
func (m *Manager) SetNick(name string) error {
	if !nick.Valid(name) {
		return fmt.Errorf("invalid nick: use 1-%d letters, digits, '-' or '_'", nick.MaxLen)
	}
	m.mu.Lock()
	m.nick = name
	chatting := m.current != nil
	m.mu.Unlock()
	if chatting {
		m.out.Println("* you're now " + name + " (from your next chat on)")
	} else {
		m.out.Println("* you're now " + name)
	}
	return nil
}

// Who prints how many peers are available right now.
func (m *Manager) Who() {
	if m.dir == nil {
		m.out.Println("* /who needs LAN discovery (not available with --listen/--connect)")
		return
	}
	m.mu.Lock()
	available, met := 0, 0
	for _, p := range m.dir.Peers() {
		if p.State != discovery.Available || p.ID == m.id {
			continue
		}
		available++
		if m.seen[p.ID] {
			met++
		}
	}
	m.mu.Unlock()
	switch {
	case available == 1:
		m.out.Println(fmt.Sprintf("* 1 person available right now%s", metSuffix(met)))
	default:
		m.out.Println(fmt.Sprintf("* %d people available right now%s", available, metSuffix(met)))
	}
}

func metSuffix(met int) string {
	if met == 0 {
		return ""
	}
	return fmt.Sprintf(" (%d you've already met)", met)
}

// ─── Inbound ────────────────────────────────────────────────────────────────

func (m *Manager) acceptLoop(ctx context.Context) {
	for {
		conn, err := m.ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return
			}
			time.Sleep(50 * time.Millisecond) // e.g. out of file descriptors
			continue
		}
		select {
		case m.slots <- struct{}{}:
			go func() {
				defer func() { <-m.slots }()
				m.handleInbound(conn)
			}()
		default:
			conn.Close() // too many handshakes in flight
		}
	}
}

// handleInbound answers one incoming connection: accept if we're seeking
// and free, busy otherwise.
func (m *Manager) handleInbound(conn net.Conn) {
	sc, hello, err := readHello(conn, m.id, m.tm.Handshake)
	if err != nil {
		conn.Close()
		return
	}

	m.mu.Lock()
	// While we're dialing someone ourselves, say busy. If two peers dial
	// each other at the same moment, both get busy and retry after a random
	// pause, so we never end up with two half-open chats.
	if m.state == Seeking && !m.dialing && !m.stopped && !m.seen[hello.ID] {
		// Reply and start the chat under the lock, so no other connection
		// can match us in between. The write is bounded by the handshake
		// deadline, which is still set.
		err := sc.WriteMessage(protocol.Message{Type: protocol.TypeAccept, ID: m.id, Nick: m.nick})
		if err == nil {
			conn.SetDeadline(time.Time{})
			m.begin(sc, hello)
		}
		m.mu.Unlock()
		if err != nil {
			conn.Close()
		}
		return
	}
	m.mu.Unlock()

	sc.WriteMessage(protocol.Message{Type: protocol.TypeBusy})
	conn.Close()
}

// ─── Outbound ───────────────────────────────────────────────────────────────

func (m *Manager) seekLoop(ctx context.Context) {
	for ctx.Err() == nil {
		peer, ok := m.nextTarget()
		if !ok {
			m.pause(ctx, m.jitter())
			continue
		}
		m.attempt(ctx, peer)
		m.pause(ctx, m.jitter())
	}
}

func (m *Manager) jitter() time.Duration {
	span := m.tm.RetryMax - m.tm.RetryMin
	if span <= 0 {
		return m.tm.RetryMin
	}
	return m.tm.RetryMin + rand.N(span)
}

// pause waits for d, or less if something worth reacting to happens.
func (m *Manager) pause(ctx context.Context, d time.Duration) {
	var updates <-chan struct{}
	if m.dir != nil {
		updates = m.dir.Updates()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-m.wake:
	case <-updates:
	case <-t.C:
	}
}

// nextTarget picks a random peer to dial and marks us as dialing. It
// returns false if we aren't seeking or nobody is eligible.
func (m *Manager) nextTarget() (discovery.Peer, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state != Seeking || m.stopped || m.dir == nil {
		return discovery.Peer{}, false
	}
	now := time.Now()
	peers := m.dir.Peers()
	m.maybeHint(now, len(peers))

	for k, until := range m.retryAfter {
		if now.After(until) {
			delete(m.retryAfter, k)
		}
	}
	var eligible []discovery.Peer
	for _, p := range peers {
		if p.State != discovery.Available || p.ID == m.id || m.seen[p.ID] || len(p.Addrs) == 0 {
			continue
		}
		if _, waiting := m.retryAfter[targetKey(p)]; waiting {
			continue
		}
		eligible = append(eligible, p)
	}
	if len(eligible) == 0 {
		return discovery.Peer{}, false
	}
	m.dialing = true
	return eligible[rand.N(len(eligible))], true
}

// targetKey identifies a peer for retry bookkeeping. A --connect target has
// no id until we reach it, so its address stands in.
func targetKey(p discovery.Peer) string {
	if p.ID != "" {
		return p.ID
	}
	return p.Addrs[0]
}

// maybeHint prints noPeersHint once if discovery has found nobody at all for
// HintAfter. m.mu must be held.
func (m *Manager) maybeHint(now time.Time, peers int) {
	if peers > 0 {
		m.lonelySince = now
		return
	}
	if m.tm.HintAfter > 0 && !m.hinted && now.Sub(m.lonelySince) >= m.tm.HintAfter {
		m.hinted = true
		m.out.Println(noPeersHint)
	}
}

// attempt dials peer, and starts a chat if they accept and we're still
// seeking.
func (m *Manager) attempt(ctx context.Context, peer discovery.Peer) {
	m.mu.Lock()
	myNick := m.nick
	m.mu.Unlock()

	sc, partner, err := m.dial(ctx, peer.Addrs, myNick)

	m.mu.Lock()
	m.dialing = false
	if err != nil {
		m.retryAfter[targetKey(peer)] = time.Now().Add(m.tm.RetryPeer)
		m.mu.Unlock()
		if m.verbose && ctx.Err() == nil {
			m.out.Println(fmt.Sprintf("* couldn't connect to %s: %v", peer.Addrs[0], err))
		}
		return
	}
	if m.state != Seeking || m.stopped || m.seen[partner.ID] {
		// The user moved on while we were dialing (/leave, /invisible,
		// quit), or it turned out to be someone we've met. Say goodbye.
		m.mu.Unlock()
		sc.SetWriteDeadline(time.Now().Add(m.tm.Handshake))
		sc.WriteMessage(protocol.Message{Type: protocol.TypeLeave})
		sc.Close()
		return
	}
	m.begin(sc, partner)
	m.mu.Unlock()
}

// dial connects to the first address that answers and runs the handshake.
// Cancelling ctx aborts it at once.
func (m *Manager) dial(ctx context.Context, addrs []string, myNick string) (*crypto.SecureConn, protocol.Message, error) {
	var lastErr error
	for _, addr := range addrs {
		d := net.Dialer{Timeout: m.tm.Dial}
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			lastErr = err
			continue
		}
		stop := context.AfterFunc(ctx, func() { conn.Close() })
		sc, partner, err := handshakeDial(conn, m.id, myNick, m.tm.Handshake)
		if !stop() {
			conn.Close() // ctx was cancelled mid-handshake
			return nil, protocol.Message{}, ctx.Err()
		}
		if err != nil {
			// We reached a termchat peer and it answered (busy, or an
			// error). Another address won't change its mind.
			conn.Close()
			return nil, protocol.Message{}, err
		}
		return sc, partner, nil
	}
	return nil, protocol.Message{}, lastErr
}

// StaticPeer is a Directory holding one fixed address, for --connect.
func StaticPeer(addr string) Directory { return staticPeer{addr} }

type staticPeer struct{ addr string }

func (s staticPeer) Peers() []discovery.Peer {
	return []discovery.Peer{{State: discovery.Available, Addrs: []string{s.addr}}}
}

func (s staticPeer) Updates() <-chan struct{} { return nil }
