//go:build !windows

package platform

import "image/color"

func AuxiliaryTitlebar(string, bool, color.NRGBA, WindowHeader) error {
	return unavailable("integrate auxiliary frame")
}
func AuxiliaryMinimize(string) error        { return unavailable("minimize auxiliary window") }
func AuxiliaryToggleMaximized(string) error { return unavailable("maximize auxiliary window") }
func AuxiliaryWindowGeometry(string) (AuxiliaryGeometry, error) {
	return AuxiliaryGeometry{}, unavailable("read auxiliary window position")
}
func PositionAuxiliaryWindow(string, int, int) error { return unavailable("position auxiliary window") }
