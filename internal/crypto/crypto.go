// Package crypto encrypts a termchat connection end to end.
//
// Both peers send a fresh X25519 public key, derive two traffic keys (one per
// direction) from the shared secret with HKDF-SHA256, and from then on every
// frame is sealed with ChaCha20-Poly1305 under a counter nonce.
//
// This protects against passive eavesdroppers on the LAN. There is no
// identity authentication (termchat is anonymous), so an active
// man-in-the-middle on the LAN is out of scope.
package crypto

import (
	"bytes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"net"
	"sync"
	"time"

	"golang.org/x/crypto/chacha20poly1305"

	"github.com/sw4p/termchat/internal/protocol"
)

const (
	// Magic starts every handshake so non-termchat traffic is rejected at once.
	Magic = "TCHT"
	// Version is the protocol major version. Different majors never talk.
	Version = protocol.Version

	helloLen = len(Magic) + 1 + 32 // magic, version, X25519 public key

	// Overhead is how many bytes sealing adds to a payload (the Poly1305 tag).
	Overhead = chacha20poly1305.Overhead
	// MaxPayload is the largest plaintext that fits in one frame.
	MaxPayload = protocol.MaxFrameSize - Overhead

	kdfInfo = "termchat v1 traffic keys"
)

var (
	ErrBadMagic  = errors.New("crypto: not a termchat peer")
	ErrVersion   = errors.New("crypto: incompatible termchat version")
	ErrReflected = errors.New("crypto: peer sent back our own key")
	ErrAuth      = errors.New("crypto: frame failed authentication")
	errExhausted = errors.New("crypto: nonce counter exhausted")
)

// SecureConn is an encrypted, message-oriented connection. It wraps a
// net.Conn rather than embedding it, so the raw Read and Write can't be
// called by mistake and bypass encryption.
//
// WritePayload and WriteMessage are safe to call from several goroutines.
// ReadPayload and ReadMessage must be called from one goroutine at a time.
type SecureConn struct {
	conn net.Conn

	sendMu  sync.Mutex
	send    cipher.AEAD
	sendCtr uint64

	recv    cipher.AEAD
	recvCtr uint64
}

// Handshake exchanges keys over conn and returns the encrypted connection.
// Both sides call it; there are no client/server roles. Set a deadline on
// conn first: Handshake waits as long as the peer does.
func Handshake(conn net.Conn) (*SecureConn, error) {
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	ours := make([]byte, 0, helloLen)
	ours = append(ours, Magic...)
	ours = append(ours, Version)
	ours = append(ours, priv.PublicKey().Bytes()...)

	// Both sides write first, so write in the background while reading.
	// Otherwise two peers on an unbuffered connection (like net.Pipe) would
	// each block in Write waiting for the other to Read.
	writeErr := make(chan error, 1)
	go func() {
		_, err := conn.Write(ours)
		writeErr <- err
	}()
	theirs := make([]byte, helloLen)
	if _, err := io.ReadFull(conn, theirs); err != nil {
		return nil, err
	}
	if err := <-writeErr; err != nil {
		return nil, err
	}

	if string(theirs[:len(Magic)]) != Magic {
		return nil, ErrBadMagic
	}
	if theirs[len(Magic)] != Version {
		return nil, ErrVersion
	}
	if bytes.Equal(theirs, ours) {
		return nil, ErrReflected
	}
	peerPub, err := ecdh.X25519().NewPublicKey(theirs[len(Magic)+1:])
	if err != nil {
		return nil, err
	}
	// ECDH fails on low-order public keys that would give an all-zero secret.
	shared, err := priv.ECDH(peerPub)
	if err != nil {
		return nil, err
	}

	// Both sides must derive the same keys, so put the two hellos in a fixed
	// order: lower bytes first. The hellos go into the salt, so the keys are
	// bound to exactly what was sent.
	weAreFirst := bytes.Compare(ours, theirs) < 0
	transcript := make([]byte, 0, 2*helloLen)
	if weAreFirst {
		transcript = append(append(transcript, ours...), theirs...)
	} else {
		transcript = append(append(transcript, theirs...), ours...)
	}
	keys, err := hkdf.Key(sha256.New, shared, transcript, kdfInfo, 2*chacha20poly1305.KeySize)
	if err != nil {
		return nil, err
	}
	// Separate keys per direction, so the two counters starting at 0 never
	// produce the same (key, nonce) pair.
	sendKey, recvKey := keys[:32], keys[32:]
	if !weAreFirst {
		sendKey, recvKey = recvKey, sendKey
	}

	sc := &SecureConn{conn: conn}
	if sc.send, err = chacha20poly1305.New(sendKey); err != nil {
		return nil, err
	}
	if sc.recv, err = chacha20poly1305.New(recvKey); err != nil {
		return nil, err
	}
	return sc, nil
}

// nonce turns a frame counter into a 12-byte ChaCha20-Poly1305 nonce.
func nonce(ctr uint64) []byte {
	var n [chacha20poly1305.NonceSize]byte
	binary.BigEndian.PutUint64(n[4:], ctr)
	return n[:]
}

// WritePayload seals p and sends it as one frame.
func (c *SecureConn) WritePayload(p []byte) error {
	if len(p) > MaxPayload {
		return protocol.ErrFrameTooLarge
	}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if c.sendCtr == math.MaxUint64 {
		return errExhausted
	}
	ct := c.send.Seal(nil, nonce(c.sendCtr), p, nil)
	// Advance even if the write fails: a nonce must never be used twice.
	c.sendCtr++
	return protocol.WriteFrame(c.conn, ct)
}

// ReadPayload reads one frame and decrypts it. Every error is a
// *protocol.StreamError: a frame that fails authentication has been tampered
// with, replayed or reordered, so the connection can't be trusted any more.
func (c *SecureConn) ReadPayload() ([]byte, error) {
	frame, err := protocol.ReadFrame(c.conn)
	if err != nil {
		return nil, &protocol.StreamError{Err: err}
	}
	if c.recvCtr == math.MaxUint64 {
		return nil, &protocol.StreamError{Err: errExhausted}
	}
	pt, err := c.recv.Open(frame[:0], nonce(c.recvCtr), frame, nil)
	if err != nil {
		return nil, &protocol.StreamError{Err: ErrAuth}
	}
	c.recvCtr++
	return pt, nil
}

// WriteMessage encodes m and sends it encrypted.
func (c *SecureConn) WriteMessage(m protocol.Message) error {
	b, err := protocol.Encode(m)
	if err != nil {
		return err
	}
	return c.WritePayload(b)
}

// ReadMessage reads, decrypts and decodes one message. As with
// protocol.ReadMessage, use protocol.IsFatal to tell a dead connection from
// one bad message.
func (c *SecureConn) ReadMessage() (protocol.Message, error) {
	b, err := c.ReadPayload()
	if err != nil {
		return protocol.Message{}, err
	}
	return protocol.Decode(b)
}

func (c *SecureConn) Close() error                       { return c.conn.Close() }
func (c *SecureConn) RemoteAddr() net.Addr               { return c.conn.RemoteAddr() }
func (c *SecureConn) SetDeadline(t time.Time) error      { return c.conn.SetDeadline(t) }
func (c *SecureConn) SetReadDeadline(t time.Time) error  { return c.conn.SetReadDeadline(t) }
func (c *SecureConn) SetWriteDeadline(t time.Time) error { return c.conn.SetWriteDeadline(t) }
