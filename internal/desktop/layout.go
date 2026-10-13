package desktop

import (
	"math"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	fynedesktop "fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type insetLayout struct {
	horizontal float32
	vertical   float32
	minimum    fyne.Size
}

func (l insetLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	content := fyne.NewSize(max(0, size.Width-l.horizontal*2), max(0, size.Height-l.vertical*2))
	for _, object := range objects {
		if !object.Visible() {
			continue
		}
		object.Move(fyne.NewPos(l.horizontal, l.vertical))
		object.Resize(content)
	}
}

func (l insetLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	result := l.minimum
	for _, object := range objects {
		if object.Visible() {
			result = result.Max(object.MinSize().AddWidthHeight(l.horizontal*2, l.vertical*2))
		}
	}
	return result
}

func inset(object fyne.CanvasObject, horizontal, vertical float32) *fyne.Container {
	return container.New(insetLayout{horizontal: horizontal, vertical: vertical}, object)
}

type themedPanel struct {
	widget.BaseWidget
	content fyne.CanvasObject
}

func panel(content fyne.CanvasObject) *themedPanel {
	result := &themedPanel{content: inset(content, 11.5, 10)}
	result.ExtendBaseWidget(result)
	return result
}

func (p *themedPanel) CreateRenderer() fyne.WidgetRenderer {
	background := canvas.NewRectangle(p.Theme().Color(surfaceColor, theme.VariantDark))
	background.StrokeWidth = 1
	background.CornerRadius = 20
	r := &panelRenderer{
		panel: p, background: background,
		objects: []fyne.CanvasObject{background, p.content},
	}
	r.Refresh()
	return r
}

type panelRenderer struct {
	panel      *themedPanel
	background *canvas.Rectangle
	objects    []fyne.CanvasObject
}

func (r *panelRenderer) Layout(size fyne.Size) {
	r.background.Resize(size)
	r.panel.content.Resize(size)
}

func (r *panelRenderer) MinSize() fyne.Size { return r.panel.content.MinSize() }

func (r *panelRenderer) Refresh() {
	r.objects[1] = r.panel.content
	t := r.panel.Theme()
	r.background.FillColor = t.Color(surfaceColor, theme.VariantDark)
	r.background.StrokeColor = t.Color(borderColor, theme.VariantDark)
	r.background.Refresh()
	r.Layout(r.panel.Size())
	r.panel.content.Refresh()
	canvas.Refresh(r.panel)
}

func (r *panelRenderer) Objects() []fyne.CanvasObject { return r.objects }
func (r *panelRenderer) Destroy()                     {}

// themedText refreshes its semantic color along with the rest of the widgets.
type themedText struct {
	widget.BaseWidget
	Text         string
	TextSize     float32
	TextStyle    fyne.TextStyle
	Alignment    fyne.TextAlign
	color        fyne.ThemeColorName
	truncate     bool
	minimumWidth float32
	hint         *toolHint
}

func (t *themedText) MouseIn(*fynedesktop.MouseEvent) {
	if t.hint != nil {
		t.hint.show(t)
	}
}
func (t *themedText) MouseMoved(*fynedesktop.MouseEvent) {}
func (t *themedText) MouseOut() {
	if t.hint != nil {
		t.hint.hide()
	}
}

func label(text string, size float32, foreground fyne.ThemeColorName) *themedText {
	result := &themedText{Text: text, TextSize: size, color: foreground}
	result.ExtendBaseWidget(result)
	return result
}

func setLabel(object *themedText, text string, foreground fyne.ThemeColorName) {
	object.Text = text
	object.color = foreground
	object.Refresh()
}

func (t *themedText) CreateRenderer() fyne.WidgetRenderer {
	r := &textRenderer{
		widget: t, text: canvas.NewText(t.Text, t.Theme().Color(t.color, theme.VariantDark)),
	}
	r.objects = []fyne.CanvasObject{r.text}
	r.Refresh()
	return r
}

type textRenderer struct {
	widget  *themedText
	text    *canvas.Text
	objects []fyne.CanvasObject
}

func (r *textRenderer) Layout(size fyne.Size) {
	text := r.widget.Text
	if r.widget.truncate && fyne.MeasureText(text, r.widget.TextSize, r.widget.TextStyle).Width > size.Width {
		runes := []rune(text)
		low, high := 0, len(runes)
		for low < high {
			middle := (low + high + 1) / 2
			candidate := string(runes[:middle]) + "…"
			if fyne.MeasureText(candidate, r.widget.TextSize, r.widget.TextStyle).Width <= size.Width {
				low = middle
			} else {
				high = middle - 1
			}
		}
		text = string(runes[:low]) + "…"
	}
	if r.text.Text != text {
		r.text.Text = text
		r.text.Refresh()
	}
	r.text.Resize(size)
}

func (r *textRenderer) MinSize() fyne.Size {
	size := fyne.MeasureText(r.widget.Text, r.widget.TextSize, r.widget.TextStyle)
	if r.widget.truncate {
		size.Width = 32
		if r.widget.minimumWidth > 0 {
			size.Width = r.widget.minimumWidth
		}
	}
	return size
}

func (r *textRenderer) Refresh() {
	r.text.Text = r.widget.Text
	r.text.TextSize = r.widget.TextSize
	r.text.TextStyle = r.widget.TextStyle
	r.text.Alignment = r.widget.Alignment
	r.text.Color = r.widget.Theme().Color(r.widget.color, theme.VariantDark)
	r.text.Refresh()
	r.Layout(r.widget.Size())
	canvas.Refresh(r.widget)
}

func (r *textRenderer) Objects() []fyne.CanvasObject { return r.objects }
func (r *textRenderer) Destroy()                     {}

func setEnabled(object fyne.Disableable, enabled bool) {
	if enabled {
		object.Enable()
		return
	}
	object.Disable()
}

// waveform has no timer or goroutine of its own. Its owner feeds it on the UI
// thread using recorder snapshots, including zero levels while paused.
type waveform struct {
	widget.BaseWidget
	values        [43]float64
	bars          [43]*canvas.Rectangle
	minimumHeight float32
	position      float64
	seekable      bool
	onSeek        func(float64)
}

func (w *waveform) Tapped(event *fyne.PointEvent) {
	if w.seekable && w.onSeek != nil && w.Size().Width > 0 {
		w.onSeek(boundedLevel(float64(event.Position.X / w.Size().Width)))
	}
}

func (w *waveform) Dragged(event *fyne.DragEvent) { w.Tapped(&event.PointEvent) }
func (w *waveform) DragEnd()                      {}

func newWaveform() *waveform {
	result := &waveform{minimumHeight: 28}
	for i := range result.bars {
		result.bars[i] = canvas.NewRectangle(theme.Color(borderColor))
		result.bars[i].CornerRadius = 2
	}
	result.ExtendBaseWidget(result)
	return result
}

func (w *waveform) push(level float64) {
	copy(w.values[:], w.values[1:])
	w.values[len(w.values)-1] = boundedLevel(level)
	w.Refresh()
}

func (w *waveform) reset() {
	clear(w.values[:])
	w.Refresh()
}

func (w *waveform) CreateRenderer() fyne.WidgetRenderer {
	objects := make([]fyne.CanvasObject, len(w.bars))
	for i, bar := range w.bars {
		objects[i] = bar
	}
	return &waveRenderer{wave: w, objects: objects}
}

type waveRenderer struct {
	wave    *waveform
	objects []fyne.CanvasObject
}

func (r *waveRenderer) Layout(size fyne.Size) {
	step := min(float32(9), max(0, size.Width-12)/float32(len(r.wave.bars)))
	offset := (size.Width - step*float32(len(r.wave.bars)-1)) / 2
	maximum := max(float32(3), size.Height-6)
	for i, bar := range r.wave.bars {
		height := 3 + float32(math.Pow(r.wave.values[i], 0.6))*(maximum-3)
		bar.Move(fyne.NewPos(offset+float32(i)*step-2, (size.Height-height)/2))
		bar.Resize(fyne.NewSize(4, height))
	}
}

func (r *waveRenderer) MinSize() fyne.Size { return fyne.NewSize(160, r.wave.minimumHeight) }

func (r *waveRenderer) Refresh() {
	for i, bar := range r.wave.bars {
		bar.FillColor = r.wave.Theme().Color(borderColor, theme.VariantDark)
		if r.wave.values[i] > 0.01 {
			bar.FillColor = r.wave.Theme().Color(accentColor, theme.VariantDark)
		}
		if r.wave.seekable {
			bar.FillColor = r.wave.Theme().Color(borderColor, theme.VariantDark)
			if float64(i)/float64(len(r.wave.bars)-1) <= r.wave.position {
				bar.FillColor = r.wave.Theme().Color(accentColor, theme.VariantDark)
			}
		}
		bar.Refresh()
	}
	r.Layout(r.wave.Size())
	canvas.Refresh(r.wave)
}

func (r *waveRenderer) Objects() []fyne.CanvasObject { return r.objects }
func (r *waveRenderer) Destroy()                     {}

func boundedLevel(level float64) float64 {
	if math.IsNaN(level) {
		return 0
	}
	return max(0, min(1, level))
}

type progressLine struct {
	widget.BaseWidget
	Value float64
}

func newProgressLine() *progressLine {
	p := &progressLine{}
	p.ExtendBaseWidget(p)
	return p
}

func (p *progressLine) SetValue(value float64) {
	p.Value = boundedLevel(value)
	p.Refresh()
}

func (p *progressLine) CreateRenderer() fyne.WidgetRenderer {
	background := canvas.NewRectangle(p.Theme().Color(borderColor, theme.VariantDark))
	background.CornerRadius = 2
	fill := canvas.NewRectangle(p.Theme().Color(accentColor, theme.VariantDark))
	fill.CornerRadius = 2
	return &progressRenderer{
		progress: p, background: background, fill: fill,
		objects: []fyne.CanvasObject{background, fill},
	}
}

type progressRenderer struct {
	progress   *progressLine
	background *canvas.Rectangle
	fill       *canvas.Rectangle
	objects    []fyne.CanvasObject
}

func (r *progressRenderer) Layout(size fyne.Size) {
	r.background.Resize(size)
	r.fill.Resize(fyne.NewSize(size.Width*float32(r.progress.Value), size.Height))
}

func (r *progressRenderer) MinSize() fyne.Size { return fyne.NewSize(24, 3) }

func (r *progressRenderer) Refresh() {
	r.background.FillColor = r.progress.Theme().Color(borderColor, theme.VariantDark)
	r.fill.FillColor = r.progress.Theme().Color(accentColor, theme.VariantDark)
	r.Layout(r.progress.Size())
	r.background.Refresh()
	r.fill.Refresh()
	canvas.Refresh(r.progress)
}

func (r *progressRenderer) Objects() []fyne.CanvasObject { return r.objects }
func (r *progressRenderer) Destroy()                     {}
