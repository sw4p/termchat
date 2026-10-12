package session

import (
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/sw4p/termchat/internal/crypto"
	"github.com/sw4p/termchat/internal/protocol"
)

var (
	errBusy = errors.New("peer is busy")
	errSelf = errors.New("connected to yourself")
)

// handshakeDial encrypts conn, sends our hello and waits for the reply:
// accept (returned as the partner) or busy (errBusy). The key exchange and
// the hello/accept share one deadline.
func handshakeDial(conn net.Conn, id, nick string, timeout time.Duration) (*crypto.SecureConn, protocol.Message, error) {
	conn.SetDeadline(time.Now().Add(timeout))
	defer conn.SetDeadline(time.Time{})

	sc, err := crypto.Handshake(conn)
	if err != nil {
		return nil, protocol.Message{}, err
	}
	hello := protocol.Message{Type: protocol.TypeHello, ID: id, Nick: nick}
	if err := sc.WriteMessage(hello); err != nil {
		return nil, protocol.Message{}, err
	}
	m, err := sc.ReadMessage()
	if err != nil {
		return nil, protocol.Message{}, err
	}
	switch {
	case m.Type == protocol.TypeBusy:
		return nil, protocol.Message{}, errBusy
	case m.Type != protocol.TypeAccept:
		return nil, protocol.Message{}, fmt.Errorf("expected accept, got %q", m.Type)
	case m.ID == id:
		return nil, protocol.Message{}, errSelf
	}
	return sc, m, nil
}

// readHello is the listening side's first half: encrypt conn and read the
// dialer's hello. It sets a deadline on conn and leaves it set, because the
// reply (accept or busy) must go out within the same deadline. The caller
// decides which reply to send, then clears the deadline.
func readHello(conn net.Conn, id string, timeout time.Duration) (*crypto.SecureConn, protocol.Message, error) {
	conn.SetDeadline(time.Now().Add(timeout))
	sc, err := crypto.Handshake(conn)
	if err != nil {
		return nil, protocol.Message{}, err
	}
	m, err := sc.ReadMessage()
	if err != nil {
		return nil, protocol.Message{}, err
	}
	if m.Type != protocol.TypeHello {
		return nil, protocol.Message{}, fmt.Errorf("expected hello, got %q", m.Type)
	}
	if m.ID == id {
		return nil, protocol.Message{}, errSelf
	}
	return sc, m, nil
}
