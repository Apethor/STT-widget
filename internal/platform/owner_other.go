//go:build !windows

package platform

func OwnWindow(string) error { return ErrUnsupported }
