//go:build !windows

package platform

import (
	"fmt"
	"os/exec"
	"runtime"
)

// OpenFolder opens an existing directory in the platform's file manager.
// Arguments are passed directly to the opener, without a shell.
// Callers should run this outside the UI loop.
func OpenFolder(path string) error {
	absolute, err := folderPath(path)
	if err != nil {
		return err
	}

	var opener string
	switch runtime.GOOS {
	case "darwin":
		opener = "open"
	case "linux", "freebsd", "openbsd", "netbsd", "dragonfly":
		opener = "xdg-open"
	default:
		return fmt.Errorf("open folder on %s: %w", runtime.GOOS, ErrUnsupported)
	}

	// absolute cannot be interpreted as an option, even if the original
	// directory name starts with a dash.
	if err := exec.Command(opener, absolute).Run(); err != nil {
		return fmt.Errorf("open folder %q using %s: %w", absolute, opener, err)
	}
	return nil
}
