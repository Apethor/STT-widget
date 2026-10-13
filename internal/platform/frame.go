package platform

// WindowHeader describes the app's custom title row in client pixels.
// ControlsWidth reserves the toolbar at the right for normal canvas events.
type WindowHeader struct {
	Top           int
	Bottom        int
	ControlsWidth int
}
