package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func folderPath(path string) (string, error) {
	if path == "" || strings.IndexByte(path, 0) >= 0 {
		return "", fmt.Errorf("open folder: %w", os.ErrInvalid)
	}

	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve folder %q: %w", path, err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", fmt.Errorf("inspect folder %q: %w", absolute, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("open folder %q: path is not a directory: %w", absolute, os.ErrInvalid)
	}
	return absolute, nil
}
