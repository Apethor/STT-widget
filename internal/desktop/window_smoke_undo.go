//go:build windows && windowsmoke

package desktop

import (
	"os"
	"path/filepath"
	"time"
)

const undoHydrationSmokeMarker = "undo-hydration.fixture.json"

// Reopening an owned fixture must use the production workspace loader without
// the harness's first-run content or groups overwriting its persisted state.
func windowSmokeUndoReopen(directory string) bool {
	marker, err := os.Stat(filepath.Join(directory, undoHydrationSmokeMarker))
	if err != nil || !marker.Mode().IsRegular() {
		return false
	}
	workspace, err := os.Stat(filepath.Join(directory, "workspace-go-v1.json"))
	return err == nil && workspace.Mode().IsRegular()
}

func (d *desktop) windowSmokeUndoDeadline() string {
	if d.closedTab == nil {
		return ""
	}
	return d.closedTab.until.UTC().Format(time.RFC3339Nano)
}
