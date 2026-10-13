package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"ai-talker/internal/store"
)

func Save(directory string, settings Settings) error {
	if err := settings.Validate(); err != nil {
		return err
	}
	path := filepath.Join(directory, "settings.json")
	previous, readErr := os.ReadFile(path)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return fmt.Errorf("preservar as configurações anteriores: %w", readErr)
	}
	if len(previous) > 0 {
		var original map[string]json.RawMessage
		if err := json.Unmarshal(previous, &original); err != nil {
			original = make(map[string]json.RawMessage)
		}
		if _, native := original["recorder_backend"]; !native {
			backup := filepath.Join(directory, "settings-python.json")
			_, statErr := os.Stat(backup)
			if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
				return fmt.Errorf("conferir as preferências Python preservadas: %w", statErr)
			}
			if errors.Is(statErr, os.ErrNotExist) {
				if err := store.Write(backup, previous); err != nil {
					return fmt.Errorf("preservar as preferências Python: %w", err)
				}
			}
		}
	}
	settings.RecorderBackend = "winmm"
	return store.WriteJSON(filepath.Join(directory, "settings.json"), settings)
}
