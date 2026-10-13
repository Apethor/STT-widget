package desktop

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// The recorder's widgets keep one owner while its presentation follows the
// active tab. Collapsing only changes the visible surface.
type recorderSurface struct {
	widget.BaseWidget
	desktop   *desktop
	expanded  fyne.CanvasObject
	collapsed fyne.CanvasObject
	activity  *themedText
	stop      *actionButton
	restore   *actionButton
}

func newRecorderSurface(d *desktop, expanded fyne.CanvasObject) *recorderSurface {
	result := &recorderSurface{desktop: d, expanded: expanded}
	result.activity = label("", 12, mutedColor)
	result.activity.truncate = true
	result.activity.minimumWidth = fyne.MeasureText("Gravação pausada", 12, result.activity.TextStyle).Width
	result.stop = d.iconButton("Parar e transcrever", theme.MediaStopIcon(), d.recordAction)
	result.stop.Importance = widget.DangerImportance
	result.restore = d.iconButton("Abrir Gravação", theme.NavigateNextIcon(), d.toggleRecorderPanel)
	result.collapsed = container.NewBorder(nil, nil, label("Gravação", 13, textColor),
		container.New(layout.NewCustomPaddedHBoxLayout(4), result.stop, result.restore),
		container.NewCenter(result.activity))
	result.ExtendBaseWidget(result)
	result.sync()
	return result
}

func (p *recorderSurface) isCollapsed() bool {
	return p.desktop.document != nil && p.desktop.document.panelSizes.RecorderCollapsed
}

func (p *recorderSurface) current() fyne.CanvasObject {
	if p.isCollapsed() {
		return p.collapsed
	}
	return p.expanded
}

func (p *recorderSurface) sync() {
	if p.desktop.capture == captureIdle {
		p.stop.Hide()
		setLabel(p.activity, "", mutedColor)
	} else {
		p.stop.Show()
		setEnabled(p.stop, p.desktop.capture == captureRunning && !p.desktop.discardDialog && !p.desktop.closing)
		text := "Gravação ativa"
		if p.desktop.capture == captureRunning && p.desktop.recorder != nil && p.desktop.recorder.Snapshot().Paused {
			text = "Gravação pausada"
		}
		setLabel(p.activity, text, recordingColor)
	}
}

func (p *recorderSurface) CreateRenderer() fyne.WidgetRenderer {
	return &recorderSurfaceRenderer{surface: p, objects: []fyne.CanvasObject{p.current()}}
}

type recorderSurfaceRenderer struct {
	surface *recorderSurface
	objects []fyne.CanvasObject
}

func (r *recorderSurfaceRenderer) Layout(size fyne.Size)        { r.surface.current().Resize(size) }
func (r *recorderSurfaceRenderer) MinSize() fyne.Size           { return r.surface.current().MinSize() }
func (r *recorderSurfaceRenderer) Objects() []fyne.CanvasObject { return r.objects }
func (r *recorderSurfaceRenderer) Destroy()                     {}
func (r *recorderSurfaceRenderer) Refresh() {
	r.surface.sync()
	r.objects[0] = r.surface.current()
	r.Layout(r.surface.Size())
	r.surface.current().Refresh()
	canvas.Refresh(r.surface)
}

func (d *desktop) syncRecorderSurface() {
	if d.recorderLayout != nil && d.recorderLayout.surface != nil {
		d.recorderLayout.surface.Refresh()
		if d.recorderDivider != nil {
			d.recorderDivider.Refresh()
		} else if d.window.Content() != nil {
			// Showing Copy, Pause or Discard can raise the recorder's
			// minimum height. Its non-column parents own that height.
			d.window.Content().Refresh()
		}
	}
}

func (d *desktop) toggleRecorderPanel() {
	doc := d.document
	if doc == nil || doc.closed {
		return
	}
	d.rememberPanelSizes(doc)
	previous := doc.panelSizes
	doc.panelSizes.RecorderCollapsed = !previous.RecorderCollapsed
	doc.presentationEpoch++
	d.restoreOuterPanelSizes(doc)
	d.syncRecorderSurface()
	if err := d.saveWorkspace(); err != nil {
		doc.panelSizes = previous
		d.restoreOuterPanelSizes(doc)
		d.syncRecorderSurface()
		d.showError("Não foi possível salvar o recolhimento da gravação: "+err.Error(), "")
	}
}
