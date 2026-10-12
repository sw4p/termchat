//go:build unix

package discovery

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// reusePort lets our socket share UDP port 5353 with the system's mDNS
// daemon (mDNSResponder on macOS, Avahi on Linux) and other termchat
// instances. Without SO_REUSEPORT, macOS refuses the bind outright.
func reusePort(network, address string, c syscall.RawConn) error {
	var serr error
	err := c.Control(func(fd uintptr) {
		if serr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1); serr != nil {
			return
		}
		serr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1)
	})
	if err != nil {
		return err
	}
	return serr
}
