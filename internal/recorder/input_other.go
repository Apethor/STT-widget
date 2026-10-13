//go:build !windows

package recorder

import "context"

// Microphones enumerates native recording devices on supported platforms.
func Microphones() ([]Device, error) {
	return nil, ErrUnsupportedPlatform
}

type unsupportedInput struct{}

func newNativeInput() Input {
	return unsupportedInput{}
}

func (unsupportedInput) Open(context.Context, int, int) error {
	return ErrUnsupportedPlatform
}

func (unsupportedInput) Start(context.Context, func([]byte, error)) error {
	return ErrUnsupportedPlatform
}

func (unsupportedInput) Close() error {
	return nil
}
