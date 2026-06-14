//go:build !windows

package tableprint

// terminalWidth on non-Windows platforms uses the generic fallback (COLUMNS
// env var or 120). This package is currently only imported from
// main_windows.go, but kept portable for future reuse.
func terminalWidth() int {
	return defaultTerminalWidth()
}