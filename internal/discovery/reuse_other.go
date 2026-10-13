//go:build !unix && !windows

package discovery

import "syscall"

// reusePort is a no-op on platforms other than Unix and Windows; it keeps the
// package compiling there.
func reusePort(network, address string, c syscall.RawConn) error { return nil }
