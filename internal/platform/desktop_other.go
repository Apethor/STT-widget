//go:build !windows

package platform

import (
	"fmt"
	"runtime"
)

// SetPinned changes whether the current process's window stays above others.
// It is currently implemented only on Windows.
func SetPinned(bool) error {
	return unavailable("pin application window")
}

// Activate brings the current process's desktop window to the foreground.
// It is currently implemented only on Windows.
func Activate() error {
	return unavailable("activate application window")
}

// Minimize iconifies the current process's main window without closing it.
func Minimize() error {
	return unavailable("minimize application window")
}

// DarkTitlebar requests a dark native title bar for the current process's window.
// It is currently implemented only on Windows.
func DarkTitlebar(bool) error {
	return unavailable("set dark title bar")
}

// Copy places text in the native clipboard. It is currently implemented only
// on Windows; a caller can use its GUI framework's clipboard on other platforms.
func Copy(string) error {
	return unavailable("copy text to clipboard")
}

func unavailable(operation string) error {
	return fmt.Errorf("%s on %s: %w", operation, runtime.GOOS, ErrUnsupported)
}
