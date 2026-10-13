package desktop

import (
	"ai-talker/internal/config"
	"ai-talker/internal/transcript"

	"fyne.io/fyne/v2/dialog"
)

type captureOrigin struct {
	document          *document
	generation        uint64
	source            string
	result            *transcript.Result
	append            *recordedAppend
	sourceLabel       string
	recordingSource   string
	screenshots       []string
	ignoreScreenshots bool
	live              *liveRecording
	settings          config.Settings
	previousLive      *liveRecording
	transcriptionRun  string
}

func (d *desktop) requestDiscardRecording() {
	if d.capture != captureRunning || d.discardDialog || d.closeDialog {
		return
	}
	d.discardDialog = true
	d.syncControls()
	complete := func(confirmed bool) {
		d.guard(func() {
			d.discardDialog = false
			if confirmed && d.capture == captureRunning {
				d.stopCapture(discardCapture)
			}
			d.syncControls()
		})()
	}
	if d.deps.confirmDiscard != nil {
		d.deps.confirmDiscard(d.window, complete)
		return
	}
	confirmation := dialog.NewConfirm(
		uiText("Descartar gravação"),
		uiText("Encerrar esta gravação sem transcrever?\nO texto que já está na aba será mantido."),
		complete, d.window,
	)
	confirmation.SetConfirmText(uiText("Descartar"))
	confirmation.SetDismissText(uiText("Continuar gravando"))
	confirmation.Show()
}

func (d *desktop) restoreDiscardedCapture(doc *document, origin *captureOrigin) {
	if origin != nil && origin.live != nil && origin.live.show {
		// Discard retains completed live text and cancels outstanding requests.
		d.cancelLiveRecording(origin.live)
		doc.busy = false
		doc.jobCancel = nil
		doc.retryButton.Hide()
		doc.view = defaultDocumentView()
		doc.view.recordText = "Gravação encerrada."
		doc.view.status = "O texto recebido durante o stream foi mantido."
		d.syncDocumentActions(doc)
		return
	}
	doc.view = defaultDocumentView()
	doc.view.recordText = "Gravação descartada."
	doc.view.status = "O texto anterior foi mantido. Pronto para gravar novamente."
	if origin != nil && origin.document == doc && origin.generation == doc.generation {
		doc.lastAudio, doc.lastResult, doc.recordedAppend = origin.source, origin.result, origin.append
		doc.live = origin.previousLive
		doc.transcriptionRun = origin.transcriptionRun
		doc.sourceLabel.SetText(origin.sourceLabel)
	} else {
		doc.lastAudio, doc.lastResult, doc.recordedAppend = "", nil, nil
		doc.live = nil
		doc.transcriptionRun = ""
		doc.sourceLabel.SetText("Português + inglês · sem traduzir sua fala")
	}
	doc.retryButton.Hide()
	d.syncDocumentActions(doc)
}
