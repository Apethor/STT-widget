package desktop

import (
	"math"

	"ai-talker/internal/config"
	"ai-talker/internal/platform"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const (
	stackedChoice = "Gravação acima dos editores"
	inlineChoice  = "Três boxes lado a lado"
	columnChoice  = "Uma coluna: gravação, transcrição e processamento"
	largeChoice   = "Grande"
	mediumChoice  = "Médio"
	smallChoice   = "Pequeno"
)

func themeName(mode string) string {
	switch mode {
	case config.ThemeLight:
		return "Claro"
	case config.ThemeDark:
		return "Escuro"
	default:
		return "Verde"
	}
}

func (d *desktop) syncThemeButton() {
	mode := d.settings.Appearance.Theme
	d.themeButton.dotColor = paletteFor(mode).surface
	if mode == config.ThemeGreen {
		d.themeButton.dotColor = rgb(0x79deb5)
	}
	d.themeButton.setHint("Mudar tema · " + themeName(mode))
	d.themeButton.Refresh()
}

func (d *desktop) syncRecordingSizeButton() {
	mode := d.settings.Appearance.RecordingSize
	if d.recorderLayout != nil {
		mode = d.recorderLayout.mode
	}
	d.sizeButton.SetIcon(theme.ViewRestoreIcon())
	d.sizeButton.setHint(uiText("Reduzir gravação para tamanho Médio"))
	if mode == config.RecordingMedium {
		d.sizeButton.SetIcon(theme.ZoomOutIcon())
		d.sizeButton.setHint(uiText("Reduzir gravação para tamanho Pequeno"))
	} else if mode == config.RecordingSmall {
		d.sizeButton.SetIcon(theme.ViewFullScreenIcon())
		d.sizeButton.setHint(uiText("Ampliar gravação para tamanho Grande"))
	}
}

func (d *desktop) toggleRecordingSize() {
	updated := cloneSettings(d.settings)
	updated.Appearance.RecordingSize = config.RecordingMedium
	if d.recorderLayout.mode == config.RecordingMedium {
		updated.Appearance.RecordingSize = config.RecordingSmall
	} else if d.recorderLayout.mode == config.RecordingSmall {
		updated.Appearance.RecordingSize = config.RecordingLarge
	}
	if updated.Appearance.RecordingSize == d.settings.Appearance.RecordingSize {
		d.resizeRecordingPanel(updated.Appearance.RecordingSize)
		return
	}
	d.persistPreferences(updated, func(err error) {
		if err != nil {
			d.showError(err.Error(), "")
		}
	})
}

func (d *desktop) chooseTheme() {
	items := make([]*fyne.MenuItem, 0, 3)
	for _, mode := range []string{config.ThemeLight, config.ThemeDark, config.ThemeGreen} {
		item := fyne.NewMenuItem(uiText(themeName(mode)), d.guard(func() { d.setTheme(mode) }))
		item.Checked = mode == d.settings.Appearance.Theme
		items = append(items, item)
	}
	menu := widget.NewPopUpMenu(fyne.NewMenu(uiText("Tema"), items...), d.window.Canvas())
	menu.ShowAtRelativePosition(fyne.NewPos(0, d.themeButton.Size().Height), d.themeButton)
}

func (d *desktop) setTheme(mode string) {
	if d.settings.Appearance.Theme == mode {
		return
	}
	updated := cloneSettings(d.settings)
	updated.Appearance.Theme = mode
	d.persistPreferences(updated, func(err error) {
		if err != nil {
			d.showError(err.Error(), "")
		}
	})
}

func (d *desktop) applyAppearance(previous, next config.Appearance) {
	if previous.EditorFontFamily() != next.EditorFontFamily() ||
		previous.EditorFontSize() != next.EditorFontSize() {
		for _, doc := range d.documents {
			doc.editor.Refresh()
			doc.processEditor.Refresh()
			doc.transcriptBody.Refresh()
			doc.processBody.Refresh()
		}
	}
	if previous.Layout != next.Layout || previous.InvertPanels != next.InvertPanels {
		d.rememberPanelSizesWithOrder(d.document, previous.InvertPanels)
	}
	if previous.Theme != next.Theme {
		d.palette.setMode(next.Theme)
		d.application.Settings().SetTheme(d.palette)
		d.syncThemeButton()
		d.brandIcon.Resource = headerIcon(next.Theme)
		d.brandIcon.Refresh()
		d.window.Content().Refresh()
	}
	if previous.Layout != next.Layout || previous.RecordingSize != next.RecordingSize || previous.InvertPanels != next.InvertPanels {
		for _, doc := range d.documents {
			d.arrangeDocumentEditors(doc)
		}
		d.syncRecordingSizeButton()
		d.recordPanel.content = inset(d.recordingContent(), 18, 10)
		d.syncRecordingSizeButton()
		d.recordPanel.Refresh()
		d.arrangePanels()
		d.restoreOuterPanelSizes(d.document)
		if !d.recordingResize {
			d.resizeRecordingPanel(next.RecordingSize)
		}
		d.window.Resize(d.window.Canvas().Size().Max(d.window.Content().MinSize()))
	}
	d.updateTitlebar()
}

func (d *desktop) initialWindowSize() fyne.Size {
	if d.settings.Appearance.Layout == config.LayoutInline {
		return fyne.NewSize(1280, 740)
	}
	if d.settings.Appearance.Layout == config.LayoutColumn {
		return fyne.NewSize(480, 900)
	}
	return fyne.NewSize(1000, 740)
}

func (d *desktop) arrangeDocumentEditors(doc *document) {
	column := d.settings.Appearance.Layout == config.LayoutColumn
	doc.editors.Horizontal = !column
	rows := 3
	if column {
		rows = 2
	}
	doc.editor.SetMinRowsVisible(rows)
	doc.processEditor.SetMinRowsVisible(rows)
	d.arrangeEditorPanels(doc)
	doc.editors.Refresh()
}

func (d *desktop) recordingContent() fyne.CanvasObject {
	inline := d.settings.Appearance.Layout == config.LayoutInline
	state := container.NewThemeOverride(d.recordState, textTheme{Theme: d.palette, size: 12})
	buttons := container.NewBorder(nil, nil, nil,
		container.New(layout.NewCustomPaddedHBoxLayout(4),
			d.playbackSpeedButton, d.pauseButton, d.stopPlaybackButton, d.outputVolumeButton, d.discardButton), d.recordButton)
	tools := container.New(layout.NewCustomPaddedHBoxLayout(4),
		d.newButton, d.importButton, d.audioHistoryButton, d.deviceButton,
		d.captureVolumes.button, d.streamButton, d.sizeButton,
		d.iconButton("Recolher Gravação", theme.NavigateBackIcon(), d.toggleRecorderPanel))
	name := container.NewBorder(nil, nil, d.copyAudioPath, d.openVideoButton, d.caption)
	d.recorderLayout = &recordingLayout{
		desktop: d, mode: d.recordingMode(d.document), inlineHeader: inline,
		resizable: d.settings.Appearance.Layout == config.LayoutColumn,
	}
	body := container.New(d.recorderLayout, name, tools, d.timer, state, d.wave, buttons)
	d.recorderLayout.objects = body.Objects
	if d.document != nil && d.document.panelSizes.RecorderHeight == 0 && d.document.panelSizes.RecorderOffset == 0 {
		d.document.panelSizes.RecorderHeight = d.recorderLayout.naturalSize(d.recorderLayout.mode).Height + 20
	}
	content := container.NewThemeOverride(body, headerTheme{Theme: d.palette})
	d.recorderLayout.surface = newRecorderSurface(d, content)
	return d.recorderLayout.surface
}

func (d *desktop) arrangePanels() {
	minimum := fyne.NewSize(840, 700)
	horizontalInset := float32(20)
	previousSplit, previousScroll := d.rowSplit, d.columnScroll
	d.columnScroll = nil
	d.columnHandle = nil
	d.recorderDivider = nil
	var body fyne.CanvasObject
	switch d.settings.Appearance.Layout {
	case config.LayoutInline:
		minimum = fyne.NewSize(1120, 620)
		// The recording box shares the editor row but keeps its natural height.
		// Its controls have one owner even when the selected document changes.
		recording := container.New(recordingColumnLayout{tabs: d.tabs}, d.recordPanel)
		d.rowSplit = container.NewHSplit(recording, d.tabs)
		d.rowSplit.Offset = 0.25
		if previousSplit != nil && previousSplit.Horizontal {
			d.rowSplit.Offset = previousSplit.Offset
		}
		body = container.NewBorder(d.header, d.footer, nil, nil, d.rowSplit)
	case config.LayoutColumn:
		minimum = fyne.NewSize(320, 700)
		horizontalInset = 12
		d.rowSplit = container.NewVSplit(d.recordPanel, d.tabs)
		d.rowSplit.Offset = float64(d.recordPanel.MinSize().Height / d.rowSplit.MinSize().Height)
		if previousSplit != nil && !previousSplit.Horizontal {
			d.rowSplit.Offset = previousSplit.Offset
		}
		d.recorderDivider = newRecordingSplitSurface(d)
		d.columnScroll, d.columnHandle = newResizableColumn(d.recorderDivider, d.hints, d.columnHeight,
			func(height float32) {
				d.columnHeight = height
				if d.document != nil {
					d.document.panelSizes.ColumnHeight = height
					d.document.presentationEpoch++
				}
			})
		d.columnHandle.ended = func() {
			if err := d.saveWorkspace(); err != nil {
				d.showError("Não foi possível salvar o tamanho dos painéis: "+err.Error(), "")
			}
		}
		if previousScroll != nil {
			d.columnScroll.Offset = previousScroll.Offset
		}
		body = container.NewBorder(d.header, d.footer, nil, nil,
			d.columnScroll,
		)
	default:
		d.rowSplit = nil
		body = container.NewBorder(container.NewVBox(d.header, d.recordPanel), d.footer, nil, nil, d.tabs)
	}
	d.content = container.New(insetLayout{
		horizontal: horizontalInset, vertical: 8, minimum: minimum,
	}, body)
	d.hints.hide()
	d.window.SetContent(container.New(&settingsResizeLayout{desktop: d}, container.NewStack(d.content, d.hints.content)))
}

type recordingColumnLayout struct{ tabs *documentTabs }

func (l recordingColumnLayout) top() float32 {
	return l.tabs.tabBarHeight()
}

func (l recordingColumnLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, object := range objects {
		object.Move(fyne.NewPos(0, l.top()))
		object.Resize(fyne.NewSize(size.Width, object.MinSize().Height))
	}
}

func (l recordingColumnLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	result := fyne.Size{}
	for _, object := range objects {
		result = result.Max(object.MinSize())
	}
	result.Height += l.top()
	return result
}

func (d *desktop) updateTitlebar() {
	if (d.deps.darkTitlebar == nil && d.deps.windowFrame == nil) || !d.started {
		return
	}
	dark := d.settings.Appearance.Theme != config.ThemeLight
	background := paletteFor(d.settings.Appearance.Theme).background
	header := d.windowHeader()
	epoch := d.preferenceEpoch.Load()
	d.operation(func() error {
		d.preferenceMu.Lock()
		defer d.preferenceMu.Unlock()
		if epoch != d.preferenceEpoch.Load() {
			return nil
		}
		if d.deps.windowFrame != nil {
			return d.deps.windowFrame(dark, background, header)
		}
		return d.deps.darkTitlebar(dark)
	}, func(error) {})
}

// Native hit testing uses physical pixels. Keep the draggable title separate
// from the toolbar, including when the window is narrow or uses display scaling.
func (d *desktop) windowHeader() platform.WindowHeader {
	scale := d.window.Canvas().Scale()
	if scale <= 0 {
		scale = 1
	}
	position := d.application.Driver().AbsolutePositionForObject(d.header)
	tools := d.application.Driver().AbsolutePositionForObject(d.headerTools)
	pixels := func(value float32) int { return int(math.Round(float64(value * scale))) }
	return platform.WindowHeader{
		Top:           pixels(position.Y),
		Bottom:        pixels(position.Y + d.header.Size().Height),
		ControlsWidth: pixels(d.window.Canvas().Size().Width - tools.X + 4),
	}
}
