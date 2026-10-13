package desktop

import (
	"ai-talker/internal/config"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

type transcribeSetupForm struct {
	dialog    *dialog.CustomDialog
	configure *widget.Button
	direct    *widget.Button
}

func (d *desktop) ensureTranscriptionSetup(doc *document, settings config.Settings, resume func()) bool {
	if err := settings.TranscriptionSetupError(); err != nil {
		d.showDocumentError(doc, err.Error(), "")
		if doc.lastAudio != "" {
			doc.retryButton.Show()
		}
		d.syncControls()
		generation, source := doc.generation, doc.lastAudio
		d.openTranscribeSetup(func() {
			if !d.closing && !doc.closed && doc.generation == generation && doc.lastAudio == source &&
				d.capture == captureIdle {
				resume()
			}
		})
		return false
	}
	return true
}

func (d *desktop) openTranscribeSetup(resume func()) {
	if d.transcribeSetup != nil {
		d.transcribeSetup.dialog.Show()
		return
	}
	form := &transcribeSetupForm{}
	message := widget.NewLabel(
		"Amazon Transcribe · arquivo envia o áudio para um bucket S3 existente na região da AWS selecionada.\n\n" +
			"Para reconhecer este mesmo áudio sem configurar um bucket, escolha Amazon Transcribe · streaming. " +
			"Ele usa conexão direta e também aceita gravações e arquivos importados.",
	)
	message.Wrapping = fyne.TextWrapWord
	form.configure = widget.NewButton(uiText("Configurar bucket S3"), d.guard(func() {
		form.dialog.Hide()
		d.openCloudBucketSettings()
	}))
	directLabel := "Usar Transcribe sem S3"
	if resume != nil {
		directLabel = "Usar sem S3 e tentar novamente"
	}
	form.direct = widget.NewButton(directLabel, d.guard(func() {
		form.direct.Disable()
		form.configure.Disable()
		updated := cloneSettings(d.settings)
		updated.Provider = config.ProviderBedrock
		updated.Cloud.ModelID = config.TranscribeStreaming
		d.persistPreferences(updated, func(err error) {
			if d.transcribeSetup != form || d.closing {
				return
			}
			if err != nil {
				form.direct.Enable()
				form.configure.Enable()
				d.showError(err.Error(), "")
				return
			}
			form.dialog.Hide()
			if resume != nil {
				resume()
			}
		})
	}))
	form.direct.Importance = widget.HighImportance
	form.dialog = dialog.NewCustom(
		uiText("Preparar Amazon Transcribe"), uiText("Cancelar"),
		container.NewVBox(message, form.configure, form.direct), d.window,
	)
	form.dialog.SetOnClosed(func() {
		if d.transcribeSetup == form {
			d.transcribeSetup = nil
		}
	})
	d.transcribeSetup = form
	form.dialog.Resize(fyne.NewSize(min(float32(440), d.window.Canvas().Size().Width-24), 260))
	form.dialog.Show()
}

func (d *desktop) openCloudBucketSettings() {
	d.openSettings()
	form := d.settingsForm
	setUISelected(form.quality, cloudChoiceName(config.TranscribeBatch))
	form.sections.selectID("connections/aws")
	if form.cloudUseBucket.Checked {
		d.window.Canvas().Focus(form.cloudBucket)
	} else {
		d.window.Canvas().Focus(form.cloudUseBucket)
	}
}
