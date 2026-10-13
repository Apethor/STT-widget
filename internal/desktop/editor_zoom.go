package desktop

import (
	"fmt"
	"math"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type editorZoomForm struct {
	popup     *widget.PopUp
	slider    *resetSlider
	value     *widget.Label
	syncing   bool
	adjusting bool
	saved     int
}

func normalizedEditorZoom(value int) int {
	if value < 75 || value > 200 {
		return 100
	}
	if value == 75 || value == 80 || value == 90 || value == 100 ||
		value >= 110 && value <= 200 && value%10 == 0 {
		return value
	}
	return int(math.Round(float64(value)/10) * 10)
}

func validEditorZoom(value int) bool {
	if value == 0 {
		return true
	}
	return normalizedEditorZoom(value) == value
}

func (d *desktop) editorZoomButton(doc *document, transcript bool) *actionButton {
	name := "Transcrição"
	if !transcript {
		name = "Processar texto"
	}
	button := d.iconButton("Zoom de "+name, theme.SearchIcon(), func() {
		d.openEditorZoom(doc, transcript)
	})
	return button
}

func (d *desktop) editorZoomValue(doc *document, transcript bool) int {
	if transcript {
		return normalizedEditorZoom(doc.transcriptZoom)
	}
	return normalizedEditorZoom(doc.processZoom)
}

func (d *desktop) syncEditorZoom(doc *document, transcript bool) {
	if doc == nil {
		return
	}
	name, button := "Processar texto", doc.processZoomButton
	if transcript {
		name, button = "Transcrição", doc.transcriptZoomButton
	}
	if button != nil {
		button.setHint(fmt.Sprintf("Zoom de %s · %d%%", name, d.editorZoomValue(doc, transcript)))
	}
	if transcript {
		if doc.transcriptBody != nil {
			doc.transcriptBody.Refresh()
		}
	} else if doc.processBody != nil {
		doc.processBody.Refresh()
	}
	if doc == d.document {
		d.window.Content().Refresh()
	}
}

func (d *desktop) openEditorZoom(doc *document, transcript bool) {
	if doc == nil || doc.closed || d.closing || d.discardDialog {
		return
	}
	form := doc.processZoomForm
	anchor := doc.processZoomButton
	if transcript {
		form, anchor = doc.transcriptZoomForm, doc.transcriptZoomButton
	}
	if form != nil && form.popup.Visible() {
		form.popup.Hide()
		return
	}

	current := d.editorZoomValue(doc, transcript)
	form = &editorZoomForm{
		slider: newResetSlider(75, 200, 100),
		value:  widget.NewLabel(fmt.Sprintf("%d%%", current)),
		saved:  current,
	}
	form.slider.Step = 10
	form.slider.SetValue(float64(current))
	name := "Transcrição"
	if !transcript {
		name = "Processar texto"
	}
	reset := widget.NewButton(uiText("Restaurar 100%"), func() { form.slider.SetValue(100) })
	content := container.NewVBox(
		settingLabel("Zoom · "+name),
		container.NewBorder(nil, nil, nil, form.value, form.slider),
		reset,
	)
	form.popup = widget.NewPopUp(content, d.window.Canvas())
	if transcript {
		doc.transcriptZoomForm = form
	} else {
		doc.processZoomForm = form
	}
	form.slider.OnChanged = func(value float64) {
		if form.syncing {
			return
		}
		form.adjusting = true
		zoom := normalizedEditorZoom(int(math.Round(value)))
		form.value.SetText(fmt.Sprintf("%d%%", zoom))
		if transcript {
			doc.transcriptZoom = zoom
		} else {
			doc.processZoom = zoom
		}
		d.syncEditorZoom(doc, transcript)
	}
	form.slider.OnChangeEnded = func(value float64) {
		if form.syncing {
			return
		}
		form.adjusting = false
		zoom := normalizedEditorZoom(int(math.Round(value)))
		if transcript {
			doc.transcriptZoom = zoom
		} else {
			doc.processZoom = zoom
		}
		if err := d.saveWorkspace(); err != nil {
			if transcript {
				doc.transcriptZoom = form.saved
			} else {
				doc.processZoom = form.saved
			}
			form.syncing = true
			form.slider.SetValue(float64(form.saved))
			form.syncing = false
			form.value.SetText(fmt.Sprintf("%d%%", form.saved))
			d.syncEditorZoom(doc, transcript)
			d.showDocumentError(doc, "Não foi possível salvar o zoom da aba: "+err.Error(), "")
			return
		}
		form.saved = zoom
		d.syncEditorZoom(doc, transcript)
	}
	form.popup.Resize(fyne.NewSize(min(float32(260), d.window.Canvas().Size().Width-32), content.MinSize().Height))
	d.positionShortcut(form.popup, anchor)
}
