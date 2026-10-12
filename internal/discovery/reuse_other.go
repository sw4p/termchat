//go:build !unix

package discovery

import "syscall"

// reusePort is a no-op where SO_REUSEPORT doesn't exist (Windows isn't a
// supported platform yet; this keeps the package compiling there).
func reusePort(network, address string, c syscall.RawConn) error { return nil }
