package protocol

import "testing"

func TestNewID(t *testing.T) {
	a, b := NewID(), NewID()
	if !ValidID(a) || !ValidID(b) {
		t.Fatalf("NewID gave invalid ids %q, %q", a, b)
	}
	if a == b {
		t.Errorf("two NewID calls both gave %q", a)
	}
}
