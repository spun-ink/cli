//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// ansiColour switches the console to virtual-terminal processing; a console that refuses (an old
// conhost) would print Bobbin's escape codes as text, so he is not drawn there.
func ansiColour(f *os.File) bool {
	handle := windows.Handle(f.Fd())
	var mode uint32
	if windows.GetConsoleMode(handle, &mode) != nil {
		return false
	}
	return windows.SetConsoleMode(handle, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING) == nil
}
