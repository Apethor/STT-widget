package whisper

import (
	"context"
	"errors"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWhisperPublishRecoversFromATransientScannerLock(t *testing.T) {
	t.Parallel()
	calls := 0
	err := retryArtifactRename(t.Context(), "stage", "installed", func(source, destination string) error {
		calls++
		if source != "stage" || destination != "installed" {
			t.Fatal("retry changed its artifact destination")
		}
		if calls == 1 {
			return windows.ERROR_ACCESS_DENIED
		}
		return nil
	})
	if err != nil || calls != 2 {
		t.Fatalf("a transient Windows lock failed installation: calls=%d / %v", calls, err)
	}
}

func TestWhisperPublishCancellationStopsRetryingWithoutRemovingArtifacts(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := 0
	err := retryArtifactRename(ctx, "stage", "installed", func(string, string) error {
		calls++
		cancel()
		return windows.ERROR_SHARING_VIOLATION
	})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("canceled artifact publication was retried: calls=%d / %v", calls, err)
	}
}
