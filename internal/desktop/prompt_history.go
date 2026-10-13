package desktop

import (
	"fmt"
	"slices"
	"unicode/utf8"

	"ai-talker/internal/processing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
)

type promptStep struct {
	ID              uint64               `json:"id"`
	Conversation    uint64               `json:"conversation"`
	Prompt          string               `json:"prompt"`
	Response        string               `json:"response"`
	Result          *processing.Result   `json:"result,omitempty"`
	Sent            bool                 `json:"sent,omitempty"`
	Processed       bool                 `json:"processed,omitempty"`
	Options         *processing.Options  `json:"options,omitempty"`
	Draft           *string              `json:"draft,omitempty"`
	AttemptOptions  *processing.Options  `json:"attempt_options,omitempty"`
	AttemptPrompt   string               `json:"attempt_prompt,omitempty"`
	PreviousOptions []processing.Options `json:"previous_options,omitempty"`
	Transcription   *transcriptionPrompt `json:"transcription,omitempty"`
}

type transcriptionPrompt struct {
	Source      string   `json:"source"`
	ModelID     string   `json:"model_id,omitempty"`
	JobID       string   `json:"job_id,omitempty"`
	RunID       string   `json:"run_id,omitempty"`
	Screenshots []string `json:"screenshots,omitempty"`
}

func (doc *document) browsingPrompts() bool {
	return len(doc.promptSteps) > 0 && doc.promptCursor >= 0 && doc.promptCursor < len(doc.promptSteps)-1
}

func (d *desktop) promptNavigation(doc *document) fyne.CanvasObject {
	doc.promptBack = d.iconButton("Prompt anterior · somente consulta", theme.NavigateBackIcon(), func() { d.navigatePrompt(doc, -1) })
	doc.promptForward = d.iconButton("Próximo prompt", theme.NavigateNextIcon(), func() { d.navigatePrompt(doc, 1) })
	doc.promptLabel = label("", 11, mutedColor)
	doc.promptLabel.truncate = true
	doc.promptBack.Hide()
	doc.promptForward.Hide()
	doc.promptLabel.Hide()
	doc.promptEdit = d.iconButton("Editar este prompt antigo", theme.DocumentCreateIcon(), func() { d.editHistoricalPrompt(doc) })
	doc.promptEdit.Hide()
	return container.NewBorder(nil, nil, container.NewHBox(doc.promptBack, doc.promptForward, doc.promptEdit), nil, doc.promptLabel)
}

func (d *desktop) currentTexts(doc *document) (string, string) {
	if doc.browsingPrompts() {
		return doc.currentPrompt, doc.currentResponse
	}
	return doc.editor.Text, doc.processEditor.Text
}

func (d *desktop) recordPrompt(doc *document, force bool) uint64 {
	prompt, response := d.currentTexts(doc)
	if doc.currentConversation == 0 {
		doc.currentConversation = 1
	}
	if count := len(doc.promptSteps); count > 0 {
		last := &doc.promptSteps[count-1]
		if !force && last.Prompt == prompt && last.Conversation == doc.currentConversation {
			if !last.Sent && !last.Processed {
				last.Response = response
			}
			return last.ID
		}
	}
	doc.nextPrompt++
	doc.promptSteps = append(doc.promptSteps, promptStep{ID: doc.nextPrompt,
		Conversation: doc.currentConversation, Prompt: prompt, Response: response, Result: doc.processResult})
	doc.currentPrompt, doc.currentResponse = prompt, response
	doc.promptCursor = len(doc.promptSteps) - 1
	d.syncPromptNavigation(doc)
	return doc.nextPrompt
}

func (d *desktop) recordTranscriptionPrompt(doc *document) uint64 {
	force := false
	if count := len(doc.promptSteps); count > 0 {
		last := doc.promptSteps[count-1]
		if last.Transcription != nil {
			force = !sameAudio(last.Transcription.Source, doc.lastAudio) ||
				last.Transcription.RunID != doc.transcriptionRun
		}
	}
	id := d.recordPrompt(doc, force)
	step := &doc.promptSteps[len(doc.promptSteps)-1]
	step.Transcription = &transcriptionPrompt{
		Source: doc.lastAudio, RunID: doc.transcriptionRun,
		Screenshots: slices.Clone(doc.screenshotFiles),
	}
	if doc.lastResult != nil && sameAudio(doc.lastResult.Source, doc.lastAudio) {
		step.Transcription.ModelID, step.Transcription.JobID = doc.lastResult.ModelID, doc.lastResult.JobID
	}
	return id
}

func (d *desktop) prepareProcessingPrompt(doc *document, continuation bool) uint64 {
	var id uint64
	sentInConversation := slices.ContainsFunc(doc.promptSteps, func(step promptStep) bool {
		return step.Conversation == doc.currentConversation && step.Sent
	})
	if !continuation && (len(doc.processHistory) > 0 || sentInConversation) {
		doc.currentConversation++
		id = d.recordPrompt(doc, true)
	} else if count := len(doc.promptSteps); count > 0 &&
		(doc.promptSteps[count-1].Sent || doc.promptSteps[count-1].Processed) {
		id = d.recordPrompt(doc, true)
	} else {
		d.syncPromptDraft(doc)
		id = d.recordPrompt(doc, false)
	}
	doc.promptSteps[len(doc.promptSteps)-1].Sent = true
	return id
}

func (d *desktop) finishProcessingPrompt(doc *document, id uint64, result processing.Result) {
	index := slices.IndexFunc(doc.promptSteps, func(step promptStep) bool { return step.ID == id })
	if index < 0 {
		return
	}
	step := &doc.promptSteps[index]
	step.Response, step.Result, step.Sent, step.Processed = result.Text, &result, true, true
	doc.currentResponse = result.Text
	d.syncPromptNavigation(doc)
}

func (d *desktop) syncPromptNavigation(doc *document) {
	if doc.promptBack == nil || doc.processEditor == nil || doc.processFollowButton == nil {
		return
	}
	visible := len(doc.promptSteps) > 1
	for _, object := range []fyne.CanvasObject{doc.promptBack, doc.promptForward, doc.promptLabel} {
		if visible {
			object.Show()
		} else {
			object.Hide()
		}
	}
	setEnabled(doc.promptBack, doc.promptCursor > 0)
	setEnabled(doc.promptForward, doc.promptCursor < len(doc.promptSteps)-1)
	if len(doc.promptSteps) > 0 && doc.promptCursor >= 0 && doc.promptCursor < len(doc.promptSteps) {
		step := doc.promptSteps[doc.promptCursor]
		setLabel(doc.promptLabel, fmt.Sprintf("Conversa %d · %d de %d", step.Conversation, doc.promptCursor+1, len(doc.promptSteps)), mutedColor)
		doc.promptLabel.hint = &toolHint{layer: d.hints}
		if step.Options != nil {
			doc.promptLabel.hint.text = uiText("Enviado com ") + step.Options.ModelID + " · " + step.Options.Mode
			if step.Options.InstructionName != "" {
				doc.promptLabel.hint.text += " · " + step.Options.InstructionName
			}
			if step.Options.ReasoningEffort != "" {
				doc.promptLabel.hint.text += uiText(" · raciocínio ") + step.Options.ReasoningEffort
			}
			for _, options := range step.PreviousOptions {
				doc.promptLabel.hint.text += uiText("\nVersão anterior: ") + options.ModelID + " · " + options.Mode
			}
		} else {
			doc.promptLabel.hint.text = "Histórico anterior à preservação das opções de envio."
		}
	}
	readonly := doc.browsingPrompts()
	setEnabled(doc.editor, !readonly || doc.editingHistoricalPrompt())
	setEnabled(doc.processEditor, !readonly)
	if readonly {
		doc.promptEdit.Show()
	} else {
		doc.promptEdit.Hide()
	}
	if readonly {
		if doc.editingHistoricalPrompt() {
			doc.processFollowButton.Show()
			setEnabled(doc.processFollowButton, !doc.processBusy && stringsTrimmedPrompt(doc) != "")
			doc.processFollowButton.setHint(uiText("Reprocessar este turno · usar as opções atuais da aba"))
		} else {
			doc.processFollowButton.Hide()
		}
		if doc.processBusy {
			doc.processButton.Show()
		} else {
			doc.processButton.Hide()
		}
		doc.clearButton.Disable()
		doc.undoButton.Disable()
		doc.retryButton.Disable()
		doc.retranscribeButton.Disable()
	} else {
		doc.processButton.Show()
		if doc.processOptions.Mode == processing.ModeMerge {
			doc.processFollowButton.Hide()
		} else {
			doc.processFollowButton.Show()
		}
	}
}

func (d *desktop) applyPromptTexts(doc *document, prompt, response string) {
	doc.historyApplying = true
	defer func() { doc.historyApplying = false }()
	doc.editor.SetText(prompt)
	doc.processEditor.SetText(response)
	d.updateTranscriptLinks(doc, !doc.editingHistoricalPrompt())
	d.updateResultLinks(doc, true)
}

func (d *desktop) navigatePrompt(doc *document, direction int) {
	if doc.closed || len(doc.promptSteps) == 0 {
		return
	}
	if !doc.browsingPrompts() {
		doc.currentPrompt, doc.currentResponse = doc.editor.Text, doc.processEditor.Text
		d.syncPromptDraft(doc)
	}
	next := max(0, min(len(doc.promptSteps)-1, doc.promptCursor+direction))
	if next == doc.promptCursor {
		return
	}
	doc.promptCursor = next
	doc.promptEditingID = 0
	step := doc.promptSteps[next]
	if next == len(doc.promptSteps)-1 {
		d.applyPromptTexts(doc, doc.currentPrompt, doc.currentResponse)
	} else {
		d.applyPromptTexts(doc, step.Prompt, step.Response)
	}
	d.syncDocumentActions(doc)
}

// Edits share one unsent draft. A sent attempt keeps the exact prompt even
// when it fails or is cancelled, and later edits belong to a new draft.
func (d *desktop) syncPromptDraft(doc *document) {
	if len(doc.promptSteps) == 0 {
		return
	}
	prompt, response := d.currentTexts(doc)
	last := &doc.promptSteps[len(doc.promptSteps)-1]
	if !last.Sent && !last.Processed {
		last.Prompt, last.Response = prompt, response
		return
	}
	if last.Prompt == prompt && last.Response == response {
		return
	}
	cursor, browsing := doc.promptCursor, doc.browsingPrompts()
	d.recordPrompt(doc, true)
	if browsing {
		doc.promptCursor = cursor
	}
}

// Core callbacks operate on the current draft, then restore the consulted
// page. Browsing never changes generations or cancels an in-flight request.
func (d *desktop) enterCurrentPrompt(doc *document) func() {
	if !doc.browsingPrompts() {
		return func() {}
	}
	cursor := doc.promptCursor
	doc.promptCursor = len(doc.promptSteps) - 1
	d.applyPromptTexts(doc, doc.currentPrompt, doc.currentResponse)
	return func() {
		doc.currentPrompt, doc.currentResponse = doc.editor.Text, doc.processEditor.Text
		if cursor < len(doc.promptSteps)-1 {
			doc.promptCursor = cursor
			step := doc.promptSteps[cursor]
			prompt := step.Prompt
			if doc.editingHistoricalPrompt() && step.Draft != nil {
				prompt = *step.Draft
			}
			d.applyPromptTexts(doc, prompt, step.Response)
		}
		d.syncDocumentActions(doc)
	}
}

func (d *desktop) returnToCurrentPrompt(doc *document) {
	if !doc.browsingPrompts() {
		return
	}
	doc.promptCursor = len(doc.promptSteps) - 1
	doc.promptEditingID = 0
	d.applyPromptTexts(doc, doc.currentPrompt, doc.currentResponse)
	d.syncDocumentActions(doc)
}

func validPromptSteps(steps []promptStep) bool {
	var prior uint64
	for _, step := range steps {
		if step.ID <= prior || step.Conversation == 0 || !utf8.ValidString(step.Prompt) || !utf8.ValidString(step.Response) ||
			step.Draft != nil && !utf8.ValidString(*step.Draft) ||
			step.Options != nil && step.Options.Validate() != nil ||
			step.AttemptOptions != nil && step.AttemptOptions.Validate() != nil ||
			step.Transcription != nil && (step.Transcription.Source == "" ||
				!validTranscriptionRun(step.Transcription.RunID)) {
			return false
		}
		prior = step.ID
	}
	return true
}

func (d *desktop) restorePromptHistory(doc *document, steps []promptStep) {
	doc.promptEditingID, doc.nextPrompt, doc.currentConversation = 0, 0, 0
	doc.promptSteps = slices.Clone(steps)
	if len(steps) == 0 {
		for _, turn := range doc.processHistory {
			doc.nextPrompt++
			doc.promptSteps = append(doc.promptSteps, promptStep{ID: doc.nextPrompt, Conversation: 1,
				Prompt: turn.Prompt, Response: turn.Response, Sent: true, Processed: true})
		}
	}
	for index := range doc.promptSteps {
		if doc.promptSteps[index].Processed {
			doc.promptSteps[index].Sent = true
		}
	}
	doc.currentPrompt, doc.currentResponse = doc.editor.Text, doc.processEditor.Text
	doc.promptCursor = len(doc.promptSteps) - 1
	if len(doc.promptSteps) > 0 {
		last := doc.promptSteps[len(doc.promptSteps)-1]
		doc.nextPrompt, doc.currentConversation = last.ID, last.Conversation
		d.syncPromptDraft(doc)
	}
	doc.currentPrompt, doc.currentResponse = doc.editor.Text, doc.processEditor.Text
	doc.promptCursor = len(doc.promptSteps) - 1
	d.syncPromptNavigation(doc)
}
