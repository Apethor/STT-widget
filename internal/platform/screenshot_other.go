//go:build !windows

package platform

import (
	"context"
	"image"
)

func SelectScreenshot(context.Context) (image.Image, error) {
	return nil, ErrUnsupported
}
