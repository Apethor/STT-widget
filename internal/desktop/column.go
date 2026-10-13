package desktop

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	fynedesktop "fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// A zero height follows the viewport. Dragging the bottom grip chooses a total
// height; the scroll container exposes its bar only when that height overflows.
type columnLayout struct {
	height   float32
	rendered float32
}

func (l *columnLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	height := l.height
	if height == 0 {
		height = size.Height
	}
	height = max(height, columnMinimum(objects).Height)
	handleHeight := objects[1].MinSize().Height
	objects[0].Move(fyne.NewPos(0, 0))
	objects[0].Resize(fyne.NewSize(size.Width, height-handleHeight))
	objects[1].Move(fyne.NewPos(0, height-handleHeight))
	objects[1].Resize(fyne.NewSize(size.Width, handleHeight))
	l.rendered = height
}

func (l *columnLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	size := columnMinimum(objects)
	size.Height = max(size.Height, l.height)
	return size
}

func columnMinimum(objects []fyne.CanvasObject) fyne.Size {
	content, handle := objects[0].MinSize(), objects[1].MinSize()
	return fyne.NewSize(max(content.Width, handle.Width), content.Height+handle.Height)
}

func newResizableColumn(
	content fyne.CanvasObject,
	hints *hintLayer,
	height float32,
	changed func(float32),
) (*container.Scroll, *heightHandle) {
	layout := &columnLayout{height: height}
	var body *fyne.Container
	var scroll *container.Scroll
	handle := newHeightHandle(hints, func(delta float32) {
		layout.height = max(columnMinimum(body.Objects).Height, layout.rendered+delta)
		changed(layout.height)
		body.Refresh()
		scroll.Refresh()
		scroll.ScrollToOffset(scroll.Offset)
	})
	body = container.New(layout, content, handle)
	scroll = container.NewVScroll(body)
	return scroll, handle
}

type heightHandle struct {
	widget.BaseWidget
	hint    toolHint
	hovered bool
	drag    func(float32)
	ended   func()
}

func newHeightHandle(hints *hintLayer, drag func(float32)) *heightHandle {
	handle := &heightHandle{
		hint: toolHint{text: "Arrastar para ajustar a altura das caixas", layer: hints},
		drag: drag,
	}
	handle.ExtendBaseWidget(handle)
	return handle
}

func (h *heightHandle) AccessibilityLabel() string { return h.hint.text }
func (h *heightHandle) Cursor() fynedesktop.Cursor { return fynedesktop.VResizeCursor }

func (h *heightHandle) Dragged(event *fyne.DragEvent) {
	h.hint.hide()
	h.drag(event.Dragged.DY)
}

func (h *heightHandle) DragEnd() {
	if h.ended != nil {
		h.ended()
	}
}

func (h *heightHandle) MouseIn(*fynedesktop.MouseEvent) {
	h.hovered = true
	h.hint.show(h)
	h.Refresh()
}

func (h *heightHandle) MouseMoved(*fynedesktop.MouseEvent) {}

func (h *heightHandle) MouseOut() {
	h.hovered = false
	h.hint.hide()
	h.Refresh()
}

func (h *heightHandle) CreateRenderer() fyne.WidgetRenderer {
	background := canvas.NewRectangle(color.Transparent)
	grip := canvas.NewRectangle(h.Theme().Color(mutedColor, theme.VariantDark))
	grip.CornerRadius = 2
	return &heightHandleRenderer{
		handle: h, background: background, grip: grip,
		objects: []fyne.CanvasObject{background, grip},
	}
}

type heightHandleRenderer struct {
	handle     *heightHandle
	background *canvas.Rectangle
	grip       *canvas.Rectangle
	objects    []fyne.CanvasObject
}

func (r *heightHandleRenderer) Layout(size fyne.Size) {
	r.background.Resize(size)
	r.grip.Move(fyne.NewPos((size.Width-36)/2, (size.Height-3)/2))
	r.grip.Resize(fyne.NewSize(36, 3))
}

func (r *heightHandleRenderer) MinSize() fyne.Size {
	return fyne.NewSize(36, max(8, r.handle.Theme().Size(theme.SizeNameSplitThickness)))
}

func (r *heightHandleRenderer) Refresh() {
	r.background.FillColor = color.Transparent
	if r.handle.hovered {
		r.background.FillColor = r.handle.Theme().Color(theme.ColorNameHover, theme.VariantDark)
	}
	r.grip.FillColor = r.handle.Theme().Color(mutedColor, theme.VariantDark)
	r.background.Refresh()
	r.grip.Refresh()
	r.Layout(r.handle.Size())
	canvas.Refresh(r.handle)
}

func (r *heightHandleRenderer) Objects() []fyne.CanvasObject { return r.objects }
func (r *heightHandleRenderer) Destroy()                     {}
