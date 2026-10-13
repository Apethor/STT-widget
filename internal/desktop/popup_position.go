package desktop

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
)

// Ordinary popups use their requested position even when part of the content
// would fall outside the canvas. Align shortcuts with the right edge of their
// button, then choose the available space above or below it.
func (d *desktop) positionShortcut(popup *widget.PopUp, anchor fyne.CanvasObject) {
	const margin, gap = float32(8), float32(4)
	canvasSize := d.window.Canvas().Size()
	position := d.application.Driver().AbsolutePositionForObject(anchor)
	size := popup.Size()
	x := position.X + anchor.Size().Width - size.Width
	y := position.Y + anchor.Size().Height + gap
	if y+size.Height > canvasSize.Height-margin {
		y = position.Y - size.Height - gap
	}
	x = max(margin, min(x, canvasSize.Width-size.Width-margin))
	y = max(margin, min(y, canvasSize.Height-size.Height-margin))
	popup.ShowAtPosition(fyne.NewPos(x, y))
}

func (d *desktop) positionCloudMenu(menu *widget.PopUpMenu, anchor fyne.CanvasObject) {
	const margin, gap = float32(8), float32(4)
	canvas := d.window.Canvas().Size()
	position := d.application.Driver().AbsolutePositionForObject(anchor)
	var childWidth float32
	for _, item := range menu.Items {
		if parent, ok := item.(interface{ Child() *widget.Menu }); ok && parent.Child() != nil {
			childWidth = max(childWidth, parent.Child().MinSize().Width)
		}
	}
	// Leave room for the submenu on the left before Fyne chooses its edge.
	// Anchoring a wide parent at its old X used to force an overlapping child.
	x := max(position.X+anchor.Size().Width-menu.Size().Width, margin+childWidth+gap)
	x = min(x, canvas.Width-menu.Size().Width-margin)
	y := min(position.Y+anchor.Size().Height+gap, canvas.Height-menu.Size().Height-margin)
	menu.ShowAtPosition(fyne.NewPos(max(margin, x), max(margin, y)))
}
