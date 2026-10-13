//go:build !windows || !cgo

package platform

import "context"

func ChooseFile(context.Context, FileDialogOptions) (string, error) {
	return "", ErrUnsupported
}
