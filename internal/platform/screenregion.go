package platform

import "errors"

// ScreenRegion is a physical-pixel rectangle on the virtual desktop. X or Y
// can be negative when a selected monitor sits left of or above the primary.
type ScreenRegion struct {
	X, Y          int
	Width, Height int
}

var ErrUnsupportedScreenRegion = errors.New("a seleção de área de vídeo requer Windows")
