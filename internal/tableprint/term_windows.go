//go:build windows

package tableprint

import (
	"os"

	"golang.org/x/sys/windows"
)

// terminalWidth returns the current console buffer width on Windows,
// falling back to defaultTerminalWidth() if stdout is not a console
// (e.g. redirected to a file or pipe).
func terminalWidth() int {
	h := windows.Handle(os.Stdout.Fd())

	var info windows.ConsoleScreenBufferInfo
	if err := windows.GetConsoleScreenBufferInfo(h, &info); err != nil {
		return defaultTerminalWidth()
	}

	w := int(info.Window.Right-info.Window.Left) + 1
	if w <= 0 {
		return defaultTerminalWidth()
	}
	return w
}