package desktop

import (
	"errors"
	"math"

	"ai-talker/internal/config"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const (
	recordingGap          = float32(4)
	recordingWideGap      = float32(8)
	recordingFileGap      = float32(32)
	compactWaveMinWidth   = float32(132)
	compactStatusMinWidth = float32(160)
	compactStatusMaxWidth = float32(240)
)

// The split supplies its current inner width before querying MinSize, allowing
// the column to use a compact or stacked minimum in the same layout pass.
type recordingLayout struct {
	desktop      *desktop
	mode         string
	resizable    bool
	inlineHeader bool
	lastWidth    float32
	objects      []fyne.CanvasObject
	surface      *recorderSurface
}

func (l *recordingLayout) naturalSize(mode string) fyne.Size {
	if len(l.objects) < 6 {
		return fyne.Size{}
	}
	size := l.stackedNaturalSize(mode)
	if mode == config.RecordingSmall && l.wideEnough(l.lastWidth) {
		size.Height = l.wideNaturalSize(mode).Height
	}
	return size
}

func (l *recordingLayout) stackedNaturalSize(mode string) fyne.Size {
	name, tools, state, buttons := l.objects[0].MinSize(), l.objects[1].MinSize(),
		l.objects[3].MinSize(), l.objects[5].MinSize()
	header := l.headerSize(name, tools)
	font, waveHeight := float32(40), float32(40)
	if mode != config.RecordingLarge {
		font, waveHeight = 28, 18
	}
	timer := fyne.MeasureText("00:00", font, l.desktop.timer.TextStyle)
	middleHeight := timer.Height + state.Height + waveHeight + recordingGap*2
	middleWidth := timer.Width
	if mode != config.RecordingLarge {
		middleHeight = max(timer.Height, state.Height+recordingGap+waveHeight)
		middleWidth = timer.Width + 8 + compactWaveMinWidth
	}
	return fyne.NewSize(max(header.Width, middleWidth, buttons.Width),
		header.Height+middleHeight+buttons.Height+recordingGap*2)
}

func (l *recordingLayout) headerSize(name, tools fyne.Size) fyne.Size {
	if l.separateHeader(name, tools) {
		return fyne.NewSize(max(name.Width, tools.Width), name.Height+recordingGap+tools.Height)
	}
	return fyne.NewSize(max(name.Width, tools.Width), max(name.Height, tools.Height))
}

func (l *recordingLayout) separateHeader(name, tools fyne.Size) bool {
	return l.inlineHeader || l.lastWidth > 0 && l.lastWidth < name.Width+tools.Width+recordingGap
}

func (l *recordingLayout) wideNaturalSize(mode string) fyne.Size {
	name, tools, state, buttons := l.objects[0].MinSize(), l.objects[1].MinSize(),
		l.objects[3].MinSize(), l.objects[5].MinSize()
	font, waveHeight := float32(40), float32(40)
	if mode != config.RecordingLarge {
		font, waveHeight = 28, 18
	}
	timer := fyne.MeasureText("00:00", font, l.desktop.timer.TextStyle)
	height := max(
		timer.Height, waveHeight, state.Height, buttons.Height, name.Height, tools.Height,
	)
	if mode == config.RecordingLarge {
		mediumTimer := fyne.MeasureText("00:00", 28, l.desktop.timer.TextStyle)
		mediumHeight := max(
			mediumTimer.Height, float32(18), state.Height, buttons.Height, name.Height, tools.Height,
		)
		height = max(height, mediumHeight+20)
	}
	return fyne.NewSize(l.wideRequiredWidth(), height)
}

func (l *recordingLayout) wideRequiredWidth() float32 {
	if len(l.objects) < 6 {
		return 0
	}
	name, tools, state, buttons := l.objects[0].MinSize(), l.objects[1].MinSize(),
		l.objects[3].MinSize(), l.objects[5].MinSize()
	timer := fyne.MeasureText(l.desktop.timer.Text, 40, l.desktop.timer.TextStyle)
	return timer.Width + 12 + compactWaveMinWidth + buttons.Width +
		name.Width + l.compactStatusWidth(state.Width) + tools.Width +
		recordingWideGap*4 + recordingFileGap
}

func (l *recordingLayout) compactStatusWidth(natural float32) float32 {
	return max(compactStatusMinWidth, min(compactStatusMaxWidth, natural))
}

func (l *recordingLayout) preferredNameWidth() float32 {
	name := l.objects[0].MinSize().Width
	caption := l.desktop.caption
	natural := fyne.MeasureText(caption.Text, caption.TextSize, caption.TextStyle).Width
	return name + max(0, natural-caption.MinSize().Width)
}

func (l *recordingLayout) MinSize([]fyne.CanvasObject) fyne.Size {
	mode := l.mode
	if l.resizable {
		mode = config.RecordingSmall
	}
	return l.naturalSize(mode)
}

func (l *recordingLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	l.lastWidth = size.Width
	if l.mode == config.RecordingSmall && l.wideEnough(size.Width) {
		l.layoutWide(objects, size)
		return
	}
	l.layoutStacked(objects, size)
}

func (l *recordingLayout) layoutStacked(objects []fyne.CanvasObject, size fyne.Size) {
	name, tools, timer, state, wave, buttons := objects[0], objects[1], objects[2],
		objects[3], objects[4], objects[5]
	nameSize, toolsSize := name.MinSize(), tools.MinSize()
	headerSize := l.headerSize(nameSize, toolsSize)
	headerHeight, buttonsHeight := headerSize.Height, buttons.MinSize().Height
	place := func(object fyne.CanvasObject, x, y, width, height float32) {
		object.Move(fyne.NewPos(x, y))
		object.Resize(fyne.NewSize(max(0, width), max(0, height)))
	}
	if l.separateHeader(nameSize, toolsSize) {
		place(name, 0, 0, size.Width, nameSize.Height)
		place(tools, 0, nameSize.Height+recordingGap, size.Width, toolsSize.Height)
	} else {
		toolsWidth := min(size.Width, toolsSize.Width)
		place(tools, size.Width-toolsWidth, 0, toolsWidth, toolsSize.Height)
		nameWidth := max(0, size.Width-toolsWidth-recordingGap)
		place(name, 0, max(0, (headerHeight-nameSize.Height)/2), nameWidth, nameSize.Height)
	}
	place(buttons, 0, size.Height-buttonsHeight, size.Width, buttonsHeight)
	top := headerHeight + recordingGap
	height := max(0, size.Height-headerHeight-buttonsHeight-recordingGap*2)
	stateHeight := state.MinSize().Height
	extra := max(0, size.Height-l.naturalSize(l.mode).Height)
	font := float32(40) + min(32, extra/5)
	timerWidth := size.Width
	if l.mode != config.RecordingLarge {
		font = 28 + min(10, extra/6)
		timerWidth = min(size.Width*0.42, max(0, size.Width-compactWaveMinWidth-8))
	}
	measured := fyne.MeasureText(l.desktop.timer.Text, font, l.desktop.timer.TextStyle)
	if measured.Width > timerWidth && measured.Width > 0 {
		font *= timerWidth / measured.Width
	}
	if math.Abs(float64(l.desktop.timer.TextSize-font)) > 0.1 {
		l.desktop.timer.TextSize = font
		l.desktop.timer.Refresh()
	}
	if l.mode != config.RecordingLarge {
		place(timer, 0, top, timerWidth, height)
		right, width := timerWidth+8, size.Width-timerWidth-8
		nameWidth := l.preferredNameWidth()
		if l.mode == config.RecordingMedium && !l.inlineHeader &&
			size.Width >= nameWidth+toolsSize.Width+l.compactStatusWidth(state.MinSize().Width)+recordingGap*4 {
			stateWidth := size.Width - nameWidth - toolsSize.Width - recordingGap*2
			place(name, 0, max(0, (headerHeight-nameSize.Height)/2), nameWidth, nameSize.Height)
			place(state, nameWidth+recordingGap, max(0, (headerHeight-stateHeight)/2), stateWidth, stateHeight)
			place(wave, right, top, width, height)
		} else {
			place(wave, right, top, width, height-stateHeight-recordingGap)
			place(state, right, top+height-stateHeight, width, stateHeight)
		}
		return
	}
	waveHeight := min(float32(110), 40+extra*0.35)
	middleHeight := max(0, height-stateHeight-waveHeight-recordingGap*2)
	place(timer, 0, top, timerWidth, middleHeight)
	top += middleHeight + recordingGap
	place(state, 0, top, size.Width, stateHeight)
	place(wave, 0, top+stateHeight+recordingGap, size.Width, waveHeight)
}

func (l *recordingLayout) wideEnough(width float32) bool {
	return len(l.objects) >= 6 && width+0.5 >= l.wideRequiredWidth()
}

func (l *recordingLayout) layoutWide(objects []fyne.CanvasObject, size fyne.Size) {
	name, tools, timer, state, wave, buttons := objects[0], objects[1], objects[2],
		objects[3], objects[4], objects[5]
	font := float32(28)
	if l.mode == config.RecordingLarge {
		font = 40
	}
	extra := max(0, size.Height-l.wideNaturalSize(l.mode).Height)
	font = min(72, font+extra/5)
	font = min(font, max(18, size.Height-8))
	nameSize, toolsSize := name.MinSize(), tools.MinSize()
	stateSize, buttonsSize := state.MinSize(), buttons.MinSize()
	stateMinWidth := l.compactStatusWidth(stateSize.Width)
	timerSize := fyne.MeasureText(l.desktop.timer.Text, font, l.desktop.timer.TextStyle)
	fixedWidth := compactWaveMinWidth + buttonsSize.Width + nameSize.Width +
		stateMinWidth + toolsSize.Width + recordingWideGap*4 + recordingFileGap
	timerMaxWidth := max(float32(18), size.Width-fixedWidth-12)
	if timerSize.Width > timerMaxWidth && timerSize.Width > 0 {
		font *= timerMaxWidth / timerSize.Width
		font = max(18, font)
	}
	if math.Abs(float64(l.desktop.timer.TextSize-font)) > 0.1 {
		l.desktop.timer.TextSize = font
		l.desktop.timer.Refresh()
	}
	timerSize = fyne.MeasureText(l.desktop.timer.Text, font, l.desktop.timer.TextStyle)
	timerWidth := timerSize.Width + 12
	minimumWidth := timerWidth + fixedWidth
	extraWidth := max(0, size.Width-minimumWidth)
	nameWidth := nameSize.Width + extraWidth*0.10
	stateWidth := stateMinWidth + extraWidth*0.10
	waveWidth := compactWaveMinWidth + extraWidth*0.55
	buttonsWidth := buttonsSize.Width + extraWidth*0.25
	buttonsHeight := max(buttonsSize.Height, min(56, size.Height))
	place := func(object fyne.CanvasObject, x, y, width, height float32) {
		object.Move(fyne.NewPos(x, y))
		object.Resize(fyne.NewSize(max(0, width), max(0, height)))
	}
	place(name, 0, max(0, (size.Height-nameSize.Height)/2), nameWidth, nameSize.Height)
	timerX := nameWidth + recordingWideGap
	place(timer, timerX, max(0, (size.Height-timerSize.Height)/2), timerWidth, timerSize.Height)
	waveX := timerX + timerWidth + 8
	waveHeight := min(110, max(float32(18), size.Height))
	place(wave, waveX, max(0, (size.Height-waveHeight)/2), waveWidth, waveHeight)
	buttonsX := waveX + waveWidth + 8
	place(buttons, buttonsX, max(0, (size.Height-buttonsHeight)/2), buttonsWidth, buttonsHeight)
	stateX := buttonsX + buttonsWidth + recordingFileGap
	place(state, stateX, max(0, (size.Height-stateSize.Height)/2), stateWidth, stateSize.Height)
	toolsX := stateX + stateWidth + recordingWideGap
	place(tools, toolsX, max(0, (size.Height-toolsSize.Height)/2), toolsSize.Width, toolsSize.Height)
}

func (d *desktop) resizeRecordingPanel(mode string) {
	if d.recorderLayout == nil {
		return
	}
	d.recorderLayout.mode = mode
	if d.document != nil {
		d.document.panelSizes.RecorderMode = mode
		d.document.panelSizes.RecorderHeight = d.recorderLayout.naturalSize(mode).Height + 20
	}
	d.syncRecordingSizeButton()
	if d.recorderDivider == nil {
		d.recordPanel.Refresh()
		if d.window.Content() != nil {
			d.window.Content().Refresh()
		}
		return
	}
	available := d.rowSplit.Size().Height - d.recorderDivider.dividerHeight()
	if available <= 0 {
		return
	}
	height := d.recorderLayout.naturalSize(mode).Height + 20
	required := height + d.rowSplit.Trailing.MinSize().Height
	if required > available && d.columnHandle != nil {
		d.columnHandle.drag(required - available)
		available = d.rowSplit.Size().Height - d.recorderDivider.dividerHeight()
	}
	if d.document == nil || !d.document.panelSizes.RecorderCollapsed {
		d.rowSplit.SetOffset(float64(height / available))
	}
	d.recorderDivider.Refresh()
}

func (d *desktop) dragRecordingDivider(delta float32) {
	split := d.rowSplit
	available := split.Size().Height - d.recorderDivider.dividerHeight()
	if d.document != nil && d.document.panelSizes.RecorderCollapsed {
		return
	}
	minimum := d.recorderLayout.naturalSize(config.RecordingSmall).Height + 20
	maximum := available - split.Trailing.MinSize().Height
	if maximum < minimum || available <= 0 {
		return
	}
	height := max(minimum, min(maximum, split.Leading.Size().Height+delta))
	mode := config.RecordingSmall
	// A narrow Small layout can need as much height as Medium. Its temporary
	// minimum must not change the chosen mode just because the width changes.
	if height > minimum+0.5 && height >= d.recorderLayout.stackedNaturalSize(config.RecordingMedium).Height+20 {
		mode = config.RecordingMedium
	}
	if height >= d.recorderLayout.stackedNaturalSize(config.RecordingLarge).Height+20 {
		mode = config.RecordingLarge
	}
	d.recordingResize = true
	d.recorderLayout.mode = mode
	if d.document != nil {
		d.document.panelSizes.RecorderMode = mode
		d.document.panelSizes.RecorderHeight = height
	}
	d.syncRecordingSizeButton()
	split.SetOffset(float64(height / available))
	d.recorderDivider.Refresh()
}

func (d *desktop) finishRecordingResize() {
	if !d.recordingResize {
		return
	}
	d.rememberPanelSizes(d.document)
	if err := d.saveWorkspace(); err != nil {
		d.showError("Não foi possível salvar a divisória da gravação: "+err.Error(), "")
	}
	mode := d.recorderLayout.mode
	if mode == d.settings.Appearance.RecordingSize {
		d.recordingResize = false
		return
	}
	updated := cloneSettings(d.settings)
	updated.Appearance.RecordingSize = mode
	d.persistPreferences(updated, func(err error) {
		if err != nil {
			d.resizeRecordingPanel(d.settings.Appearance.RecordingSize)
			err = errors.Join(err, d.saveWorkspace())
			d.showError("Não foi possível salvar o tamanho da gravação: "+err.Error(), "")
		}
	})
	d.recordingResize = false
}

// The grip overlays the standard split's divider, preserving its offset and
// sizing API while owning drag completion and the recorder's mode transition.
type recordingSplitSurface struct {
	widget.BaseWidget
	desktop *desktop
	split   *container.Split
	grip    *heightHandle
}

func newRecordingSplitSurface(d *desktop) *recordingSplitSurface {
	surface := &recordingSplitSurface{desktop: d, split: d.rowSplit}
	surface.grip = newHeightHandle(d.hints, d.dragRecordingDivider)
	surface.grip.hint.text = "Arrastar para ajustar a gravação · Pequeno, Médio ou Grande pela altura"
	surface.grip.ended = d.finishRecordingResize
	surface.ExtendBaseWidget(surface)
	return surface
}

func (s *recordingSplitSurface) dividerHeight() float32 {
	return max(5, s.Theme().Size(theme.SizeNameSplitThickness))
}

func (s *recordingSplitSurface) CreateRenderer() fyne.WidgetRenderer {
	return &recordingSplitRenderer{
		surface: s, objects: []fyne.CanvasObject{s.split, s.grip},
	}
}

type recordingSplitRenderer struct {
	surface *recordingSplitSurface
	objects []fyne.CanvasObject
}

func (r *recordingSplitRenderer) Layout(size fyne.Size) {
	split := r.surface.split
	if r.surface.desktop.recorderLayout != nil {
		// The split asks its children for minimum height before laying them
		// out. Supply the current body width first so the recorder can switch
		// between the compact and stacked minimums in the same pass.
		r.surface.desktop.recorderLayout.lastWidth = max(0, size.Width-36)
	}
	d := r.surface.desktop
	if doc := d.document; doc != nil {
		available := size.Height - r.surface.dividerHeight()
		if available > 0 {
			height := doc.panelSizes.RecorderHeight
			if doc.panelSizes.RecorderCollapsed {
				height = d.recordPanel.MinSize().Height
			} else if height == 0 {
				height = d.recorderLayout.naturalSize(d.recorderLayout.mode).Height + 20
				if doc.panelSizes.RecorderOffset > 0 {
					height = max(height, float32(doc.panelSizes.RecorderOffset)*available)
				}
				doc.panelSizes.RecorderHeight = height
			}
			if height > 0 && !(doc.processCollapsed && doc.transcriptCollapsed && !doc.panelSizes.RecorderCollapsed) {
				minimum := d.recordPanel.MinSize().Height
				if !doc.panelSizes.RecorderCollapsed {
					minimum = max(minimum, d.recorderLayout.naturalSize(d.recorderLayout.mode).Height+20)
				}
				height = max(minimum, height)
				split.Offset = min(1, float64(height/available))
			}
		}
	}
	split.Resize(size)
	r.surface.grip.Move(fyne.NewPos(0, split.Leading.Size().Height))
	r.surface.grip.Resize(fyne.NewSize(size.Width, r.surface.dividerHeight()))
}

func (r *recordingSplitRenderer) MinSize() fyne.Size {
	d := r.surface.desktop
	size := r.surface.split.MinSize()
	height := d.recordPanel.MinSize().Height
	if d.document == nil || !d.document.panelSizes.RecorderCollapsed {
		height = max(height, d.recorderLayout.naturalSize(d.recorderLayout.mode).Height+20)
		if d.document != nil {
			height = max(height, d.document.panelSizes.RecorderHeight)
		}
	}
	// The split itself keeps Small as its draggable minimum. The scroll
	// surface must also reserve the chosen height, leaving every editor its
	// own minimum even when status/error labels wrap at a narrower width.
	size.Height = max(size.Height, height+r.surface.split.Trailing.MinSize().Height+r.surface.dividerHeight())
	return size
}

func (r *recordingSplitRenderer) Refresh() {
	r.surface.split.Refresh()
	r.Layout(r.surface.Size())
	r.surface.grip.Refresh()
	canvas.Refresh(r.surface)
}

func (r *recordingSplitRenderer) Objects() []fyne.CanvasObject { return r.objects }
func (r *recordingSplitRenderer) Destroy()                     {}
