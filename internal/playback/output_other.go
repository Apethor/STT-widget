//go:build !windows

package playback

import (
	"errors"
	"os"
)

func createPlaybackFile(folder string) (*os.File, error) {
	return os.CreateTemp(folder, "audio-*.wav")
}

func openOutput(string) (output, error) {
	return nil, errors.New("a reprodução integrada está disponível no Windows")
}
