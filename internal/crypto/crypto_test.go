package crypto

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sw4p/termchat/internal/protocol"
)

// handshakePair runs Handshake on both ends of a and b at once.
func handshakePair(t *testing.T, a, b net.Conn) (*SecureConn, *SecureConn) {
	t.Helper()
	type result struct {
		sc  *SecureConn
		err error
	}
	ch := make(chan result, 1)
	go func() {
		sc, err := Handshake(b)
		ch <- result{sc, err}
	}()
	sa, err := Handshake(a)
	if err != nil {
		t.Fatalf("handshake a: %v", err)
	}
	r := <-ch
	if r.err != nil {
		t.Fatalf("handshake b: %v", r.err)
	}
	return sa, r.sc
}

// wire records every byte a relay forwards.
type wire struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *wire) record(p []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf.Write(p)
}

func (w *wire) Bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return bytes.Clone(w.buf.Bytes())
}

// hook sees the i-th frame after the handshake and returns the frames to
// forward in its place: nil drops it, two frames duplicate or reorder.
type hook func(i int, frame []byte) [][]byte

// relay copies src to dst: the hello bytes as they are, then frame by frame
// through h. It plays an attacker sitting on the LAN.
func relay(src, dst net.Conn, h hook, w *wire) {
	defer dst.Close()
	hello := make([]byte, helloLen)
	if _, err := io.ReadFull(src, hello); err != nil {
		return
	}
	w.record(hello)
	if _, err := dst.Write(hello); err != nil {
		return
	}
	for i := 0; ; i++ {
		f, err := protocol.ReadFrame(src)
		if err != nil {
			return
		}
		w.record(f)
		out := [][]byte{f}
		if h != nil {
			out = h(i, f)
		}
		for _, o := range out {
			if err := protocol.WriteFrame(dst, o); err != nil {
				return
			}
		}
	}
}

// proxiedPair connects alice and bob through a relay in each direction.
// ab tampers with alice→bob frames. The returned wire holds alice→bob bytes.
func proxiedPair(t *testing.T, ab hook) (alice, bob *SecureConn, w *wire) {
	t.Helper()
	a, aRelay := net.Pipe()
	bRelay, b := net.Pipe()
	w = &wire{}
	go relay(aRelay, bRelay, ab, w)
	go relay(bRelay, aRelay, nil, &wire{})
	alice, bob = handshakePair(t, a, b)
	t.Cleanup(func() { alice.Close(); bob.Close() })
	return alice, bob, w
}

// sendAsync writes payloads in the background, since net.Pipe writes block
// until the other side reads.
func sendAsync(sc *SecureConn, payloads ...string) {
	go func() {
		for _, p := range payloads {
			if sc.WritePayload([]byte(p)) != nil {
				return
			}
		}
	}()
}

func TestRoundTripBothWays(t *testing.T) {
	a, b := net.Pipe()
	alice, bob := handshakePair(t, a, b)
	defer alice.Close()
	defer bob.Close()

	for i := 0; i < 50; i++ {
		from, to := alice, bob
		if i%2 == 1 {
			from, to = bob, alice
		}
		want := protocol.Message{Type: protocol.TypeMsg, Text: fmt.Sprintf("message %d 👋", i)}
		go from.WriteMessage(want)
		got, err := to.ReadMessage()
		if err != nil {
			t.Fatalf("message %d: %v", i, err)
		}
		if got != want {
			t.Fatalf("message %d: got %+v, want %+v", i, got, want)
		}
	}
}

func TestNoPlaintextOnWire(t *testing.T) {
	alice, bob, w := proxiedPair(t, nil)
	const secret = "meet me by the vending machine"
	go alice.WriteMessage(protocol.Message{Type: protocol.TypeMsg, Text: secret})
	if _, err := bob.ReadMessage(); err != nil {
		t.Fatal(err)
	}
	wire := w.Bytes()
	for _, s := range []string{secret, "vending", `"msg"`, `"text"`} {
		if bytes.Contains(wire, []byte(s)) {
			t.Errorf("wire contains plaintext %q", s)
		}
	}
	if !bytes.HasPrefix(wire, []byte(Magic)) {
		t.Errorf("wire doesn't start with the %q magic", Magic)
	}
}

func TestFreshKeysEverySession(t *testing.T) {
	ciphertext := func() []byte {
		alice, bob, w := proxiedPair(t, nil)
		sendAsync(alice, "same words")
		if _, err := bob.ReadPayload(); err != nil {
			t.Fatal(err)
		}
		return w.Bytes()[helloLen:]
	}
	if c1, c2 := ciphertext(), ciphertext(); bytes.Equal(c1, c2) {
		t.Error("two sessions produced identical ciphertext for the same message")
	}
}

func TestTamperedFrameFails(t *testing.T) {
	flip := func(i int, f []byte) [][]byte {
		if i == 0 {
			f = bytes.Clone(f)
			f[3] ^= 0x01
		}
		return [][]byte{f}
	}
	alice, bob, _ := proxiedPair(t, flip)
	sendAsync(alice, "pay alice $5")

	_, err := bob.ReadPayload()
	if !errors.Is(err, ErrAuth) || !protocol.IsFatal(err) {
		t.Errorf("err = %v (fatal=%v), want fatal ErrAuth", err, protocol.IsFatal(err))
	}
}

func TestReplayedFrameFails(t *testing.T) {
	dup := func(i int, f []byte) [][]byte { return [][]byte{f, f} }
	alice, bob, _ := proxiedPair(t, dup)
	sendAsync(alice, "once")

	if p, err := bob.ReadPayload(); err != nil || string(p) != "once" {
		t.Fatalf("first read = %q, %v", p, err)
	}
	if _, err := bob.ReadPayload(); !errors.Is(err, ErrAuth) {
		t.Errorf("replayed frame: err = %v, want ErrAuth", err)
	}
}

func TestReorderedFramesFail(t *testing.T) {
	var held []byte
	swap := func(i int, f []byte) [][]byte {
		if i == 0 {
			held = f
			return nil
		}
		if i == 1 {
			return [][]byte{f, held}
		}
		return [][]byte{f}
	}
	alice, bob, _ := proxiedPair(t, swap)
	sendAsync(alice, "first", "second")

	if _, err := bob.ReadPayload(); !errors.Is(err, ErrAuth) {
		t.Errorf("out-of-order frame: err = %v, want ErrAuth", err)
	}
}

func TestDroppedFrameFails(t *testing.T) {
	drop := func(i int, f []byte) [][]byte {
		if i == 0 {
			return nil
		}
		return [][]byte{f}
	}
	alice, bob, _ := proxiedPair(t, drop)
	sendAsync(alice, "first", "second")

	if _, err := bob.ReadPayload(); !errors.Is(err, ErrAuth) {
		t.Errorf("frame after a dropped one: err = %v, want ErrAuth", err)
	}
}

// fakePeer reads our hello from conn and answers with reply(ourHello).
func fakePeer(conn net.Conn, reply func(ours []byte) []byte) {
	go func() {
		ours := make([]byte, helloLen)
		if _, err := io.ReadFull(conn, ours); err != nil {
			return
		}
		conn.Write(reply(ours))
	}()
}

func TestHandshakeRejects(t *testing.T) {
	tests := []struct {
		name  string
		reply func(ours []byte) []byte
		want  error // nil means "any error"
	}{
		{"http client", func([]byte) []byte {
			return []byte("GET / HTTP/1.1\r\nHost: x\r\n\r\n" + strings.Repeat(" ", 10))
		}, ErrBadMagic},
		{"newer major version", func(ours []byte) []byte {
			b := bytes.Clone(ours)
			b[len(Magic)] = Version + 1
			b[len(b)-1] ^= 0xff // a different key, so it isn't a reflection
			return b
		}, ErrVersion},
		{"our own key echoed back", func(ours []byte) []byte {
			return ours
		}, ErrReflected},
		{"all-zero key", func([]byte) []byte {
			return append([]byte(Magic+"\x01"), make([]byte, 32)...)
		}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a, b := net.Pipe()
			defer a.Close()
			defer b.Close()
			fakePeer(b, tc.reply)
			sc, err := Handshake(a)
			if err == nil {
				t.Fatalf("Handshake succeeded: %+v", sc)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestHandshakeHonoursDeadline(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	a.SetDeadline(time.Now().Add(50 * time.Millisecond))
	go io.Copy(io.Discard, b) // b reads our hello but never answers

	start := time.Now()
	if _, err := Handshake(a); err == nil {
		t.Fatal("handshake with a silent peer succeeded")
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("took %v to give up", d)
	}
}

func TestPayloadSizeLimit(t *testing.T) {
	a, b := net.Pipe()
	alice, bob := handshakePair(t, a, b)
	defer alice.Close()
	defer bob.Close()

	if err := alice.WritePayload(make([]byte, MaxPayload+1)); !errors.Is(err, protocol.ErrFrameTooLarge) {
		t.Errorf("oversized payload: err = %v, want ErrFrameTooLarge", err)
	}
	// A rejected payload must not use up a nonce, or the next frame would fail.
	want := bytes.Repeat([]byte{'x'}, MaxPayload)
	go alice.WritePayload(want)
	got, err := bob.ReadPayload()
	if err != nil || !bytes.Equal(got, want) {
		t.Errorf("max-size payload: %d bytes, err = %v", len(got), err)
	}
}

func TestConcurrentWriters(t *testing.T) {
	a, b := net.Pipe()
	alice, bob := handshakePair(t, a, b)
	defer alice.Close()
	defer bob.Close()

	const writers, each = 8, 25
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				alice.WriteMessage(protocol.Message{Type: protocol.TypeMsg, Text: fmt.Sprintf("w%d-%d", w, i)})
			}
		}()
	}
	seen := map[string]bool{}
	for i := 0; i < writers*each; i++ {
		m, err := bob.ReadMessage()
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		seen[m.Text] = true
	}
	wg.Wait()
	if len(seen) != writers*each {
		t.Errorf("got %d distinct messages, want %d", len(seen), writers*each)
	}
}
