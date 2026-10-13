//go:build !windows

package recorder

import (
	"errors"
	"testing"
)

func TestNativeRecorderReportsUnsupportedPlatform(t *testing.T) {
	t.Parallel()
	if _, err := Microphones(); !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("Microphones = %v", err)
	}
	r := New(-1, t.TempDir())
	if path, err := r.Start(t.Context()); path != "" || !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("native Start = %q, %v", path, err)
	}
	if state := r.Snapshot(); state.Active || state.Error == "" {
		t.Fatalf("unsupported platform is not visible: %+v", state)
	}
}
