package desktop

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"ai-talker/internal/processing"
	"ai-talker/internal/store"
	"ai-talker/internal/transcript"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const maxDocuments = 32

// Each tab owns its editors, tasks, last audio, undo and processing options.
// These fields and widgets are read and written only on the UI thread.
type document struct {
	id               uint64
	group            uint64
	name             string
	item             *container.TabItem
	closed           bool
	generation       uint64
	jobCancel        context.CancelFunc
	busy             bool
	jobStarted       time.Time
	lastProgressAt   time.Time
	lastAudio        string
	lastResult       *transcript.Result
	evaluation       *transcriptEvaluation
	audioMissing     bool
	audioHistory     []audioSnapshot
	audioLoading     bool
	editEpoch        uint64
	transcriptionRun string
	draftText        string
	copyEpoch        uint64
	undo             *clearUndo
	recordedAppend   *recordedAppend
	jobAppend        *recordedAppend
	jobProvider      string
	jobProgress      transcript.Progress
	live             *liveRecording
	voice            *voiceDocument
	view             documentView

	editor               *widget.Entry
	clearButton          *actionButton
	undoButton           *actionButton
	copyButton           *actionButton
	saveButton           *actionButton
	retryButton          *widget.Button
	retranscribeButton   *actionButton
	evaluationButton     *actionButton
	transcriptName       *themedText
	transcriptPreview    *widget.RichText
	transcriptReadScroll *container.Scroll
	transcriptBody       *fyne.Container
	transcriptViewButton *actionButton
	transcriptReading    bool
	screenshotFiles      []string
	qualityBars          *qualityIndicator
	providerButton       *actionButton
	editors              *container.Split
	sourceLabel          *widget.Label
	errorLabel           *widget.Label
	transcriptCollapsed  bool
	transcriptPanel      *collapsiblePanel
	transcriptCollapse   *actionButton
	transcriptZoomButton *actionButton
	panelSizes           panelSizes
	presentationEpoch    uint64
	transcriptZoom       int
	processZoom          int
	transcriptZoomForm   *editorZoomForm
	processZoomForm      *editorZoomForm

	processOptions      processing.Options
	processCollapsed    bool
	processPanel        *collapsiblePanel
	processCollapse     *actionButton
	processZoomButton   *actionButton
	processOffset       float64
	promptSteps         []promptStep
	promptCursor        int
	nextPrompt          uint64
	currentConversation uint64
	currentPrompt       string
	currentResponse     string
	historyApplying     bool
	promptBack          *actionButton
	promptForward       *actionButton
	promptLabel         *themedText
	promptEdit          *actionButton
	promptEditingID     uint64
	processGeneration   uint64
	processCancel       context.CancelFunc
	processBusy         bool
	processSource       string
	processResult       *processing.Result
	processHistory      []processing.Turn
	processDraftText    string
	processEditor       *widget.Entry
	processPreview      *widget.RichText
	processReadScroll   *container.Scroll
	processBody         *fyne.Container
	processViewButton   *actionButton
	processLinkCount    int
	processReading      bool
	processButton       *actionButton
	processFollowButton *actionButton
	processCopyButton   *actionButton
	processSaveButton   *actionButton
	processMode         *widget.Select
	processModel        *widget.Select
	processReasoning    *widget.Select
	processPrompt       *actionButton
	processSearch       *actionButton
	processError        *widget.Label
	processStatus       *widget.Label
	updatingOptions     bool
}

type clearUndo struct {
	Text             string             `json:"text"`
	Result           *transcript.Result `json:"result,omitempty"`
	Source           string             `json:"source"`
	AudioMissing     bool               `json:"audio_missing,omitempty"`
	Append           *recordedAppend    `json:"recording_append,omitempty"`
	Live             *liveArchive       `json:"live,omitempty"`
	TranscriptionRun string             `json:"transcription_run,omitempty"`
}

// The last recording can be retried without appending the same segment twice.
// A changed editor is kept intact; only an unchanged generated suffix is replaced.
type recordedAppend struct {
	Source      string   `json:"source"`
	Prefix      string   `json:"prefix"`
	Combined    string   `json:"combined"`
	Content     string   `json:"content,omitempty"`
	Screenshots []string `json:"screenshots,omitempty"`
	LinksReady  bool     `json:"links_ready,omitempty"`
	VideoPath   string   `json:"video_path,omitempty"`
}

type documentView struct {
	timer      string
	recordText string
	importance widget.Importance
	status     string
	progress   float64
}

func defaultDocumentView() documentView {
	return documentView{
		timer: "00:00", recordText: "Pronto para começar",
		importance: widget.SuccessImportance,
		status:     "O áudio só é enviado ao concluir.",
	}
}

func (d *desktop) createDocument() *document {
	group := d.groups[0]
	if d.document != nil {
		group = d.document.group
	}
	return d.createDocumentInGroup(group)
}

func (d *desktop) createDocumentInGroup(group uint64) *document {
	sizes := panelSizes{EditorOffset: 0.5}
	if d.document != nil && !d.selectingTabs {
		d.rememberPanelSizes(d.document)
		sizes = d.document.panelSizes
	}
	d.cancelMergeForGroup(group, "As abas do grupo mudaram. Clique em Mesclar para usar os textos atuais.")
	d.nextDocument++
	doc := &document{
		id: d.nextDocument, group: group, generation: 1, view: defaultDocumentView(),
		processOptions: d.settings.Processing.WithLegacyMerge(d.settings.Merge),
		panelSizes:     sizes, transcriptZoom: 100, processZoom: 100,
	}
	if len(d.documents) > 0 && !d.selectingTabs {
		doc.voice = &voiceDocument{Mode: voiceModeRecording}
	}
	if d.document != nil && !d.selectingTabs {
		doc.transcriptZoom = normalizedEditorZoom(d.document.transcriptZoom)
		doc.processZoom = normalizedEditorZoom(d.document.processZoom)
	}
	if previous := d.groupDocuments(group); len(previous) > 0 {
		doc.processOptions = previous[len(previous)-1].processOptions
	}
	doc.processOptions = doc.processOptions.WithLegacyMerge(d.settings.Merge)
	doc.transcriptName = label("Transcrição", 16, textColor)
	doc.transcriptName.TextStyle.Bold = true
	doc.transcriptName.hint = &toolHint{layer: d.hints}
	doc.qualityBars = d.newQualityIndicator(d.settings.Quality)
	doc.providerButton = d.iconButton("Transcrever com AWS Bedrock", cloudIcon, d.chooseTranscriptionProvider)
	doc.providerButton.Importance = widget.LowImportance
	d.syncDocumentProvider(doc)
	doc.sourceLabel = widget.NewLabel("Português + inglês · sem traduzir sua fala")
	doc.sourceLabel.Truncation = fyne.TextTruncateEllipsis
	doc.editor = widget.NewMultiLineEntry()
	doc.editor.Wrapping = fyne.TextWrapWord
	doc.editor.SetMinRowsVisible(3)
	doc.editor.SetPlaceHolder("A transcrição aparece aqui. Você também pode digitar ou colar um texto.")
	doc.editor.OnChanged = func(text string) {
		d.guard(func() {
			if doc.historyApplying {
				return
			}
			if doc.browsingPrompts() {
				d.changeHistoricalPrompt(doc, text)
				return
			}
			doc.currentPrompt = text
			doc.editEpoch++
			d.cancelMergeForGroup(doc.group, "Texto alterado. Clique em Mesclar para usar a edição.")
			if doc.processBusy && text != doc.processSource {
				d.invalidateProcessing(doc)
				doc.processStatus.SetText("Texto alterado. Clique em Processar para usar a edição.")
				d.syncControls()
			}
			d.syncDocumentActions(doc)
			d.syncCombineControls()
			d.updateTranscriptLinks(doc, doc.transcriptReading)
		})()
	}
	doc.errorLabel = widget.NewLabel("")
	doc.errorLabel.Wrapping = fyne.TextWrapWord
	doc.errorLabel.Importance = widget.DangerImportance
	doc.errorLabel.Hide()
	doc.retryButton = widget.NewButton("Tentar novamente", d.guard(func() {
		if !doc.busy && doc.lastAudio != "" {
			d.retryDocument(doc)
		}
	}))
	doc.retryButton.Hide()
	doc.retranscribeButton = d.iconButton(
		"Retranscrever áudio · usar o modelo selecionado", theme.ViewRefreshIcon(),
		func() { d.retranscribeDocument(doc) },
	)
	doc.evaluationButton = d.iconButton("Avaliar transcrição", nil, func() { d.openTranscriptEvaluation(doc) })
	doc.evaluationButton.Text = "☆"
	doc.evaluationButton.Importance = widget.LowImportance
	doc.clearButton = d.iconButton("Limpar texto", theme.DeleteIcon(), func() { d.clearText(true) })
	doc.undoButton = d.iconButton("Desfazer limpeza", theme.ContentUndoIcon(), d.undoClear)
	doc.copyButton = d.iconButton("Copiar texto", theme.ContentCopyIcon(), d.copyText)
	doc.copyButton.Importance = widget.HighImportance
	doc.saveButton = d.iconButton("Salvar transcrição", theme.DocumentSaveIcon(), d.saveText)
	doc.transcriptViewButton = d.iconButton("Ler transcrição com links clicáveis", theme.VisibilityIcon(), func() {
		d.updateTranscriptLinks(doc, !doc.transcriptReading)
		if !doc.transcriptReading {
			d.window.Canvas().Focus(doc.editor)
		}
	})
	doc.transcriptViewButton.Disable()
	provider := container.NewThemeOverride(doc.providerButton, providerTheme{Theme: d.palette})
	doc.transcriptCollapse = d.iconButton("Recolher transcrição", theme.NavigateBackIcon(), func() {
		d.toggleTranscriptionPanel(doc)
	})
	doc.transcriptZoomButton = d.editorZoomButton(doc, true)
	for _, button := range []*actionButton{
		doc.providerButton, doc.transcriptZoomButton, doc.transcriptCollapse,
		doc.clearButton, doc.undoButton, doc.retranscribeButton, doc.evaluationButton,
		doc.transcriptViewButton, doc.copyButton, doc.saveButton,
	} {
		button.compact = true
	}
	header := container.New(layout.NewCustomPaddedVBoxLayout(2),
		container.NewBorder(nil, nil, nil, container.NewHBox(provider, doc.qualityBars,
			doc.transcriptZoomButton, doc.transcriptCollapse), doc.transcriptName),
		d.promptNavigation(doc),
		d.smallText(doc.sourceLabel),
	)
	actions := container.NewVBox(
		d.smallText(doc.errorLabel),
		container.NewHBox(doc.retryButton, layout.NewSpacer()),
		container.NewHBox(doc.clearButton, doc.undoButton, doc.retranscribeButton, doc.evaluationButton,
			layout.NewSpacer(), doc.transcriptViewButton, doc.copyButton, doc.saveButton),
	)
	doc.transcriptPanel = newTranscriptPanel(d, doc, panel(container.NewBorder(
		header, actions, nil, nil,
		d.transcriptPresentation(doc),
	)))
	processedPanel := d.buildProcessingPanel(doc)
	doc.editors = container.NewHSplit(doc.transcriptPanel, processedPanel)
	d.arrangeDocumentEditors(doc)
	doc.item = container.NewTabItem(uiTextf("Aba %d", len(d.groupDocuments(group))+1), doc.editors)
	d.documents = slices.Insert(d.documents, d.documentInsertion(group), doc)
	d.syncDocumentActions(doc)
	return doc
}

func (d *desktop) smallText(object fyne.CanvasObject) fyne.CanvasObject {
	return container.NewThemeOverride(object, textTheme{
		Theme: d.application.Settings().Theme(), size: 11,
	})
}

func (d *desktop) addDocument() *document {
	if len(d.documents) >= maxDocuments {
		d.status.SetText("Feche uma aba antes de abrir mais.")
		return nil
	}
	doc := d.createDocument()
	d.tabs.Append(doc.item)
	d.tabs.Select(doc.item)
	return doc
}

func (d *desktop) selectDocument(doc *document) {
	if d.selectingTabs || doc == nil || doc.closed || doc == d.document {
		return
	}
	d.hints.hide()
	d.rememberPanelSizes(d.document)
	d.rememberDocumentView()
	d.stopPlayback()
	d.document = doc
	d.restoreOuterPanelSizes(doc)
	d.syncDocumentProvider(doc)
	d.renderDocument(doc)
	d.syncControls()
	// The compatibility snapshot always describes the selected tab; a result
	// completed in another tab cannot overwrite it.
	var err error
	if doc.lastResult != nil {
		result := *doc.lastResult
		result.Text, _ = d.currentTexts(doc)
		err = store.WriteJSON(d.latestPath(), result)
	} else {
		err = d.removeLatest()
	}
	if err != nil {
		d.showDocumentError(doc, "Não foi possível atualizar a transcrição selecionada: "+err.Error(), "")
	}
}

func (d *desktop) rememberDocumentView() {
	if d.document == nil || d.capture != captureIdle {
		return
	}
	timer := d.timer.Text
	if d.playback != nil && d.playback.document == d.document {
		timer = d.view.timer
	}
	d.view = documentView{
		timer: timer, recordText: d.recordState.Text, importance: d.recordState.Importance,
		status: d.status.Text, progress: d.progress.Value,
	}
}

func (d *desktop) renderDocument(doc *document) {
	if doc != d.document || doc.closed || d.capture != captureIdle {
		return
	}
	setLabel(d.timer, doc.view.timer, textColor)
	d.recordState.Importance = doc.view.importance
	d.recordState.SetText(doc.view.recordText)
	d.status.SetText(doc.view.status)
	d.progress.SetValue(doc.view.progress)
	d.renderPlayback()
}

func (d *desktop) syncDocumentActions(doc *document) {
	if doc == nil || doc.editor == nil {
		return
	}
	hasText := strings.TrimSpace(doc.editor.Text) != ""
	setEnabled(doc.copyButton, hasText)
	setEnabled(doc.saveButton, hasText)
	setEnabled(doc.undoButton, doc.undo != nil)
	doc.clearButton.Enable()
	setEnabled(doc.retryButton, !doc.busy && d.capture == captureIdle && doc.lastAudio != "" && !doc.audioMissing)
	setEnabled(doc.retranscribeButton, !doc.busy && d.capture == captureIdle && doc.lastAudio != "" && !doc.audioMissing)
	d.syncTranscriptEvaluation(doc)
	if doc.processEditor != nil {
		d.syncProcessingActions(doc)
	}
	d.syncPromptNavigation(doc)
}

func (d *desktop) invalidateDocumentJob(doc *document) {
	d.cancelMergeForGroup(doc.group, "As transcrições do grupo mudaram. Clique em Mesclar para usar os textos atuais.")
	if doc.jobCancel != nil {
		doc.jobCancel()
		doc.jobCancel = nil
	}
	doc.generation++
	doc.busy = false
	doc.jobProgress = transcript.Progress{}
	doc.jobAppend = nil
}

func (d *desktop) launchDocument(doc *document, generation uint64, work func()) {
	d.workers.Go(func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				slog.Error("falha em uma tarefa de transcrição", "panic", recovered)
				d.postJob(doc, generation, func() {
					d.invalidateDocumentJob(doc)
					d.showDocumentError(doc, "Não foi possível concluir a transcrição. Tente novamente.", "")
					if doc.lastAudio != "" {
						doc.retryButton.Show()
					}
					d.reveal()
				})
			}
		}()
		work()
	})
}

func (d *desktop) hasJobs() bool {
	if d.mergeBusy {
		return true
	}
	for _, doc := range d.documents {
		if doc.busy || doc.processBusy {
			return true
		}
	}
	return false
}

func (d *desktop) finishWhenReady() {
	if d.closeAfterJob && !d.hasJobs() {
		d.finish()
	}
}

func (d *desktop) undoClear() {
	if d.document.browsingPrompts() {
		return
	}
	if d.undo == nil {
		return
	}
	undo := *d.undo
	if err := d.saveDraft(); err != nil {
		d.showError(err.Error(), "")
		return
	}
	if !sameAudio(d.lastAudio, undo.Source) {
		if !d.preserveAudioHistory(d.document) {
			return
		}
		d.stopDocumentPlayback(d.document)
	}
	d.invalidateJob()
	d.invalidateProcessing(d.document)
	d.editor.SetText(undo.Text)
	d.lastAudio = undo.Source
	d.audioMissing = undo.AudioMissing
	d.lastResult = undo.Result
	d.recordedAppend = undo.Append
	d.transcriptionRun = undo.TranscriptionRun
	d.restoreLiveArchive(d.document, undo.Live)
	d.undo = nil
	d.errorLabel.Hide()
	if d.lastResult != nil {
		result := *d.lastResult
		result.Text = undo.Text
		if err := d.persistDocumentResult(d.document, result); err != nil {
			d.showError("O texto foi restaurado, mas não foi possível salvar: "+err.Error(), "")
			return
		}
	}
	d.view.status = "Limpeza desfeita."
	d.renderDocument(d.document)
	d.syncControls()
}

func (d *desktop) closeDocument(item *container.TabItem) {
	var doc *document
	for _, candidate := range d.documents {
		if candidate.item == item {
			doc = candidate
			break
		}
	}
	if doc == nil || doc.closed {
		return
	}
	if d.capture != captureIdle && d.captureDocument == doc {
		d.status.SetText("Pare a gravação antes de fechar esta aba.")
		return
	}
	if err := d.saveDocumentDrafts(doc); err != nil {
		d.showDocumentError(doc, err.Error(), "")
		return
	}
	// A recognized transcription or generated answer is also preserved when
	// closing its tab, even if the user never edited that text.
	if err := d.writeDraft(strings.TrimSpace(doc.editor.Text), "", &doc.draftText, "Texto"); err != nil {
		d.showDocumentError(doc, err.Error(), "")
		return
	}
	if err := d.writeDraft(strings.TrimSpace(doc.processEditor.Text), "", &doc.processDraftText, "Processado"); err != nil {
		d.showDocumentError(doc, err.Error(), "")
		return
	}
	if doc == d.document {
		d.rememberDocumentView()
	}
	closed := &closedDocument{
		document: doc, index: slices.Index(d.groupDocuments(doc.group), doc),
		group:      doc.group,
		groupIndex: slices.Index(d.groups, doc.group), groupName: d.groupNames[doc.group],
		groupNameWidth:     d.groupNameWidths[doc.group],
		groupAutomaticName: d.groupAutomaticNames[doc.group],
	}
	original := d.snapshotWorkspace()
	successor := original
	successor.Tabs = nil
	remaining := make([]*document, 0, len(d.documents)-1)
	for index, candidate := range d.documents {
		if candidate != doc {
			remaining = append(remaining, candidate)
			successor.Tabs = append(successor.Tabs, original.Tabs[index])
		}
	}
	selected := d.document
	if len(remaining) == 0 {
		successor.NextID++
		successor.Selected = successor.NextID
		successor.Tabs = []documentSnapshot{{
			ID: successor.NextID, Group: doc.group, Name: "Aba 1", Options: d.settings.Processing,
		}}
	} else if len(d.groupDocuments(doc.group)) == 1 {
		closed.groupRemoved = true
		successor.Groups = slices.Delete(slices.Clone(original.Groups), closed.groupIndex, closed.groupIndex+1)
		successor.GroupNames = maps.Clone(original.GroupNames)
		delete(successor.GroupNames, doc.group)
		successor.GroupAutomaticNames = maps.Clone(original.GroupAutomaticNames)
		delete(successor.GroupAutomaticNames, doc.group)
		if selected == doc {
			neighbor := successor.Groups[min(closed.groupIndex, len(successor.Groups)-1)]
			for _, candidate := range remaining {
				if candidate.group == neighbor {
					selected = candidate
					break
				}
			}
			successor.Selected = selected.id
		}
	} else if selected == doc {
		for _, candidate := range remaining {
			if candidate.group == doc.group {
				selected = candidate
				break
			}
		}
		successor.Selected = selected.id
	}
	if err := d.archiveDocumentEvaluation(doc); err != nil {
		d.showDocumentError(doc, "Não foi possível preservar a avaliação interna: "+err.Error(), "")
		return
	}
	closedEntries, closedArchive := d.prepareClosure(closed)
	successor.ClosedTabs = closedArchive
	if len(remaining) == 0 {
		replacement := successor.Tabs[0]
		successor.ClosedTabs[len(successor.ClosedTabs)-1].Replacement = &replacement
		successor.ClosedTabs[len(successor.ClosedTabs)-1].ReplacementUntouched = true
	}
	// Commit the remaining groups before cancelling tasks or removing an
	// editor. A failed archive or atomic write keeps the original tab open.
	if err := d.writeWorkspace(successor); err != nil {
		d.showDocumentError(doc, "Não foi possível salvar o fechamento da aba: "+err.Error(), "")
		return
	}
	d.cancelMergeForGroup(doc.group, "As abas do grupo mudaram. Clique em Mesclar para usar os textos atuais.")
	if doc.busy {
		doc.view.recordText = "Transcrição cancelada ao fechar a aba."
		doc.view.status = "Você pode tentar novamente com o áudio preservado."
	}
	if doc.processBusy {
		doc.processStatus.SetText("Processamento cancelado ao fechar a aba.")
	}
	d.invalidateDocumentJob(doc)
	d.invalidateProcessing(doc)
	d.hints.hide()
	doc.closed = true
	d.documents = remaining
	d.groups, d.groupNames = successor.Groups, successor.GroupNames
	d.groupAutomaticNames = successor.GroupAutomaticNames
	if len(remaining) == 0 {
		closed.replacement = d.createDocumentInGroup(doc.group)
		selected = closed.replacement
	}
	d.selectingTabs = true
	d.tabs.SetItems(documentItems(d.documents))
	d.tabs.Select(selected.item)
	d.selectingTabs = false
	d.renumberDocuments()
	d.selectDocument(selected)
	d.publishClosedDocuments(closedEntries, closed.until)
	d.syncControls()
}

func (d *desktop) documentLatestPath(doc *document) string {
	return filepath.Join(d.directory, "abas", fmt.Sprintf("%d", doc.id), "last-transcript.json")
}

func (d *desktop) persistDocumentResult(doc *document, result transcript.Result) error {
	if err := store.WriteJSON(d.documentLatestPath(doc), result); err != nil {
		return err
	}
	if doc == d.document {
		return store.WriteJSON(d.latestPath(), result)
	}
	return nil
}

func (d *desktop) removeDocumentLatest(doc *document) error {
	err := os.Remove(d.documentLatestPath(doc))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("não foi possível limpar o resultado da aba: %w", err)
	}
	if doc == d.document {
		return d.removeLatest()
	}
	return nil
}

type workspaceSnapshot struct {
	Version             int                 `json:"version"`
	Selected            uint64              `json:"selected"`
	NextID              uint64              `json:"next_id"`
	Tabs                []documentSnapshot  `json:"tabs"`
	Groups              []uint64            `json:"groups,omitempty"`
	GroupNames          map[uint64]string   `json:"group_names,omitempty"`
	GroupAutomaticNames map[uint64]string   `json:"group_automatic_names,omitempty"`
	GroupNameWidths     map[uint64]float32  `json:"group_name_widths,omitempty"`
	NextGroup           uint64              `json:"next_group,omitempty"`
	ClosedTabs          []closedTabSnapshot `json:"closed_tabs,omitempty"`
}

type documentSnapshot struct {
	ID                  uint64                `json:"id"`
	Group               uint64                `json:"group,omitempty"`
	Name                string                `json:"name"`
	Renamed             bool                  `json:"renamed,omitempty"`
	Text                string                `json:"text"`
	Source              string                `json:"source"`
	Result              *transcript.Result    `json:"result,omitempty"`
	Evaluation          *transcriptEvaluation `json:"evaluation,omitempty"`
	AudioMissing        bool                  `json:"audio_missing,omitempty"`
	Undo                *clearUndo            `json:"undo,omitempty"`
	ProcessedText       string                `json:"processed_text"`
	Processed           *processing.Result    `json:"processed,omitempty"`
	Options             processing.Options    `json:"processing"`
	Append              *recordedAppend       `json:"recording_append,omitempty"`
	Retry               bool                  `json:"retry,omitempty"`
	ScreenshotFiles     []string              `json:"screenshot_files,omitempty"`
	Conversation        []processing.Turn     `json:"conversation,omitempty"`
	Live                *liveArchive          `json:"live,omitempty"`
	TranscriptionRun    string                `json:"transcription_run,omitempty"`
	AudioHistory        []audioSnapshot       `json:"audio_history,omitempty"`
	ProcessCollapsed    bool                  `json:"process_collapsed,omitempty"`
	ProcessOffset       float64               `json:"process_offset,omitempty"`
	PromptSteps         []promptStep          `json:"prompt_steps,omitempty"`
	TranscriptCollapsed bool                  `json:"transcript_collapsed,omitempty"`
	PanelSizes          panelSizes            `json:"panel_sizes,omitempty"`
	TranscriptZoom      int                   `json:"transcript_zoom,omitempty"`
	ProcessZoom         int                   `json:"process_zoom,omitempty"`
	Voice               *voiceArchive         `json:"voice,omitempty"`
}

func (d *desktop) saveWorkspace() error {
	return d.writeWorkspace(d.snapshotWorkspace())
}

func (d *desktop) snapshotWorkspace() workspaceSnapshot {
	d.rememberPanelSizes(d.document)
	snapshot := workspaceSnapshot{
		Version: 1, Selected: d.id, NextID: d.nextDocument, Groups: d.groups, GroupNames: d.groupNames, NextGroup: d.nextGroup,
		GroupNameWidths:     maps.Clone(d.groupNameWidths),
		GroupAutomaticNames: maps.Clone(d.groupAutomaticNames),
	}
	for _, doc := range d.documents {
		snapshot.Tabs = append(snapshot.Tabs, d.snapshotDocument(doc))
	}
	snapshot.ClosedTabs = d.archiveClosedDocuments(d.closedTabs, time.Time{})
	return snapshot
}

func (d *desktop) snapshotDocument(doc *document) documentSnapshot {
	d.syncPromptDraft(doc)
	var liveState *liveArchive
	if doc.live != nil {
		liveState = doc.live.archive()
	}
	prompt, response := d.currentTexts(doc)
	return documentSnapshot{
		ID: doc.id, Group: doc.group, Name: doc.item.Text, Renamed: doc.name != "", Text: prompt,
		Source: doc.lastAudio, Result: doc.lastResult, Undo: doc.undo,
		Evaluation: doc.evaluation, AudioMissing: doc.audioMissing,
		ProcessedText: response, Processed: doc.processResult,
		Options: doc.processOptions,
		Append:  doc.recordedAppend, Retry: doc.retryButton.Visible(),
		ScreenshotFiles:     doc.screenshotFiles,
		Conversation:        doc.processHistory,
		Live:                liveState,
		TranscriptionRun:    doc.transcriptionRun,
		AudioHistory:        d.audioSnapshots(doc),
		ProcessCollapsed:    doc.processCollapsed,
		ProcessOffset:       doc.processOffset,
		PromptSteps:         slices.Clone(doc.promptSteps),
		TranscriptCollapsed: doc.transcriptCollapsed,
		PanelSizes:          doc.panelSizes,
		TranscriptZoom:      normalizedEditorZoom(doc.transcriptZoom),
		ProcessZoom:         normalizedEditorZoom(doc.processZoom),
		Voice:               archiveVoice(doc.voice),
	}
}

func (d *desktop) writeWorkspace(snapshot workspaceSnapshot) error {
	snapshot.GroupNameWidths = maps.Clone(snapshot.GroupNameWidths)
	for group := range snapshot.GroupNameWidths {
		if !slices.Contains(snapshot.Groups, group) {
			delete(snapshot.GroupNameWidths, group)
		}
	}
	path := filepath.Join(d.directory, "workspace-go-v1.json")
	if d.workspaceInvalid {
		original, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("preservar o arquivo de abas anterior: %w", err)
		}
		name := "workspace-go-v1-invalid-" + d.deps.now().Format("20060102-150405.000000000") + ".json"
		if err := store.Write(filepath.Join(d.directory, name), original); err != nil {
			return fmt.Errorf("preservar o arquivo de abas anterior: %w", err)
		}
	}
	write := d.deps.writeWorkspace
	if write == nil {
		write = func(path string, snapshot workspaceSnapshot) error { return store.WriteJSON(path, snapshot) }
	}
	if err := write(path, snapshot); err != nil {
		return err
	}
	d.workspaceInvalid = false
	return nil
}

func (d *desktop) loadWorkspace() bool {
	var snapshot workspaceSnapshot
	err := store.ReadJSON(filepath.Join(d.directory, "workspace-go-v1.json"), &snapshot)
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	if err != nil || snapshot.Version != 1 || len(snapshot.Tabs) == 0 || len(snapshot.Tabs) > maxDocuments {
		d.workspaceInvalid = true
		d.showError("Não foi possível ler as abas anteriores. O arquivo foi preservado.", "")
		return true
	}
	if !normalizeWorkspaceGroups(&snapshot) {
		d.workspaceInvalid = true
		d.showError("Os grupos anteriores têm dados inválidos. O arquivo foi preservado.", "")
		return true
	}
	for group, width := range snapshot.GroupNameWidths {
		if !slices.Contains(snapshot.Groups, group) || width < minGroupNameWidth || width > maxGroupNameWidth {
			d.workspaceInvalid = true
			d.showError("Os limites dos nomes de grupo têm dados inválidos. O arquivo foi preservado.", "")
			return true
		}
	}
	seen := make(map[uint64]bool)
	for index := range snapshot.Tabs {
		tab := &snapshot.Tabs[index]
		tab.Options = tab.Options.WithLegacyMerge(d.settings.Merge)
		if tab.ID == 0 || seen[tab.ID] || tab.ID > snapshot.NextID || tab.Options.Validate() != nil ||
			!d.validLiveArchive(tab.Live) || !validTranscriptionRun(tab.TranscriptionRun) ||
			!d.validAudioHistory(tab.AudioHistory) || !validPromptSteps(tab.PromptSteps) || !tab.PanelSizes.valid() ||
			!validEditorZoom(tab.TranscriptZoom) || !validEditorZoom(tab.ProcessZoom) ||
			!validTranscriptEvaluation(tab.Evaluation) ||
			tab.Undo != nil && !validTranscriptionRun(tab.Undo.TranscriptionRun) {
			d.workspaceInvalid = true
			d.showError("As abas anteriores têm dados inválidos. O arquivo foi preservado.", "")
			return true
		}
		seen[tab.ID] = true
	}
	if !d.validClosedDocuments(&snapshot) {
		d.workspaceInvalid = true
		d.showError("As abas anteriores têm dados inválidos. O arquivo foi preservado.", "")
		return true
	}
	d.selectingTabs = true
	for _, doc := range d.documents {
		doc.closed = true
	}
	d.documents = nil
	d.groups, d.nextGroup = snapshot.Groups, snapshot.NextGroup
	d.groupNames = maps.Clone(snapshot.GroupNames)
	d.groupAutomaticNames = maps.Clone(snapshot.GroupAutomaticNames)
	d.groupNameWidths = maps.Clone(snapshot.GroupNameWidths)
	if d.groupNameWidths == nil {
		d.groupNameWidths = make(map[uint64]float32)
	}
	if d.groupNames == nil {
		d.groupNames = make(map[uint64]string)
	}
	d.nextDocument = snapshot.NextID
	items := make([]*container.TabItem, 0, len(snapshot.Tabs))
	var selected *document
	for _, tab := range snapshot.Tabs {
		doc := d.createDocumentInGroup(tab.Group)
		doc.id = tab.ID
		if tab.Renamed || !automaticTabName(tab.Name) {
			doc.name = tab.Name
		}
		doc.lastAudio = tab.Source
		doc.lastResult = tab.Result
		doc.evaluation = tab.Evaluation
		doc.audioMissing = tab.AudioMissing
		doc.transcriptionRun = tab.TranscriptionRun
		doc.audioHistory = tab.AudioHistory
		doc.processResult = tab.Processed
		doc.processOptions = tab.Options
		doc.processCollapsed = tab.ProcessCollapsed
		doc.processOffset = tab.ProcessOffset
		doc.transcriptCollapsed = tab.TranscriptCollapsed
		doc.panelSizes = tab.PanelSizes.normalized(tab.ProcessOffset)
		d.arrangeDocumentEditors(doc)
		doc.processHistory = tab.Conversation
		doc.screenshotFiles = tab.ScreenshotFiles
		doc.historyApplying = true
		doc.editor.SetText(tab.Text)
		d.updateTranscriptLinks(doc, true)
		doc.processEditor.SetText(tab.ProcessedText)
		doc.historyApplying = false
		d.restorePromptHistory(doc, tab.PromptSteps)
		d.updateResultLinks(doc, true)
		doc.undo = tab.Undo
		doc.recordedAppend = tab.Append
		d.restoreLiveArchive(doc, tab.Live)
		d.restoreVoice(doc, tab.Voice)
		// Draft markers describe successful archive writes in this process.
		// Restored text may exist only in the workspace snapshot.
		if tab.Result != nil {
			doc.view.timer = clockText(tab.Result.DurationSeconds)
			doc.sourceLabel.SetText("Português + inglês · sem traduzir sua fala")
			if tab.Result.NeedsReview {
				doc.retryButton.Show()
			}
		}
		if tab.Source != "" && !tab.AudioMissing && (tab.Retry || tab.Result == nil || doc.live != nil && doc.live.needsRetry()) {
			doc.retryButton.Show()
		}
		d.syncTranscriptionDetails(doc)
		doc.view.status = "Você pode continuar editando, copiar ou processar."
		d.syncProcessingOptions(doc)
		d.syncDocumentActions(doc)
		items = append(items, doc.item)
		if tab.ID == snapshot.Selected {
			selected = doc
		}
	}
	d.restoreClosedDocuments(snapshot.ClosedTabs)
	d.nextDocument = snapshot.NextID
	if selected == nil {
		selected = d.documents[0]
	}
	d.tabs.SetItems(items)
	d.document = selected
	d.restoreOuterPanelSizes(selected)
	d.tabs.Select(selected.item)
	d.selectingTabs = false
	d.renumberDocuments()
	d.renderDocument(selected)
	d.syncControls()
	return true
}

func (d *desktop) saveAllDrafts() error {
	for _, doc := range d.documents {
		if err := d.saveDocumentDrafts(doc); err != nil {
			return err
		}
	}
	return nil
}
