//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package platform

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func lockFile(file *os.File) error {
	err := flock(file, unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return errors.Join(ErrLocked, err)
	}
	return err
}

func unlockFile(file *os.File) error {
	return flock(file, unix.LOCK_UN)
}

func flock(file *os.File, operation int) error {
	for {
		err := unix.Flock(int(file.Fd()), operation)
		if !errors.Is(err, unix.EINTR) {
			return err
		}
	}
}
