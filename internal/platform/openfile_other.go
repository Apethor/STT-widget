//go:build !windows

package platform

func OpenFile(string) error { return ErrUnsupported }
