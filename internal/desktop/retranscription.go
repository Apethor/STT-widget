package desktop

import (
	"context"
	"crypto/rand"
	"path/filepath"
	"slices"
	"strings"

	"ai-talker/internal/live"
)

// A new cache namespace forces recognition even with the same model. Keeping
// that namespace on the document lets retry resume only this new attempt.
func (d *desktop) transcriptionCacheDirectory(doc *document) string {
	if doc.transcriptionRun == "" {
		return ""
	}
	return filepath.Join(d.directory, "jobs-retranscribe-go-v1", doc.transcriptionRun)
}

func validTranscriptionRun(run string) bool {
	return run == "" || len(run) >= 26 && len(run) <= 128 &&
		strings.Trim(run, "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567") == ""
}

func (d *desktop) retranscribeDocument(doc *document) {
	if doc.browsingPrompts() {
		return
	}
	if d.closing || doc.closed || doc.busy || d.capture != captureIdle || doc.lastAudio == "" {
		return
	}
	if !d.ensureTranscriptionSetup(doc, d.settings, func() { d.retranscribeDocument(doc) }) {
		return
	}
	if err := d.saveDocumentDrafts(doc); err != nil {
		d.showDocumentError(doc, err.Error(), "")
		return
	}
	doc.transcriptionRun = rand.Text()
	if doc.live != nil && doc.live.Source == doc.lastAudio {
		d.retranscribeLiveRecording(doc)
		return
	}
	if doc.recordedAppend == nil || doc.recordedAppend.Source != doc.lastAudio {
		doc.recordedAppend = &recordedAppend{Source: doc.lastAudio, Combined: doc.editor.Text}
	}
	d.startDocumentTranscription(doc, doc.lastAudio, true)
}

func (d *desktop) retranscribeLiveRecording(doc *document) {
	session := doc.live
	d.invalidateDocumentJob(doc)
	session.ctx, session.cancel = context.WithCancel(d.ctx)
	session.settings = cloneSettings(d.settings)
	session.canceled, session.completed, session.stopped, session.show = false, false, true, false
	session.epoch++
	session.started = d.deps.now()
	session.running = make(map[uint64]bool)
	session.progress = make(map[uint64]float64)
	session.cursors = make(map[string]uint64)
	if len(session.PreviousBlocks) == 0 {
		session.PreviousEntries = slices.Clone(session.Entries)
	} else if len(session.PreviousEntries) > 0 {
		session.PreviousEntries = session.previousDisplayEntries()
	}
	session.PreviousBlocks = slices.Clone(session.blocks)
	// Preserve screenshot positions and the previously published text while
	// rebuilding all speech parts from both original recording tracks.
	session.Entries = slices.DeleteFunc(session.Entries, func(entry live.Entry) bool { return !entry.Screenshot })
	doc.busy = true
	doc.jobStarted, doc.lastProgressAt = session.started, session.started
	doc.jobProvider = session.settings.TranscriptionProvider()
	doc.jobCancel = func() { d.cancelLiveRecording(session) }
	doc.errorLabel.Hide()
	doc.retryButton.Hide()
	doc.view.recordText = "Retranscrevendo o áudio carregado."
	doc.view.status = "Reconhecendo novamente com o modelo selecionado."
	doc.view.progress = 0
	d.recoverLiveTracks(session, func(err error) {
		if err != nil {
			doc.busy, doc.jobCancel = false, nil
			d.cancelLiveRecording(session)
			d.showDocumentError(doc, "Não foi possível ler as trilhas preservadas: "+err.Error(), "")
			doc.retryButton.Show()
			d.syncControls()
			return
		}
		d.pumpLiveParts(session)
		d.completeLiveIfReady(session)
	})
	d.renderDocument(doc)
	d.syncControls()
}
