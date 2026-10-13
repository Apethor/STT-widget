package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func imagePath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(filepath.Ext(absolute), ".png") {
		return "", fmt.Errorf("a screenshot precisa ser um PNG: %w", os.ErrInvalid)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("a screenshot não é um arquivo: %w", os.ErrInvalid)
	}
	return absolute, nil
}
