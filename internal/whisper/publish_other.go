//go:build !windows

package whisper

func artifactBusy(error) bool { return false }
