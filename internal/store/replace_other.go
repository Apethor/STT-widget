//go:build !windows

package store

import "os"

func replace(source, destination string) error {
	return os.Rename(source, destination)
}
