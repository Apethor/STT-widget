//go:build !windows

package recorder

func SharedMicrophones() ([]Device, error) { return nil, ErrUnsupportedPlatform }
func PlaybackDevices() ([]Device, error)   { return nil, ErrUnsupportedPlatform }
func NewShared(device int, _ string, directory string) *Recorder {
	return NewWithInput(device, directory, nil)
}
func NewSystem(_ string, directory string) *Recorder { return NewWithInput(-1, directory, nil) }
