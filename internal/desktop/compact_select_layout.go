package desktop

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
)

// Select already truncates its visible label. A bounded minimum keeps longer
// model/instruction names from widening the window; the popup shows them fully.
type compactSelectLayout struct{ width float32 }

func (l compactSelectLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, object := range objects {
		object.Move(fyne.NewPos(0, 0))
		object.Resize(size)
	}
}

func (l compactSelectLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	height := float32(0)
	for _, object := range objects {
		height = max(height, object.MinSize().Height)
	}
	return fyne.NewSize(l.width, height)
}

func compactSelect(object fyne.CanvasObject, width float32) *fyne.Container {
	return container.New(compactSelectLayout{width: width}, object)
}
