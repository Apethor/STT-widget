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

// Fyne routes mouse events exclusively to the top canvas overlay, even outside
// its rectangle. Hints therefore share the content tree as noninteractive
// objects, so showing one cannot remove hover or intercept a button's click.
type hintLayer struct {
	canvas  fyne.Canvas
	content *fyne.Container
	active  *toolHint
}

func newHintLayer(surface fyne.Canvas) *hintLayer {
	layer := &hintLayer{canvas: surface}
	layer.content = container.New(layer)
	return layer
}

func (l *hintLayer) MinSize([]fyne.CanvasObject) fyne.Size { return fyne.Size{} }
func (l *hintLayer) Layout([]fyne.CanvasObject, fyne.Size) {
	if l.active != nil {
		l.active.position()
	}
}

func (l *hintLayer) hide() {
	if l.active != nil {
		l.active.hide()
	}
}

type toolHint struct {
	text   string
	layer  *hintLayer
	box    fyne.CanvasObject
	anchor fyne.CanvasObject
}

func (h *toolHint) show(object fyne.CanvasObject) {
	if h.text == "" || h.layer == nil {
		return
	}
	h.layer.hide()
	text := widget.NewLabel(uiText(h.text))
	background := canvas.NewRectangle(theme.Color(theme.ColorNameOverlayBackground))
	background.CornerRadius = 6
	background.StrokeWidth = 1
	background.StrokeColor = theme.Color(borderColor)
	box := container.NewStack(background, inset(text, 6, 2))
	box.Resize(box.MinSize())
	h.box, h.anchor = box, object
	h.layer.active = h
	h.layer.content.Objects = []fyne.CanvasObject{box}
	h.layer.content.Refresh()
}

func (h *toolHint) position() {
	position := fyne.CurrentApp().Driver().AbsolutePositionForObject(h.anchor)
	position.Y += h.anchor.Size().Height + 5
	position.X = max(0, min(position.X, h.layer.canvas.Size().Width-h.box.Size().Width))
	if position.Y+h.box.Size().Height > h.layer.canvas.Size().Height {
		position.Y = max(0, position.Y-h.anchor.Size().Height-h.box.Size().Height-10)
	}
	h.box.Move(position)
}

func (h *toolHint) hide() {
	if h.box != nil {
		h.box, h.anchor = nil, nil
		h.layer.active = nil
		h.layer.content.Objects = nil
		h.layer.content.Refresh()
	}
}

type actionButton struct {
	widget.Button
	hint     toolHint
	dotColor color.Color
	compact  bool
}

func (d *desktop) iconButton(text string, icon fyne.Resource, action func()) *actionButton {
	b := &actionButton{
		Button: widget.Button{Icon: icon, OnTapped: d.guard(action)},
		hint:   toolHint{text: text, layer: d.hints},
	}
	b.ExtendBaseWidget(b)
	return b
}

func (b *actionButton) AccessibilityLabel() string { return uiText(b.hint.text) }

func (b *actionButton) MinSize() fyne.Size {
	if b.compact {
		return fyne.NewSize(20, 20)
	}
	return b.Button.MinSize()
}

func (b *actionButton) CreateRenderer() fyne.WidgetRenderer {
	base := b.Button.CreateRenderer()
	if b.compact {
		return compactActionRenderer{base}
	}
	if b.dotColor == nil {
		return base
	}
	dot := canvas.NewCircle(b.dotColor)
	dot.StrokeWidth = 1
	return &indicatorButtonRenderer{WidgetRenderer: base, button: b, dot: dot}
}

type compactActionRenderer struct{ fyne.WidgetRenderer }

func (r compactActionRenderer) MinSize() fyne.Size { return fyne.NewSize(20, 20) }

type indicatorButtonRenderer struct {
	fyne.WidgetRenderer
	button *actionButton
	dot    *canvas.Circle
}

func (r *indicatorButtonRenderer) MinSize() fyne.Size {
	size := r.WidgetRenderer.MinSize()
	size.Width += 16
	return size
}

func (r *indicatorButtonRenderer) Layout(size fyne.Size) {
	r.WidgetRenderer.Layout(size)
	r.dot.Resize(fyne.NewSize(10, 10))
	r.dot.Move(fyne.NewPos(size.Width-18, (size.Height-10)/2))
}

func (r *indicatorButtonRenderer) Refresh() {
	r.WidgetRenderer.Refresh()
	r.dot.FillColor = r.button.dotColor
	r.dot.StrokeColor = r.button.Theme().Color(mutedColor, theme.VariantDark)
	r.dot.Refresh()
	r.Layout(r.button.Size())
}

func (r *indicatorButtonRenderer) Objects() []fyne.CanvasObject {
	return append(r.WidgetRenderer.Objects(), r.dot)
}

func (b *actionButton) MouseIn(event *fynedesktop.MouseEvent) {
	b.Button.MouseIn(event)
	b.hint.show(b)
}

func (b *actionButton) MouseOut() {
	b.hint.hide()
	b.Button.MouseOut()
}

func (b *actionButton) Tapped(event *fyne.PointEvent) {
	b.hint.hide()
	b.Button.Tapped(event)
}

func (b *actionButton) setHint(text string) {
	if b.hint.text == text {
		return
	}
	b.hint.text = uiText(text)
	b.hint.hide()
}

type connectionDot struct {
	widget.BaseWidget
	hint  toolHint
	color fyne.ThemeColorName
}

func (d *desktop) newConnectionDot() *connectionDot {
	dot := &connectionDot{
		hint:  toolHint{text: "AWS: ainda não verificada", layer: d.hints},
		color: mutedColor,
	}
	dot.ExtendBaseWidget(dot)
	return dot
}

func (d *connectionDot) MouseIn(*fynedesktop.MouseEvent)    { d.hint.show(d) }
func (d *connectionDot) MouseMoved(*fynedesktop.MouseEvent) {}
func (d *connectionDot) MouseOut()                          { d.hint.hide() }

func (d *connectionDot) CreateRenderer() fyne.WidgetRenderer {
	dot := canvas.NewCircle(d.Theme().Color(d.color, theme.VariantDark))
	return &connectionRenderer{widget: d, dot: dot, objects: []fyne.CanvasObject{dot}}
}

type connectionRenderer struct {
	widget  *connectionDot
	dot     *canvas.Circle
	objects []fyne.CanvasObject
}

func (r *connectionRenderer) Layout(size fyne.Size) {
	r.dot.Resize(fyne.NewSize(8, 8))
	r.dot.Move(fyne.NewPos((size.Width-8)/2, (size.Height-8)/2))
}

func (r *connectionRenderer) MinSize() fyne.Size { return fyne.NewSize(16, 16) }
func (r *connectionRenderer) Refresh() {
	r.dot.FillColor = r.widget.Theme().Color(r.widget.color, theme.VariantDark)
	r.dot.Refresh()
	canvas.Refresh(r.widget)
}
func (r *connectionRenderer) Objects() []fyne.CanvasObject { return r.objects }
func (r *connectionRenderer) Destroy()                     {}

func (d *desktop) setAWSStatus(text string, role fyne.ThemeColorName) {
	setLabel(d.awsLabel, text, role)
	d.awsIndicator.color = role
	if role == accentColor {
		d.awsIndicator.color = theme.ColorNameSuccess
	}
	profile := d.settings.Profile
	if profile == "" {
		profile = uiText("cadeia padrão da AWS")
	}
	d.awsIndicator.hint.text = uiText(text) + " · " + profile + " · " + d.settings.Region
	d.awsIndicator.hint.hide()
	d.awsIndicator.Refresh()
}
