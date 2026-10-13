//go:build !windows

package platform

import "context"

func SelectScreenRegion(context.Context) (ScreenRegion, error) {
	return ScreenRegion{}, ErrUnsupportedScreenRegion
}
