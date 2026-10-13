package desktop

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"ai-talker/internal/store"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const maxEvaluationCommentRunes = 2000

type transcriptEvaluation struct {
	Score     int               `json:"score"`
	Comment   string            `json:"comment,omitempty"`
	Reference string            `json:"reference"`
	UpdatedAt time.Time         `json:"updated_at"`
	Context   []evaluationStage `json:"context,omitempty"`
}

type evaluationSegment struct {
	Start      float64 `json:"start,omitempty"`
	End        float64 `json:"end,omitempty"`
	Track      string  `json:"track,omitempty"`
	Label      string  `json:"label,omitempty"`
	Text       string  `json:"text,omitempty"`
	Screenshot bool    `json:"screenshot,omitempty"`
	Path       string  `json:"path,omitempty"`
}

type evaluationStage struct {
	Source       string              `json:"source,omitempty"`
	PromptID     uint64              `json:"prompt_id,omitempty"`
	ModelID      string              `json:"model_id,omitempty"`
	JobID        string              `json:"job_id,omitempty"`
	RunID        string              `json:"run_id,omitempty"`
	AudioMissing bool                `json:"audio_missing,omitempty"`
	Tracks       []string            `json:"tracks,omitempty"`
	Text         string              `json:"text"`
	Screenshots  []string            `json:"screenshots,omitempty"`
	Segments     []evaluationSegment `json:"segments,omitempty"`
}

type closedEvaluation struct {
	TabID      uint64               `json:"tab_id"`
	GroupID    uint64               `json:"group_id"`
	ClosedAt   time.Time            `json:"closed_at"`
	Evaluation transcriptEvaluation `json:"evaluation"`
	Context    []evaluationStage    `json:"context"`
}

type evaluationArchive struct {
	Version int                `json:"version"`
	Entries []closedEvaluation `json:"entries"`
}

func validTranscriptEvaluation(value *transcriptEvaluation) bool {
	if value == nil {
		return true
	}
	return value.Score >= 0 && value.Score <= 5 &&
		len(value.Reference) == sha256.Size*2 &&
		strings.Trim(value.Reference, "0123456789abcdef") == "" &&
		utf8.ValidString(value.Comment) &&
		utf8.RuneCountInString(value.Comment) <= maxEvaluationCommentRunes &&
		!value.UpdatedAt.IsZero() &&
		(len(value.Context) == 0 || evaluationReference(value.Context) == value.Reference)
}

func (d *desktop) evaluationContext(doc *document) []evaluationStage {
	if doc == nil {
		return nil
	}
	stages := make([]evaluationStage, 0, len(doc.audioHistory)+len(doc.promptSteps)+1)
	for _, step := range doc.promptSteps {
		origin := step.Transcription
		if origin == nil || origin.Source == "" {
			continue
		}
		text := step.Prompt
		if step.Draft != nil {
			text = *step.Draft
		}
		stage := evaluationStage{
			Source: origin.Source, PromptID: step.ID, ModelID: origin.ModelID,
			JobID: origin.JobID, RunID: origin.RunID, Text: text,
			Screenshots: slices.Clone(origin.Screenshots),
		}
		// Partial recognition and edits share one final stage for a run.
		index := slices.IndexFunc(stages, func(previous evaluationStage) bool {
			return sameAudio(previous.Source, stage.Source) && previous.RunID == stage.RunID
		})
		if index >= 0 {
			stages[index] = stage
		} else {
			stages = append(stages, stage)
		}
	}
	appendSnapshot := func(snapshot audioSnapshot) {
		if snapshot.Source == "" {
			return
		}
		stage := evaluationStage{
			Source: snapshot.Source, AudioMissing: snapshot.AudioMissing, Text: snapshot.Text,
			Screenshots: slices.Clone(snapshot.ScreenshotFiles),
			RunID:       snapshot.TranscriptionRun,
		}
		if snapshot.Result != nil {
			stage.ModelID, stage.JobID = snapshot.Result.ModelID, snapshot.Result.JobID
		}
		if snapshot.Live != nil {
			for _, track := range snapshot.Live.Tracks {
				if track.Snapshot.Path != "" {
					stage.Tracks = append(stage.Tracks, track.Snapshot.Path)
				}
			}
			for _, entry := range snapshot.Live.Entries {
				stage.Segments = append(stage.Segments, evaluationSegment{
					Start: entry.Start, End: entry.End, Track: entry.Track,
					Label: entry.Label, Text: entry.Text, Screenshot: entry.Screenshot,
					Path: entry.Path,
				})
			}
		}
		index := slices.IndexFunc(stages, func(previous evaluationStage) bool {
			return sameAudio(previous.Source, stage.Source) && previous.RunID == stage.RunID
		})
		if index >= 0 {
			stage.PromptID = stages[index].PromptID
			stages[index] = stage
		} else {
			stages = append(stages, stage)
		}
	}
	for _, saved := range doc.audioHistory {
		if !saved.HistoryRemoved && !sameAudio(saved.Source, doc.lastAudio) {
			appendSnapshot(saved)
		}
	}
	appendSnapshot(d.snapshotAudio(doc))
	return stages
}

func cloneEvaluationContext(stages []evaluationStage) []evaluationStage {
	cloned := slices.Clone(stages)
	for index := range cloned {
		cloned[index].Tracks = slices.Clone(cloned[index].Tracks)
		cloned[index].Screenshots = slices.Clone(cloned[index].Screenshots)
		cloned[index].Segments = slices.Clone(cloned[index].Segments)
	}
	return cloned
}

func evaluationReference(stages []evaluationStage) string {
	payload, err := json.Marshal(stages)
	if err != nil || len(stages) == 0 {
		return ""
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func upgradeLegacyEvaluation(value *transcriptEvaluation, stages []evaluationStage) bool {
	reference := evaluationReference(stages)
	if reference == "" {
		return false
	}
	if value.Reference != reference {
		legacy := cloneEvaluationContext(stages)
		for index := range legacy {
			legacy[index].PromptID = 0
			legacy[index].ModelID, legacy[index].JobID, legacy[index].RunID = "", "", ""
		}
		if value.Reference != evaluationReference(legacy) {
			return false
		}
	}
	value.Reference, value.Context = reference, cloneEvaluationContext(stages)
	return true
}

func (d *desktop) transcriptEvaluationReference(doc *document) string {
	if doc == nil || doc.lastAudio == "" || doc.audioMissing ||
		strings.TrimSpace(doc.editor.Text) == "" || doc.browsingPrompts() {
		return ""
	}
	return evaluationReference(d.evaluationContext(doc))
}

func (d *desktop) syncTranscriptEvaluation(doc *document) {
	if doc.evaluationButton == nil {
		return
	}
	available := !doc.closed && doc.lastAudio != "" &&
		strings.TrimSpace(doc.editor.Text) != "" && !doc.audioMissing && !doc.browsingPrompts()
	if !available {
		doc.evaluationButton.Hide()
		return
	}
	reference := d.transcriptEvaluationReference(doc)
	if doc.evaluation != nil && len(doc.evaluation.Context) == 0 {
		if !upgradeLegacyEvaluation(doc.evaluation, d.evaluationContext(doc)) {
			doc.evaluation = nil
		}
	}
	doc.evaluationButton.Show()
	setEnabled(doc.evaluationButton,
		!doc.busy && d.capture == captureIdle && !d.closing && reference != "")
	if doc.evaluation != nil {
		doc.evaluationButton.SetText("★")
		doc.evaluationButton.Importance = widget.HighImportance
		doc.evaluationButton.setHint(fmt.Sprintf("Avaliação atual: %d de 5 · clicar para reavaliar", doc.evaluation.Score))
	} else {
		doc.evaluationButton.SetText("☆")
		doc.evaluationButton.Importance = widget.LowImportance
		doc.evaluationButton.setHint(uiText("Avaliar esta transcrição"))
	}
}

func (d *desktop) openTranscriptEvaluation(doc *document) {
	if doc == nil || doc.closed || doc != d.document || doc.busy || d.capture != captureIdle ||
		doc.browsingPrompts() || doc.lastAudio == "" || strings.TrimSpace(doc.editor.Text) == "" {
		return
	}
	d.returnToCurrentPrompt(doc)
	selected := 0
	if doc.evaluation != nil {
		selected = doc.evaluation.Score
	}
	var form *dialog.CustomDialog
	stars := make([]*widget.Button, 5)
	refreshStars := func() {
		for index, star := range stars {
			label := "☆"
			if index < selected {
				label = "★"
			}
			star.SetText(label)
		}
	}
	starRow := container.NewHBox()
	for index := range stars {
		score := index + 1
		stars[index] = widget.NewButton(uiText("☆"), d.guard(func() {
			if selected == score {
				selected = 0
			} else {
				selected = score
			}
			refreshStars()
		}))
		starRow.Add(stars[index])
	}
	refreshStars()
	description := widget.NewLabel("Escolha de 1 a 5 estrelas. Sem estrelas, OK registra nota zero.")
	description.Wrapping = fyne.TextWrapWord
	confirm := widget.NewButtonWithIcon(uiText("OK"), theme.ConfirmIcon(), d.guard(func() {
		form.Hide()
		d.openEvaluationComment(doc, selected)
	}))
	confirm.Importance = widget.HighImportance
	cancel := widget.NewButton(uiText("Cancelar"), d.guard(func() { form.Hide() }))
	form = dialog.NewCustomWithoutButtons(uiText("Avaliar transcrição"),
		container.NewVBox(description, starRow, container.NewHBox(layout.NewSpacer(), cancel, confirm)),
		d.window)
	form.Resize(fyne.NewSize(360, 190))
	form.Show()
}

func (d *desktop) openEvaluationComment(doc *document, score int) {
	if doc == nil || doc.closed || doc != d.document || d.capture != captureIdle ||
		doc.lastAudio == "" || strings.TrimSpace(doc.editor.Text) == "" {
		return
	}
	entry := widget.NewMultiLineEntry()
	entry.Wrapping = fyne.TextWrapWord
	entry.SetMinRowsVisible(3)
	if doc.evaluation != nil {
		entry.SetText(doc.evaluation.Comment)
	}
	errorLabel := widget.NewLabel("")
	errorLabel.Importance = widget.DangerImportance
	errorLabel.Wrapping = fyne.TextWrapWord
	errorLabel.Hide()
	var form *dialog.CustomDialog
	send := widget.NewButton(uiText("Enviar"), d.guard(func() {
		comment := strings.TrimSpace(entry.Text)
		if utf8.RuneCountInString(comment) > maxEvaluationCommentRunes {
			errorLabel.SetText("O comentário pode ter até 2000 caracteres.")
			errorLabel.Show()
			return
		}
		context := d.evaluationContext(doc)
		reference := evaluationReference(context)
		if reference == "" {
			form.Hide()
			return
		}
		previous := doc.evaluation
		doc.evaluation = &transcriptEvaluation{
			Score: score, Comment: comment, Reference: reference, UpdatedAt: d.deps.now(),
			Context: cloneEvaluationContext(context),
		}
		if err := d.saveWorkspace(); err != nil {
			doc.evaluation = previous
			errorLabel.SetText("Não foi possível salvar a avaliação. Tente novamente.")
			errorLabel.Show()
			return
		}
		form.Hide()
		d.syncDocumentActions(doc)
	}))
	send.Importance = widget.HighImportance
	cancel := widget.NewButton(uiText("Cancelar"), d.guard(func() { form.Hide() }))
	form = dialog.NewCustomWithoutButtons(uiText("Comentário opcional"),
		container.NewVBox(
			widget.NewLabel(fmt.Sprintf("Nota %d de 5. Enviar sem comentário também salva a avaliação.", score)),
			entry, errorLabel, container.NewHBox(layout.NewSpacer(), cancel, send),
		), d.window)
	form.Resize(fyne.NewSize(460, 300))
	form.Show()
	d.window.Canvas().Focus(entry)
}

func (d *desktop) archiveDocumentEvaluation(doc *document) error {
	if doc == nil || doc.evaluation == nil || !validTranscriptEvaluation(doc.evaluation) {
		return nil
	}
	stages := doc.evaluation.Context
	if len(stages) == 0 {
		stages = d.evaluationContext(doc)
		if !upgradeLegacyEvaluation(doc.evaluation, stages) {
			doc.evaluation = nil
			return nil
		}
	}
	if evaluationReference(stages) != doc.evaluation.Reference {
		doc.evaluation = nil
		return nil
	}
	path := filepath.Join(d.directory, "avaliacoes-fechadas-v1.json")
	archive := evaluationArchive{Version: 1}
	err := store.ReadJSON(path, &archive)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("ler avaliações internas: %w", err)
	}
	if archive.Version != 0 && archive.Version != 1 {
		return errors.New("o arquivo de avaliações internas tem uma versão desconhecida")
	}
	archive.Version = 1
	entry := closedEvaluation{
		TabID: doc.id, GroupID: doc.group, ClosedAt: d.deps.now(),
		Evaluation: *doc.evaluation, Context: cloneEvaluationContext(stages),
	}
	entry.Evaluation.Context = nil // The archive owns a single copy of the context.
	index := slices.IndexFunc(archive.Entries, func(saved closedEvaluation) bool {
		return saved.TabID == doc.id
	})
	if index >= 0 {
		archive.Entries[index] = entry
	} else {
		archive.Entries = append(archive.Entries, entry)
	}
	if err := store.WriteJSON(path, archive); err != nil {
		return fmt.Errorf("salvar avaliação interna: %w", err)
	}
	return nil
}

func evaluationContainsScreenshot(stages []evaluationStage, path string) bool {
	for _, stage := range stages {
		if slices.ContainsFunc(stage.Screenshots, func(candidate string) bool { return sameAudio(candidate, path) }) ||
			slices.ContainsFunc(stage.Segments, func(segment evaluationSegment) bool {
				return segment.Screenshot && sameAudio(segment.Path, path)
			}) {
			return true
		}
	}
	return false
}

func (d *desktop) evaluationLinkedToCleanup(stages []evaluationStage, tabID uint64, item cleanupItem) bool {
	if item.HistoryItem {
		return item.HistoryID == tabID
	}
	if item.Category == cleanupHistory && item.DocID != tabID {
		return false
	}
	containsPath := func(path string) bool {
		return sameAudio(path, item.Source) || slices.ContainsFunc(item.Paths, func(candidate string) bool {
			return sameAudio(candidate, path)
		})
	}
	for _, stage := range stages {
		switch item.Category {
		case cleanupScreenshots:
			if evaluationContainsScreenshot([]evaluationStage{stage}, item.Source) {
				return true
			}
		case cleanupRecordings:
			if containsPath(stage.Source) || slices.ContainsFunc(stage.Tracks, containsPath) {
				return true
			}
		case cleanupTranscriptions, cleanupHistory:
			if sameAudio(stage.Source, item.Source) ||
				item.DocID == tabID && item.Source == "" {
				return true
			}
			if item.Category == cleanupTranscriptions {
				if validCacheJobID(stage.JobID) && containsPath(filepath.Join(d.directory, "jobs-go-v1", stage.JobID)) ||
					stage.RunID != "" && validTranscriptionRun(stage.RunID) &&
						containsPath(filepath.Join(d.directory, "jobs-retranscribe-go-v1", stage.RunID)) {
					return true
				}
			}
		}
	}
	return false
}

func (d *desktop) invalidateCleanupEvaluations(items []cleanupItem) {
	for _, doc := range d.cleanupDocuments() {
		if doc.evaluation == nil {
			continue
		}
		context := doc.evaluation.Context
		if len(context) == 0 {
			context = d.evaluationContext(doc)
		}
		if slices.ContainsFunc(items, func(item cleanupItem) bool {
			return d.evaluationLinkedToCleanup(context, doc.id, item)
		}) {
			doc.evaluation = nil
		}
	}
	for _, entry := range d.closedTabs {
		if entry.combined == nil {
			continue
		}
		for index := range entry.combined.states {
			tab := &entry.combined.states[index]
			if tab.Evaluation != nil && slices.ContainsFunc(items, func(item cleanupItem) bool {
				return d.evaluationLinkedToCleanup(tab.Evaluation.Context, tab.ID, item)
			}) {
				tab.Evaluation = nil
			}
		}
	}
}

func (d *desktop) invalidateScreenshotEvaluations(path string) error {
	documents := append([]*document(nil), d.documents...)
	for _, entry := range d.closedTabs {
		documents = append(documents, entry.document)
		if entry.combined != nil {
			for index := range entry.combined.states {
				saved := &entry.combined.states[index]
				if saved.Evaluation != nil && evaluationContainsScreenshot(saved.Evaluation.Context, path) {
					saved.Evaluation = nil
				}
			}
		}
	}
	for _, doc := range documents {
		if doc == nil || doc.evaluation == nil {
			continue
		}
		context := doc.evaluation.Context
		if len(context) == 0 {
			context = d.evaluationContext(doc)
		}
		if evaluationContainsScreenshot(context, path) {
			doc.evaluation = nil
		}
	}
	return d.pruneClosedEvaluations([]cleanupItem{{Category: cleanupScreenshots, Source: path}})
}

func (d *desktop) archiveDocumentEvaluations(documents []*document) error {
	for _, doc := range documents {
		if err := d.archiveDocumentEvaluation(doc); err != nil {
			return err
		}
	}
	return nil
}
