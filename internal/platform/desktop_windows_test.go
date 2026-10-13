package platform

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"

	"golang.org/x/sys/windows"
)

func TestCurrentWindowBelongsToCurrentProcess(t *testing.T) {
	t.Parallel()
	// Enumeration only: no windows are created, activated, resized, or pinned.
	// Concurrent queries also exercise the native callback's request lifetime.
	results := make(chan error, 8)
	var workers sync.WaitGroup
	for range cap(results) {
		workers.Go(func() {
			window, err := currentWindow()
			if errors.Is(err, ErrWindowNotFound) {
				results <- nil
				return
			}
			if err != nil {
				results <- err
				return
			}
			var pid uint32
			if _, err := windows.GetWindowThreadProcessId(window, &pid); err != nil {
				results <- err
				return
			}
			if pid != uint32(os.Getpid()) {
				results <- fmt.Errorf("selected window belongs to pid %d, want %d", pid, os.Getpid())
				return
			}
			results <- nil
		})
	}
	workers.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Error(err)
		}
	}
}
