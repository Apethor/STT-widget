//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package platform

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestOpenFolderPassesLiteralPathAndReturnsProcessError(t *testing.T) {
	directory := t.TempDir()
	folder := filepath.Join(directory, "--folder ; literal $() spaces")
	if err := os.Mkdir(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	opener := "xdg-open"
	if runtime.GOOS == "darwin" {
		opener = "open"
	}
	record := filepath.Join(directory, "arguments.txt")
	// This fake opener records arguments and exits. It opens no GUI and never
	// evaluates the supplied directory name.
	script := "#!/bin/sh\nprintf '%s\\n' \"$#\" \"$1\" > \"$AI_TALKER_PLATFORM_OPENER_ARGS\"\nexit 23\n"
	if err := os.WriteFile(filepath.Join(directory, opener), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory)
	t.Setenv("AI_TALKER_PLATFORM_OPENER_ARGS", record)

	err := OpenFolder(folder)
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 23 {
		t.Fatalf("OpenFolder = %v, want exit status 23", err)
	}
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{"1", folder, ""}, "\n")
	if string(data) != want {
		t.Fatalf("opener arguments = %q, want %q", data, want)
	}
}
