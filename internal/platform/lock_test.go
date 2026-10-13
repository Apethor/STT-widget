//go:build windows || darwin || dragonfly || freebsd || linux || netbsd || openbsd

package platform

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestTryLockPreservesFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "gui.lock")
	contents := []byte("existing lock file contents")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	lock, err := TryLock(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := lock.Close(); err != nil {
			t.Error(err)
		}
	})
	other, err := TryLock(path)
	if other != nil || !errors.Is(err, ErrLocked) {
		if other != nil {
			if closeErr := other.Close(); closeErr != nil {
				t.Error(closeErr)
			}
		}
		t.Fatalf("second TryLock = %v, %v; want nil, ErrLocked", other, err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}

	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("lock file was removed: %v", err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("lock file identity changed")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, contents) {
		t.Fatalf("lock contents = %q, want %q", data, contents)
	}
}

func TestTryLockFilesystemErrors(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	tests := []struct {
		name string
		path string
		want error
	}{
		{name: "missing parent", path: filepath.Join(directory, "missing", "gui.lock"), want: os.ErrNotExist},
		{name: "directory", path: directory},
		{name: "empty path", path: ""},
		{name: "nul in path", path: filepath.Join(directory, "bad\x00.lock")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lock, err := TryLock(test.path)
			if lock != nil {
				if closeErr := lock.Close(); closeErr != nil {
					t.Error(closeErr)
				}
				t.Fatal("invalid path acquired a lock")
			}
			if err == nil || errors.Is(err, ErrLocked) {
				t.Fatalf("TryLock = %v; want filesystem error", err)
			}
			if test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("TryLock = %v; want %v", err, test.want)
			}
		})
	}
}

func TestTryLockCompetingProcesses(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "gui.lock")
	lock, err := TryLock(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := lock.Close(); err != nil {
			t.Error(err)
		}
	})

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	command := lockHelperCommand(t, ctx, path, "contend")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("competing process: %v\n%s", err, output)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}

	holder := startLockHolder(t, path)
	assertLocked(t, path)
	holder.release(t)
	next, err := TryLock(path)
	if err != nil {
		t.Fatalf("lock after graceful process exit: %v", err)
	}
	if err := next.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestTryLockReleasedOnProcessDeath(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "gui.lock")
	holder := startLockHolder(t, path)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	assertLocked(t, path)

	holder.kill(t)
	next := lockAfterProcessDeath(t, path)
	if err := next.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("process death replaced or deleted the lock file")
	}
}

func TestFileLockClose(t *testing.T) {
	t.Parallel()
	t.Run("nil and zero value", func(t *testing.T) {
		var nilLock *FileLock
		var zeroLock FileLock
		if err := nilLock.Close(); err != nil {
			t.Fatal(err)
		}
		if err := zeroLock.Close(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("concurrent and repeated", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "gui.lock")
		lock, err := TryLock(path)
		if err != nil {
			t.Fatal(err)
		}
		results := make(chan error, 8)
		var workers sync.WaitGroup
		for range cap(results) {
			workers.Go(func() { results <- lock.Close() })
		}
		workers.Wait()
		close(results)
		for err := range results {
			if err != nil {
				t.Error(err)
			}
		}

		next, err := TryLock(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := next.Close(); err != nil {
				t.Error(err)
			}
		})
		if err := lock.Close(); err != nil {
			t.Fatal(err)
		}
		// Closing a former owner again must not release the newer owner's lock.
		assertLocked(t, path)
	})
}

// TestFileLockHelper runs only in child processes created by these tests.
// It does not use the application's data directory or interact with a GUI.
func TestFileLockHelper(t *testing.T) {
	mode := os.Getenv("AI_TALKER_PLATFORM_LOCK_HELPER")
	if mode == "" {
		t.Skip("subprocess helper")
	}
	path := os.Getenv("AI_TALKER_PLATFORM_LOCK_PATH")
	lock, err := TryLock(path)
	if mode == "contend" {
		if lock != nil {
			if closeErr := lock.Close(); closeErr != nil {
				t.Error(closeErr)
			}
		}
		if !errors.Is(err, ErrLocked) {
			t.Fatalf("competing process TryLock = %v; want ErrLocked", err)
		}
		return
	}
	if mode != "hold" {
		t.Fatalf("unknown helper mode %q", mode)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := lock.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := fmt.Fprintln(os.Stdout, "LOCKED"); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		t.Fatal(err)
	}
}

func assertLocked(t *testing.T, path string) {
	t.Helper()
	lock, err := TryLock(path)
	if lock != nil {
		if closeErr := lock.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}
	if lock != nil || !errors.Is(err, ErrLocked) {
		t.Fatalf("TryLock = %v, %v; want nil, ErrLocked", lock, err)
	}
}

func lockAfterProcessDeath(t *testing.T, path string) *FileLock {
	t.Helper()
	// Windows documents that OS cleanup after termination can be delayed.
	// Only the test waits; TryLock itself always remains nonblocking.
	deadline := time.Now().Add(2 * time.Second)
	for {
		lock, err := TryLock(path)
		if err == nil {
			return lock
		}
		if !errors.Is(err, ErrLocked) || !time.Now().Before(deadline) {
			t.Fatalf("lock after process death: %v", err)
		}
		select {
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func lockHelperCommand(t *testing.T, ctx context.Context, path, mode string) *exec.Cmd {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, executable, "-test.run=^TestFileLockHelper$")
	command.Env = append(
		os.Environ(),
		"AI_TALKER_PLATFORM_LOCK_HELPER="+mode,
		"AI_TALKER_PLATFORM_LOCK_PATH="+path,
	)
	Hide(command)
	return command
}

type lockHolder struct {
	command *exec.Cmd
	input   io.WriteCloser
	stderr  bytes.Buffer
	waited  bool
}

func startLockHolder(t *testing.T, path string) *lockHolder {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	holder := &lockHolder{command: lockHelperCommand(t, ctx, path, "hold")}
	holder.command.Stderr = &holder.stderr

	var err error
	holder.input, err = holder.command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := holder.command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := holder.command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if holder.waited {
			return
		}
		if err := holder.command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			t.Error(err)
		}
		err := holder.command.Wait()
		holder.waited = true
		if err != nil {
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) {
				t.Error(err)
			}
		}
	})
	line, err := bufio.NewReader(output).ReadString('\n')
	if err != nil || line != "LOCKED\n" {
		holder.kill(t)
		t.Fatalf("lock holder readiness = %q, %v\n%s", line, err, holder.stderr.String())
	}
	return holder
}

func (h *lockHolder) release(t *testing.T) {
	t.Helper()
	if err := h.input.Close(); err != nil {
		t.Fatal(err)
	}
	err := h.command.Wait()
	h.waited = true
	if err != nil {
		t.Fatalf("lock holder exit: %v\n%s", err, h.stderr.String())
	}
}

func (h *lockHolder) kill(t *testing.T) {
	t.Helper()
	if err := h.command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Fatal(err)
	}
	err := h.command.Wait()
	h.waited = true
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		t.Fatalf("wait for killed lock holder: %v\n%s", err, h.stderr.String())
	}
}
