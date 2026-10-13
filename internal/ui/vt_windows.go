//go:build windows

package ui

import "golang.org/x/sys/windows"

// enableVT turns on ANSI escape processing for the console behind fd, so our
// colours and cursor movement work in cmd.exe and legacy conhost, not just
// Windows Terminal. It fails on consoles too old to support it (before
// Windows 10 1511). restore puts the old output mode back.
func enableVT(fd int) (restore func(), err error) {
	h := windows.Handle(fd)
	var old uint32
	if err := windows.GetConsoleMode(h, &old); err != nil {
		return nil, err
	}
	// DISABLE_NEWLINE_AUTO_RETURN: "\n" moves down without returning to
	// column 0, as on a Unix terminal in raw mode; Screen writes "\r\n".
	mode := old | windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING | windows.DISABLE_NEWLINE_AUTO_RETURN
	if err := windows.SetConsoleMode(h, mode); err != nil {
		return nil, err
	}
	return func() { windows.SetConsoleMode(h, old) }, nil
}
