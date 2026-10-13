//go:build !windows && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd

package platform

import (
	"fmt"
	"os"
	"runtime"
)

func lockFile(*os.File) error {
	return fmt.Errorf("file locking on %s: %w", runtime.GOOS, ErrUnsupported)
}

func unlockFile(*os.File) error {
	return fmt.Errorf("file unlocking on %s: %w", runtime.GOOS, ErrUnsupported)
}
