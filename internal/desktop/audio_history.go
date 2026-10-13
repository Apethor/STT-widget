package desktop

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"ai-talker/internal/live"
	"ai-talker/internal/transcript"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
)

// A history item owns the transcript state for one audio source. Restoring it
// also restores the append baseline and pending timeline, so retry neither
// duplicates later recordings nor forgets completed Team/Stream parts.
type audioSnapshot struct {
	Source           string             `json:"source"`
	SavedAt          time.Time          `json:"saved_at"`
	Text             string             `json:"text"`
	Result           *transcript.Result `json:"result,omitempty"`
	Append           *recordedAppend    `json:"recording_append,omitempty"`
	Live             *liveArchive       `json:"live,omitempty"`
	Undo             *clearUndo         `json:"undo,omitempty"`
	ScreenshotFiles  []string           `json:"screenshot_files,omitempty"`
	TranscriptionRun string             `json:"transcription_run,omitempty"`
	Timer            string             `json:"timer"`
	SourceLabel      string             `json:"source_label"`
	Error            string             `json:"error,omitempty"`
	Retry            bool               `json:"retry,omitempty"`
	AudioMissing     bool               `json:"audio_missing,omitempty"`
	HistoryRemoved   bool               `json:"history_removed,omitempty"`
}

func sameAudio(first, second string) bool {
	return first != "" && second != "" && strings.EqualFold(filepath.Clean(first), filepath.Clean(second))
}

func cloneAudioSnapshot(snapshot audioSnapshot) audioSnapshot {
	snapshot.ScreenshotFiles = slices.Clone(snapshot.ScreenshotFiles)
	if snapshot.Result != nil {
		result := *snapshot.Result
		result.Parts = slices.Clone(result.Parts)
		for index := range result.Parts {
			result.Parts[index].Usage = maps.Clone(result.Parts[index].Usage)
		}
		result.Exports = maps.Clone(result.Exports)
		snapshot.Result = &result
	}
	if snapshot.Append != nil {
		appendState := *snapshot.Append
		appendState.Screenshots = slices.Clone(appendState.Screenshots)
		snapshot.Append = &appendState
	}
	if snapshot.Live != nil {
		snapshot.Live = (&liveRecording{liveArchive: *snapshot.Live}).archive()
	}
	if snapshot.Undo != nil {
		undo := *snapshot.Undo
		saved := cloneAudioSnapshot(audioSnapshot{Result: undo.Result, Append: undo.Append, Live: undo.Live})
		undo.Result, undo.Append, undo.Live = saved.Result, saved.Append, saved.Live
		snapshot.Undo = &undo
	}
	return snapshot
}

func (d *desktop) removeAudioHistoryScreenshot(doc *document, path string) bool {
	changed := false
	for index, saved := range doc.audioHistory {
		snapshot := cloneAudioSnapshot(saved)
		if removeAudioScreenshot(&snapshot, path) {
			doc.audioHistory[index] = snapshot
			changed = true
		}
	}
	return changed
}

func (d *desktop) attachAudioHistoryScreenshot(owner *screenshotAssociation, path string) bool {
	doc := owner.document
	index := slices.IndexFunc(doc.audioHistory, func(saved audioSnapshot) bool {
		return sameAudio(saved.Source, owner.source)
	})
	if index < 0 {
		return false
	}
	snapshot := cloneAudioSnapshot(doc.audioHistory[index])
	if !slices.Contains(snapshot.ScreenshotFiles, path) {
		snapshot.ScreenshotFiles = append(snapshot.ScreenshotFiles, path)
	}
	if snapshot.Live != nil && owner.live != nil {
		session := &liveRecording{liveArchive: *snapshot.Live}
		entry := slices.IndexFunc(session.Entries, func(entry live.Entry) bool {
			return entry.Screenshot && entry.ID == owner.marker
		})
		if entry < 0 {
			return false
		}
		previous := session.PreviousBlocks
		if len(previous) == 0 {
			previous = session.textBlocks()
		}
		session.Entries[entry].Path, session.Entries[entry].Pending = path, false
		blocks, retainingPrevious := session.displayBlocks(snapshot.TranscriptionRun != "")
		if snapshot.Text == live.Render(session.Prefix, previous) {
			snapshot.Text = live.Render(session.Prefix, blocks)
		} else {
			snapshot.Text = live.Reconcile(snapshot.Text, previous, blocks)
		}
		snapshot.Live.Combined = snapshot.Text
		if retainingPrevious {
			snapshot.Live.PreviousBlocks = slices.Clone(blocks)
		}
		if snapshot.Append != nil {
			snapshot.Append.Content, snapshot.Append.Combined = snapshot.Text, snapshot.Text
		}
	} else if snapshot.Append != nil {
		recorded := snapshot.Append
		if !slices.Contains(recorded.Screenshots, path) {
			recorded.Screenshots = append(recorded.Screenshots, path)
		}
		if recorded.LinksReady && snapshot.Text == recorded.Combined {
			snapshot.Text = appendTranscriptText(recorded.Content, screenshotFooter(recorded.Screenshots))
			recorded.Combined = snapshot.Text
		}
	}
	if snapshot.Result != nil {
		snapshot.Result.Text = snapshot.Text
	}
	doc.audioHistory[index] = snapshot
	return true
}

func (d *desktop) snapshotAudio(doc *document) audioSnapshot {
	prompt, _ := d.currentTexts(doc)
	snapshot := audioSnapshot{
		Source: doc.lastAudio, SavedAt: d.deps.now(), Text: prompt,
		Result: doc.lastResult, Append: doc.recordedAppend, Undo: doc.undo,
		ScreenshotFiles: doc.screenshotFiles, TranscriptionRun: doc.transcriptionRun,
		Timer: doc.view.timer, SourceLabel: doc.sourceLabel.Text,
		Retry: doc.busy || doc.retryButton.Visible(), AudioMissing: doc.audioMissing,
	}
	if doc == d.document && d.playback == nil {
		snapshot.Timer = d.timer.Text
	}
	// During recognition of a new recording the editor can still describe
	// the previous successful audio. It must not become the new file's result.
	if snapshot.Result != nil && !sameAudio(snapshot.Result.Source, snapshot.Source) {
		snapshot.Result = nil
	}
	if doc.live != nil && sameAudio(doc.live.Source, snapshot.Source) {
		snapshot.Live = doc.live.archive()
		snapshot.Retry = snapshot.Retry || snapshot.Live.needsRetry()
	}
	if doc.errorLabel.Visible() {
		snapshot.Error = doc.errorLabel.Text
	}
	return cloneAudioSnapshot(snapshot)
}

func (d *desktop) audioSnapshots(doc *document) []audioSnapshot {
	history := slices.Clone(doc.audioHistory)
	if doc.lastAudio == "" {
		return history
	}
	current := d.snapshotAudio(doc)
	for index, previous := range history {
		if sameAudio(previous.Source, current.Source) {
			if previous.HistoryRemoved {
				// The active audio/text still belongs to the tab. Its removed
				// history entry stays a small tombstone instead of being rebuilt
				// by every workspace save.
				return history
			}
			current.SavedAt = previous.SavedAt
			history[index] = current
			return history
		}
	}
	return append(history, current)
}

// Persist before replacing a source. A failed write keeps both the old audio
// and its editors available instead of silently losing their recovery path.
func (d *desktop) preserveAudioHistory(doc *document) bool {
	if doc == nil || doc.closed || doc.lastAudio == "" {
		return true
	}
	previous := doc.audioHistory
	doc.audioHistory = d.audioSnapshots(doc)
	if err := d.saveWorkspace(); err != nil {
		doc.audioHistory = previous
		d.showDocumentError(doc, "Não foi possível preservar o histórico de áudio: "+err.Error(), "")
		return false
	}
	d.syncAudioHistoryButton()
	return true
}

func (d *desktop) validAudioHistory(history []audioSnapshot) bool {
	seen := make(map[string]bool, len(history))
	for _, snapshot := range history {
		key := strings.ToLower(filepath.Clean(snapshot.Source))
		if snapshot.Source == "" || seen[key] || !validTranscriptionRun(snapshot.TranscriptionRun) ||
			!d.validLiveArchive(snapshot.Live) || snapshot.Live != nil && !sameAudio(snapshot.Live.Source, snapshot.Source) ||
			snapshot.Undo != nil && (!validTranscriptionRun(snapshot.Undo.TranscriptionRun) || !d.validLiveArchive(snapshot.Undo.Live)) {
			return false
		}
		seen[key] = true
	}
	return true
}

func (d *desktop) syncAudioHistoryButton() {
	if d.audioHistoryButton == nil || d.document == nil {
		return
	}
	count := len(slices.DeleteFunc(slices.Clone(d.audioHistory), func(snapshot audioSnapshot) bool {
		return snapshot.HistoryRemoved
	}))
	if d.lastAudio != "" && !slices.ContainsFunc(d.audioHistory, func(snapshot audioSnapshot) bool {
		return sameAudio(snapshot.Source, d.lastAudio)
	}) {
		count++
	}
	d.audioHistoryButton.setHint(fmt.Sprintf("Histórico de áudios desta aba · %d · carregar um áudio anterior", count))
	setEnabled(d.audioHistoryButton, count > 0 && !d.audioLoading && d.capture == captureIdle && !d.closing)
}

func (d *desktop) openAudioHistory() {
	doc := d.document
	if d.capture != captureIdle || doc.audioLoading || d.closing || !d.preserveAudioHistory(doc) {
		return
	}
	d.hints.hide()
	d.showAudioHistoryPage(doc, 0)
}

func (d *desktop) showAudioHistoryPage(doc *document, offset int) {
	if doc.closed || doc.audioLoading || d.closing || d.capture != captureIdle {
		return
	}
	menu := widget.NewPopUpMenu(d.audioHistoryPage(doc, offset), d.window.Canvas())
	anchor := d.audioHistoryButton
	position := d.application.Driver().AbsolutePositionForObject(anchor)
	menu.ShowAtPosition(fyne.NewPos(
		max(0, position.X+anchor.Size().Width-menu.Size().Width),
		position.Y+anchor.Size().Height,
	))
}

func (d *desktop) audioHistoryMenu(doc *document) *fyne.Menu {
	return d.audioHistoryPage(doc, 0)
}

func (d *desktop) audioHistoryPage(doc *document, offset int) *fyne.Menu {
	const pageSize = 10
	items := make([]*fyne.MenuItem, 0, pageSize+2)
	history := slices.DeleteFunc(slices.Clone(doc.audioHistory), func(snapshot audioSnapshot) bool {
		return snapshot.HistoryRemoved
	})
	end := min(offset+pageSize, len(history))
	if offset > 0 {
		items = append(items, fyne.NewMenuItem(uiText("Mais recentes…"),
			d.guard(func() { d.showAudioHistoryPage(doc, max(0, offset-pageSize)) })))
	}
	for position := offset; position < end; position++ {
		index := len(history) - 1 - position
		snapshot := history[index]
		name := []rune(filepath.Base(snapshot.Source))
		if len(name) > 25 {
			name = append(name[:24], '…')
		}
		title := fmt.Sprintf("%d · %s", index+1, string(name))
		if snapshot.Timer != "" && snapshot.Timer != "00:00" {
			title += " · " + snapshot.Timer
		}
		if snapshot.AudioMissing {
			title += " · áudio removido"
		}
		item := fyne.NewMenuItem(title, d.guard(func() { d.restoreAudioSnapshot(doc, snapshot.Source) }))
		item.Checked = sameAudio(doc.lastAudio, snapshot.Source)
		items = append(items, item)
	}
	if end < len(history) {
		items = append(items, fyne.NewMenuItem(uiText("Mais antigos…"),
			d.guard(func() { d.showAudioHistoryPage(doc, end) })))
	}
	return fyne.NewMenu(uiText("Áudios desta aba"), items...)
}

func (d *desktop) restoreAudioSnapshot(doc *document, source string) {
	d.returnToCurrentPrompt(doc)
	if d.closing || doc.closed || doc.audioLoading || d.capture != captureIdle || sameAudio(doc.lastAudio, source) {
		return
	}
	index := slices.IndexFunc(doc.audioHistory, func(snapshot audioSnapshot) bool { return sameAudio(snapshot.Source, source) })
	if index < 0 || doc.audioHistory[index].HistoryRemoved || !d.preserveAudioHistory(doc) {
		return
	}
	target := cloneAudioSnapshot(doc.audioHistory[index])
	if target.AudioMissing {
		d.applyAudioSnapshot(doc, target)
		doc.view.recordText = "Áudio removido pela limpeza."
		doc.view.status = "A transcrição histórica permanece disponível, mas o áudio não está mais na pasta interna."
		d.renderDocument(doc)
		d.syncControls()
		return
	}
	d.stopDocumentPlayback(doc)
	wasBusy := doc.busy
	d.invalidateDocumentJob(doc)
	if doc.live != nil {
		d.cancelLiveRecording(doc.live)
	}
	if wasBusy {
		d.showDocumentCancellation(doc)
	}
	generation := doc.generation
	editEpoch := doc.editEpoch
	doc.audioLoading = true
	d.syncAudioHistoryButton()
	// Imported files can reside on network drives. Validate them off the UI
	// thread, then reject the callback if a newer audio action took ownership.
	d.operation(func() error {
		paths := []string{target.Source}
		if target.Live != nil {
			for _, track := range target.Live.Tracks {
				paths = append(paths, track.Snapshot.Path)
			}
		}
		for _, path := range paths {
			if err := d.ctx.Err(); err != nil {
				return err
			}
			info, err := os.Stat(path)
			if err != nil {
				return fmt.Errorf("carregar %s: %w", filepath.Base(path), err)
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("%s não é um arquivo de áudio", filepath.Base(path))
			}
		}
		return nil
	}, func(err error) {
		doc.audioLoading = false
		if doc.closed || doc.generation != generation || d.capture != captureIdle {
			d.syncAudioHistoryButton()
			return
		}
		if err != nil {
			d.showDocumentError(doc, "Não foi possível abrir o áudio anterior: "+err.Error(), "")
			d.syncControls()
			return
		}
		if doc.editEpoch != editEpoch {
			if d.preserveAudioHistory(doc) {
				doc.view.status = "Texto editado. A troca de áudio foi cancelada."
				d.renderDocument(doc)
			}
			d.syncControls()
			return
		}
		// Screenshot selections/deletions can update a historical entry while
		// its audio files are checked. Apply its latest state, not the old copy.
		currentTarget := slices.IndexFunc(doc.audioHistory, func(saved audioSnapshot) bool {
			return sameAudio(saved.Source, source)
		})
		if currentTarget < 0 || doc.audioHistory[currentTarget].HistoryRemoved || !d.preserveAudioHistory(doc) {
			d.syncControls()
			return
		}
		d.returnToCurrentPrompt(doc)
		previous := d.snapshotAudio(doc)
		previousSteps := slices.Clone(doc.promptSteps)
		previousID, previousConversation := doc.nextPrompt, doc.currentConversation
		d.applyAudioSnapshot(doc, doc.audioHistory[currentTarget])
		if err := d.saveWorkspace(); err != nil {
			d.applyAudioSnapshot(doc, previous)
			doc.promptSteps = previousSteps
			doc.nextPrompt, doc.currentConversation = previousID, previousConversation
			doc.promptCursor = len(previousSteps) - 1
			d.showDocumentError(doc, "Não foi possível salvar a troca de áudio: "+err.Error(), "")
			d.syncControls()
			return
		}
		if doc.lastResult != nil {
			result := *doc.lastResult
			result.Text = doc.editor.Text
			err = d.persistDocumentResult(doc, result)
		} else {
			err = d.removeDocumentLatest(doc)
		}
		if err != nil {
			d.showDocumentError(doc, "Áudio restaurado, mas não foi possível atualizar o resultado salvo: "+err.Error(), "")
		}
		d.renderDocument(doc)
		d.syncControls()
	})
}

func (d *desktop) applyAudioSnapshot(doc *document, snapshot audioSnapshot) {
	d.returnToCurrentPrompt(doc)
	snapshot = cloneAudioSnapshot(snapshot)
	doc.lastAudio, doc.lastResult = snapshot.Source, snapshot.Result
	doc.audioMissing = snapshot.AudioMissing
	doc.transcriptionRun, doc.recordedAppend, doc.undo = snapshot.TranscriptionRun, snapshot.Append, snapshot.Undo
	doc.screenshotFiles = snapshot.ScreenshotFiles
	doc.live = nil
	d.restoreLiveArchive(doc, snapshot.Live)
	doc.editor.SetText(snapshot.Text)
	doc.sourceLabel.SetText(snapshot.SourceLabel)
	d.syncTranscriptionDetails(doc)
	doc.view = defaultDocumentView()
	doc.view.timer = snapshot.Timer
	if doc.view.timer == "" {
		doc.view.timer = "00:00"
	}
	doc.view.recordText = "Áudio anterior carregado."
	doc.view.status = "Você pode ouvir ou tentar novamente. Os outros áudios continuam no histórico."
	if snapshot.AudioMissing {
		doc.view.recordText = "Áudio removido pela limpeza."
		doc.view.status = "A transcrição foi mantida. O arquivo de áudio não está mais na pasta interna."
	}
	doc.errorLabel.Hide()
	if snapshot.Error != "" {
		doc.errorLabel.SetText(snapshot.Error)
		doc.errorLabel.Show()
	}
	doc.retryButton.Hide()
	if !snapshot.AudioMissing && (snapshot.Retry || snapshot.Result == nil || doc.live != nil && doc.live.needsRetry()) {
		doc.retryButton.Show()
	}
	d.updateTranscriptLinks(doc, true)
	d.syncDocumentActions(doc)
}
