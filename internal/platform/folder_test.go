package platform

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFolderPath(t *testing.T) {
	t.Parallel()
	directory := filepath.Join(t.TempDir(), "--pasta com espaços; ação $literal")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := folderPath(directory)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.Abs(directory)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("folder path = %q, want %q", got, want)
	}
}

func TestOpenFolderRejectsInvalidPaths(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	file := filepath.Join(directory, "transcript.txt")
	if err := os.WriteFile(file, []byte("preserved"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		path string
		want error
	}{
		{name: "empty path", path: "", want: os.ErrInvalid},
		{name: "nul in path", path: "bad\x00path", want: os.ErrInvalid},
		{name: "missing folder", path: filepath.Join(directory, "missing"), want: os.ErrNotExist},
		{name: "regular file", path: file, want: os.ErrInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Every case fails before launching a file manager.
			if err := OpenFolder(test.path); !errors.Is(err, test.want) {
				t.Fatalf("OpenFolder = %v, want %v", err, test.want)
			}
		})
	}
}
