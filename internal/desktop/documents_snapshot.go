package desktop

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"ai-talker/internal/store"
)

// JSON is the same durable contract used to reopen a session. Round-tripping
// here detaches every nested result, history and timeline before UI mutation.
func detachedWorkspace(snapshot workspaceSnapshot) (workspaceSnapshot, error) {
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return workspaceSnapshot{}, fmt.Errorf("preservar o estado das abas: %w", err)
	}
	var copy workspaceSnapshot
	if err := json.Unmarshal(payload, &copy); err != nil {
		return workspaceSnapshot{}, fmt.Errorf("recuperar o estado das abas: %w", err)
	}
	return copy, nil
}

func (d *desktop) restoreDocumentSnapshot(doc *document, tab documentSnapshot) {
	doc.historyApplying = true
	doc.group = tab.Group
	doc.name = ""
	if tab.Renamed || !automaticTabName(tab.Name) {
		doc.name = tab.Name
	}
	doc.lastAudio, doc.lastResult = tab.Source, tab.Result
	doc.evaluation = tab.Evaluation
	doc.audioMissing = tab.AudioMissing
	doc.undo, doc.recordedAppend = tab.Undo, tab.Append
	doc.transcriptionRun, doc.audioHistory = tab.TranscriptionRun, tab.AudioHistory
	doc.audioLoading = false
	doc.processOptions, doc.processResult = tab.Options, tab.Processed
	doc.processCollapsed, doc.processOffset = tab.ProcessCollapsed, tab.ProcessOffset
	doc.transcriptCollapsed, doc.panelSizes = tab.TranscriptCollapsed, tab.PanelSizes.normalized(tab.ProcessOffset)
	doc.transcriptZoom, doc.processZoom = normalizedEditorZoom(tab.TranscriptZoom), normalizedEditorZoom(tab.ProcessZoom)
	doc.processHistory, doc.screenshotFiles = tab.Conversation, tab.ScreenshotFiles
	doc.editor.SetText(tab.Text)
	doc.processEditor.SetText(tab.ProcessedText)
	doc.historyApplying = false
	d.restorePromptHistory(doc, tab.PromptSteps)
	doc.live = nil
	d.restoreLiveArchive(doc, tab.Live)
	d.restoreVoice(doc, tab.Voice)
	doc.closed = false
	doc.view = defaultDocumentView()
	if tab.Result != nil {
		doc.view.timer = clockText(tab.Result.DurationSeconds)
	}
	doc.errorLabel.Hide()
	doc.processError.Hide()
	doc.retryButton.Hide()
	if tab.Source != "" && !tab.AudioMissing && (tab.Retry || tab.Result == nil || doc.live != nil && doc.live.needsRetry()) {
		doc.retryButton.Show()
	}
	doc.sourceLabel.SetText("Português + inglês · sem traduzir sua fala")
	d.arrangeDocumentEditors(doc)
	d.syncProcessingOptions(doc)
	d.syncTranscriptionDetails(doc)
	d.updateTranscriptLinks(doc, true)
	d.updateResultLinks(doc, true)
	d.syncDocumentActions(doc)
}

func (d *desktop) archiveRecoveryWorkspace(snapshot workspaceSnapshot) error {
	directory := filepath.Join(d.directory, "rascunhos")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(directory, "antes-de-desfazer-"+d.deps.now().Format("20060102-150405")+"-*.json")
	if err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return store.WriteJSON(file.Name(), snapshot)
}
