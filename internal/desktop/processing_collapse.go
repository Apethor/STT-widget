package desktop

import (
	"ai-talker/internal/config"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type collapsiblePanel struct {
	widget.BaseWidget
	desktop       *desktop
	doc           *document
	expanded      fyne.CanvasObject
	column        fyne.CanvasObject
	side          fyne.CanvasObject
	transcription bool
}

func newCollapsiblePanel(d *desktop, doc *document, expanded fyne.CanvasObject) *collapsiblePanel {
	return newDocumentPanel(d, doc, expanded, false)
}

func newTranscriptPanel(d *desktop, doc *document, expanded fyne.CanvasObject) *collapsiblePanel {
	return newDocumentPanel(d, doc, expanded, true)
}

func newDocumentPanel(d *desktop, doc *document, expanded fyne.CanvasObject, transcription bool) *collapsiblePanel {
	title, shortTitle := "Processar texto", "Texto"
	restore := func() { d.toggleProcessingPanel(doc) }
	if transcription {
		title, shortTitle = "Transcrição", "Fala"
		restore = func() { d.toggleTranscriptionPanel(doc) }
	}
	horizontal := panel(container.NewBorder(nil, nil, nil,
		d.iconButton("Abrir "+title, theme.NavigateNextIcon(), restore),
		label(title, 13, textColor)))
	vertical := panel(container.NewVBox(
		d.iconButton("Abrir "+title, theme.NavigateNextIcon(), restore),
		label(shortTitle, 11, mutedColor),
	))
	result := &collapsiblePanel{desktop: d, doc: doc, expanded: expanded, column: horizontal, side: vertical, transcription: transcription}
	result.ExtendBaseWidget(result)
	return result
}

func (p *collapsiblePanel) current() fyne.CanvasObject {
	collapsed := p.doc.processCollapsed
	if p.transcription {
		collapsed = p.doc.transcriptCollapsed
	}
	if !collapsed {
		return p.expanded
	}
	if p.desktop.settings.Appearance.Layout == config.LayoutColumn {
		return p.column
	}
	return p.side
}

func (p *collapsiblePanel) CreateRenderer() fyne.WidgetRenderer {
	return &collapsibleRenderer{panel: p, objects: []fyne.CanvasObject{p.current()}}
}

type collapsibleRenderer struct {
	panel   *collapsiblePanel
	objects []fyne.CanvasObject
}

func (r *collapsibleRenderer) Layout(size fyne.Size)        { r.panel.current().Resize(size) }
func (r *collapsibleRenderer) MinSize() fyne.Size           { return r.panel.current().MinSize() }
func (r *collapsibleRenderer) Objects() []fyne.CanvasObject { return r.objects }
func (r *collapsibleRenderer) Destroy()                     {}
func (r *collapsibleRenderer) Refresh() {
	r.objects[0] = r.panel.current()
	r.Layout(r.panel.Size())
	r.panel.current().Refresh()
	canvas.Refresh(r.panel)
}

func (d *desktop) toggleProcessingPanel(doc *document) {
	d.toggleDocumentPanel(doc, false)
}

func (d *desktop) toggleTranscriptionPanel(doc *document) {
	d.toggleDocumentPanel(doc, true)
}

func (d *desktop) toggleDocumentPanel(doc *document, transcription bool) {
	if doc.closed {
		return
	}
	d.rememberPanelSizes(doc)
	process, transcript, sizes := doc.processCollapsed, doc.transcriptCollapsed, doc.panelSizes
	doc.processOffset = sizes.EditorOffset
	if transcription {
		doc.transcriptCollapsed = !doc.transcriptCollapsed
	} else {
		doc.processCollapsed = !doc.processCollapsed
	}
	doc.presentationEpoch++
	d.arrangeEditorPanels(doc)
	if doc == d.document {
		d.restoreOuterPanelSizes(doc)
	}
	if err := d.saveWorkspace(); err != nil {
		doc.processCollapsed, doc.transcriptCollapsed, doc.panelSizes = process, transcript, sizes
		d.arrangeEditorPanels(doc)
		d.restoreOuterPanelSizes(doc)
		d.showProcessingError(doc, "Não foi possível salvar o recolhimento: "+err.Error())
	}
	d.tabs.Refresh()
}
