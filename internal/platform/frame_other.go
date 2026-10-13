//go:build !windows

package platform

import "image/color"

// IntegratedTitlebar is available only on Windows.
func IntegratedTitlebar(bool, color.NRGBA, WindowHeader) error {
	return unavailable("integrate application frame")
}
