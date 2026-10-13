package desktop

import (
	"path/filepath"
	"strings"
	"time"
)

// Registered recordings/screenshots may use the legacy root or one dated
// subfolder. Other directories remain outside the persisted capture contract.
func captureDirectory(directory, root string) bool {
	if strings.EqualFold(directory, root) {
		return true
	}
	if !strings.EqualFold(filepath.Dir(directory), root) {
		return false
	}
	day := filepath.Base(directory)
	date, err := time.Parse("2006-01-02", day)
	return err == nil && date.Format("2006-01-02") == day
}
