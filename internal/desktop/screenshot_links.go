package desktop

import (
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"strings"

	"fyne.io/fyne/v2/dialog"
)

const screenshotLinkHeading = "Link dos screenshots"

type screenshotAssociation struct {
	document *document
	source   string
	capture  *captureOrigin
	live     *liveRecording
	marker   uint64
}

func screenshotFooter(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	return screenshotLinkHeading + "\n" + strings.Join(paths, "\n")
}

func (d *desktop) attachScreenshot(owner *screenshotAssociation, path string) {
	if owner == nil || owner.document.closed || owner.capture != nil && owner.capture.ignoreScreenshots {
		return
	}
	doc := owner.document
	if owner.capture != nil && owner.capture.recordingSource == owner.source {
		if !slices.Contains(owner.capture.screenshots, path) {
			owner.capture.screenshots = append(owner.capture.screenshots, path)
		}
	}
	// Clearing the editor during capture keeps this recording and its photos.
	if doc.recordedAppend == nil && d.captureOrigin == owner.capture && d.captureDocument == doc && doc.lastAudio == owner.source {
		doc.recordedAppend = &recordedAppend{Source: owner.source, Prefix: doc.editor.Text, Combined: doc.editor.Text}
	}
	if doc.lastAudio != owner.source || doc.recordedAppend == nil || doc.recordedAppend.Source != owner.source {
		if d.attachAudioHistoryScreenshot(owner, path) {
			if err := d.saveWorkspace(); err != nil {
				d.showDocumentError(doc, "Não foi possível salvar a screenshot no histórico: "+err.Error(), "")
			}
		}
		d.syncDocumentActions(doc)
		return
	}
	if !slices.Contains(doc.screenshotFiles, path) {
		doc.screenshotFiles = append(doc.screenshotFiles, path)
	}
	if owner.live != nil {
		session := owner.live
		if doc.live != nil && sameAudio(doc.live.Source, owner.source) {
			session = doc.live
		}
		d.attachLiveScreenshot(session, owner.marker, path)
		d.syncDocumentActions(doc)
		return
	}
	if !slices.Contains(doc.recordedAppend.Screenshots, path) {
		doc.recordedAppend.Screenshots = append(slices.Clone(doc.recordedAppend.Screenshots), path)
	}
	if doc.jobAppend != nil && doc.jobAppend.Source == owner.source {
		doc.jobAppend.Screenshots = slices.Clone(doc.recordedAppend.Screenshots)
	}
	d.refreshRecordingScreenshotLinks(doc)
	d.syncDocumentActions(doc)
}

// Selection can finish after speech recognition. Only the matching recording
// and its unchanged generated text can receive the late screenshot footer.
func (d *desktop) refreshRecordingScreenshotLinks(doc *document) {
	defer d.enterCurrentPrompt(doc)()
	recorded := doc.recordedAppend
	if recorded == nil || !recorded.LinksReady || recorded.Source != doc.lastAudio ||
		doc.closed || doc.editor.Text != recorded.Combined {
		return
	}
	text := appendTranscriptText(recorded.Content, screenshotFooter(recorded.Screenshots))
	recorded.Combined = text
	doc.editor.SetText(text)
	d.updateTranscriptLinks(doc, true)
	d.persistScreenshotText(doc)
}

func (d *desktop) persistScreenshotText(doc *document) {
	if doc.lastResult != nil {
		result := *doc.lastResult
		result.Text, _ = d.currentTexts(doc)
		doc.lastResult = &result
		if err := d.persistDocumentResult(doc, result); err != nil {
			d.showDocumentError(doc, "Não foi possível salvar os links das screenshots: "+err.Error(), "")
		}
	}
}

func (d *desktop) removeScreenshotReference(owner *screenshotAssociation, path string) {
	if err := d.invalidateScreenshotEvaluations(path); err != nil {
		d.showError("A screenshot foi excluída, mas a avaliação ligada a ela não pôde ser removida: "+err.Error(), "")
	}
	if owner != nil && owner.capture != nil {
		owner.capture.screenshots = slices.DeleteFunc(owner.capture.screenshots, func(item string) bool { return item == path })
	}
	if owner != nil && owner.live != nil {
		d.removeLiveScreenshot(owner.live, owner.marker)
	}
	// Even when only the evaluated immutable context mentions this file,
	// persist the removed note before the tab can be reopened.
	historyChanged := path != ""
	for _, doc := range d.documents {
		if !doc.closed {
			historyChanged = d.removeAudioHistoryScreenshot(doc, path) || historyChanged
			undo := cloneAudioSnapshot(audioSnapshot{Undo: doc.undo})
			if removeAudioScreenshot(&undo, path) {
				doc.undo = undo.Undo
				historyChanged = true
			}
			historyChanged = removeDraftScreenshot(doc.promptSteps, path) || historyChanged
			markDraftScreenshot(doc.promptSteps, path, "")
			d.syncDocumentActions(doc)
		}
		if !doc.closed && slices.Contains(doc.screenshotFiles, path) {
			d.removeDocumentScreenshotReference(doc, path)
		}
	}
	d.removeRecoveryScreenshot(path)
	if historyChanged {
		if err := d.saveWorkspace(); err != nil {
			d.showError("Não foi possível atualizar os links no histórico de áudio: "+err.Error(), "")
		}
	}
}

func (d *desktop) removeDocumentScreenshotReference(doc *document, path string) {
	defer d.enterCurrentPrompt(doc)()
	previousText := doc.editor.Text
	doc.screenshotFiles = slices.DeleteFunc(doc.screenshotFiles, func(item string) bool { return item == path })
	liveChanged := false
	if doc.live != nil {
		session := doc.live
		state := audioSnapshot{Live: session.archive()}
		liveChanged = removeAudioScreenshot(&state, path)
		session.liveArchive = *state.Live
		session.rebuildScreenshotCuts()
		d.renderLiveText(session)
	}
	strip := func(recorded *recordedAppend) {
		if recorded == nil {
			return
		}
		recorded.Screenshots = slices.DeleteFunc(recorded.Screenshots, func(item string) bool { return item == path })
		recorded.Prefix = removeScreenshotLine(recorded.Prefix, path)
		recorded.Combined = removeScreenshotLine(recorded.Combined, path)
		recorded.Content = removeScreenshotLine(recorded.Content, path)
	}
	strip(doc.recordedAppend)
	strip(doc.jobAppend)
	text := removeScreenshotLine(doc.editor.Text, path)
	if text != doc.editor.Text {
		doc.editor.SetText(text)
	}
	if doc.editor.Text != previousText {
		d.updateTranscriptLinks(doc, true)
		d.persistScreenshotText(doc)
	}
	if liveChanged && doc.live.completed {
		d.persistLiveResult(doc.live)
	}
	d.syncDocumentActions(doc)
}

func removeScreenshotLine(text, path string) string {
	lines := strings.Split(text, "\n")
	removed := false
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) == path {
			lines = slices.Delete(lines, i, i+1)
			removed = true
		}
	}
	if !removed {
		return text
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if lines[i] == screenshotLinkHeading && (i+1 == len(lines) || strings.TrimSpace(lines[i+1]) == "") {
			lines = slices.Delete(lines, i, i+1)
		}
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

func (d *desktop) screenshotFileLinks(doc *document) map[string]string {
	files := make(map[string]string, len(doc.screenshotFiles))
	root, err := filepath.Abs(filepath.Join(d.directory, "screenshots"))
	if err != nil {
		return files
	}
	for _, path := range doc.screenshotFiles {
		absolute, err := filepath.Abs(path)
		if err != nil || !captureDirectory(filepath.Dir(absolute), root) || !strings.EqualFold(filepath.Ext(absolute), ".png") {
			continue
		}
		target := (&url.URL{Scheme: "file", Path: "/" + filepath.ToSlash(absolute)}).String()
		files[target] = path
	}
	return files
}

func (d *desktop) openImageLink(doc *document, path string) {
	if doc.closed || d.deps.openImage == nil {
		return
	}
	d.operation(func() error { return d.deps.openImage(path) }, func(err error) {
		if err != nil && !doc.closed {
			dialog.ShowError(fmt.Errorf("não foi possível abrir a screenshot: %w", err), d.window)
		}
	})
}
