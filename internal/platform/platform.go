// Package platform provides native desktop operations and process-safe locks.
// Desktop operations do not depend on the GUI framework.
package platform

import "errors"

var (
	// ErrLocked means another owner already holds the file lock.
	ErrLocked = errors.New("platform: file is locked")

	// ErrUnsupported means the native operation is unavailable on this platform.
	ErrUnsupported = errors.New("platform: operation is unsupported")

	// ErrWindowNotFound means the current process has no suitable desktop window.
	ErrWindowNotFound = errors.New("platform: application window not found")

	// ErrForegroundDenied means Windows refused foreground activation.
	ErrForegroundDenied = errors.New("platform: foreground activation denied")

	// ErrClipboardBusy means opening the clipboard failed. The caller may retry
	// asynchronously; Copy never waits for another application to release it.
	ErrClipboardBusy = errors.New("platform: clipboard is busy")
)
