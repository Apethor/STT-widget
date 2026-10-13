package platform

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestImagePathOnlyAcceptsExistingPNGFiles(t *testing.T) {
	directory := t.TempDir()
	valid := filepath.Join(directory, "Screenshot com espaços.png")
	if err := os.WriteFile(valid, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if path, err := imagePath(valid); err != nil || path != valid {
		t.Fatalf("o caminho não preservou os espaços: %q, %v", path, err)
	}
	for _, path := range []string{filepath.Join(directory, "missing.png"), filepath.Join(directory, "file.exe"), directory} {
		if _, err := imagePath(path); err == nil {
			t.Fatalf("o caminho %q foi aceito como screenshot", path)
		}
	}
	if _, err := imagePath(filepath.Join(directory, "missing.png")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("arquivo inexistente não retornou um erro reconhecível")
	}
}
