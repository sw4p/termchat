//go:build !windows

package ui

// enableVT is a no-op: Unix terminals interpret escape codes already.
func enableVT(fd int) (restore func(), err error) { return func() {}, nil }
