package protocol

import (
	"crypto/rand"
	"encoding/hex"
)

// NewID returns a fresh random peer id: 128 bits as IDLen lowercase hex digits.
// It is not derived from anything about this machine.
func NewID() string {
	var b [IDLen / 2]byte
	rand.Read(b[:]) // never returns an error; it crashes the program if the OS can't supply randomness
	return hex.EncodeToString(b[:])
}
