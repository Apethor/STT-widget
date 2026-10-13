package platform

import (
	"errors"
	"fmt"
	"os"
	"sync"
)

// FileLock owns an exclusive OS lock until Close or process termination.
// It must not be copied after use. Its zero value is safe to close.
type FileLock struct {
	file     *os.File
	once     sync.Once
	closeErr error
}

// TryLock opens or creates path and acquires its lock without waiting.
// The parent directory must already exist. Existing contents are preserved.
// Contention returns an error matching ErrLocked through errors.Is.
//
// The lock file is never deleted: replacing its inode could allow competing
// processes to lock different files at the same path.
func TryLock(path string) (*FileLock, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file %q: %w", path, err)
	}

	if err := lockFile(file); err != nil {
		lockErr := fmt.Errorf("lock file %q: %w", path, err)
		if closeErr := file.Close(); closeErr != nil {
			lockErr = errors.Join(lockErr, fmt.Errorf("close lock file %q: %w", path, closeErr))
		}
		return nil, lockErr
	}

	return &FileLock{file: file}, nil
}

// Close releases the lock and closes its file. It is safe to call concurrently,
// repeatedly, or on a nil receiver. Repeated calls return the first close error.
func (l *FileLock) Close() error {
	if l == nil {
		return nil
	}

	l.once.Do(func() {
		if l.file == nil {
			return
		}

		if err := unlockFile(l.file); err != nil {
			l.closeErr = fmt.Errorf("unlock file %q: %w", l.file.Name(), err)
		}
		if err := l.file.Close(); err != nil {
			l.closeErr = errors.Join(
				l.closeErr,
				fmt.Errorf("close lock file %q: %w", l.file.Name(), err),
			)
		}
	})

	return l.closeErr
}
