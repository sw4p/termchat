//go:build windows

package discovery

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// reusePort lets our socket share UDP port 5353 with the DNS Client service
// (which holds it on every modern Windows) and other termchat instances.
// Windows has no SO_REUSEPORT; SO_REUSEADDR alone allows the shared bind.
func reusePort(network, address string, c syscall.RawConn) error {
	var serr error
	err := c.Control(func(fd uintptr) {
		serr = windows.SetsockoptInt(windows.Handle(fd), windows.SOL_SOCKET, windows.SO_REUSEADDR, 1)
	})
	if err != nil {
		return err
	}
	return serr
}
