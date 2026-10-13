package whisper

import (
	"context"
	"os"
	"time"
)

func renameArtifact(ctx context.Context, source, destination string) error {
	return retryArtifactRename(ctx, source, destination, os.Rename)
}

func retryArtifactRename(ctx context.Context, source, destination string, rename func(string, string) error) error {
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := rename(source, destination)
		if err == nil || attempt >= 5 || !artifactBusy(err) {
			return err
		}
		// Windows scanners can briefly hold a newly extracted executable.
		// Cancellation interrupts the bounded wait without deleting backups.
		timer := time.NewTimer(time.Duration(attempt+1) * 100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func restoreArtifact(source, destination string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return renameArtifact(ctx, source, destination)
}
