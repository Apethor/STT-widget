package desktop

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"ai-talker/internal/processing"
)

func (doc *document) editingHistoricalPrompt() bool {
	return doc.browsingPrompts() && doc.promptEditingID != 0 &&
		doc.promptSteps[doc.promptCursor].ID == doc.promptEditingID
}

func stringsTrimmedPrompt(doc *document) string { return strings.TrimSpace(doc.editor.Text) }

func (d *desktop) editHistoricalPrompt(doc *document) {
	if doc.closed || !doc.browsingPrompts() {
		return
	}
	step := &doc.promptSteps[doc.promptCursor]
	doc.promptEditingID = step.ID
	prompt := step.Prompt
	if step.Draft != nil {
		prompt = *step.Draft
	}
	d.applyPromptTexts(doc, prompt, step.Response)
	d.updateTranscriptLinks(doc, false)
	doc.processStatus.SetText("Edite o prompt e clique em Reprocessar. Serão usadas as opções atuais da aba e o contexto anterior a este turno.")
	d.syncDocumentActions(doc)
	d.window.Canvas().Focus(doc.editor)
}

func (d *desktop) changeHistoricalPrompt(doc *document, text string) {
	if !doc.editingHistoricalPrompt() {
		return
	}
	step := &doc.promptSteps[doc.promptCursor]
	step.Draft = &text
	doc.editEpoch++
	if doc.processBusy && doc.processSource != text {
		d.invalidateProcessing(doc)
		doc.processStatus.SetText("Prompt alterado. Reprocesse para usar esta edição.")
	}
	d.syncDocumentActions(doc)
}

// Each completed pair is addressed by its prompt ID and conversation. The
// active context can include an edited answer which was not sent again.
func historicalContext(doc *document, index int) ([]processing.Turn, int) {
	conversation := doc.promptSteps[index].Conversation
	var history []processing.Turn
	for _, step := range doc.promptSteps[:index] {
		if step.Conversation != conversation || !step.Processed {
			continue
		}
		turn := processing.Turn{Prompt: step.Prompt, Response: step.Response}
		if conversation == doc.currentConversation && len(history) < len(doc.processHistory) {
			turn = doc.processHistory[len(history)]
		}
		history = append(history, turn)
	}
	return history, len(history)
}

func (d *desktop) startHistoricalProcessing(doc *document) {
	if doc.closed || doc.processBusy || !doc.editingHistoricalPrompt() || d.closing {
		return
	}
	index := doc.promptCursor
	id, conversation := doc.promptSteps[index].ID, doc.promptSteps[index].Conversation
	options, err := doc.processOptions.ResolveInstruction(d.settings.Instructions)
	if err == nil && options.Mode == processing.ModeMerge {
		options, err = options.ForMerge(d.settings.Merge)
	}
	if err != nil {
		d.showProcessingError(doc, err.Error())
		return
	}
	history, turnIndex := historicalContext(doc, index)
	request := processing.Request{Text: doc.editor.Text, Options: options, History: history, WebSearchLimit: d.settings.WebSearchLimit}
	request, reduced := request.ContextWindow()
	if err := request.Validate(); err != nil {
		d.showProcessingError(doc, err.Error())
		return
	}
	if d.deps.process == nil {
		d.showProcessingError(doc, "O serviço de processamento não está disponível.")
		return
	}
	text := request.Text
	doc.promptSteps[index].Draft, doc.promptSteps[index].AttemptPrompt = &text, text
	doc.promptSteps[index].AttemptOptions = &options
	if err := d.saveWorkspace(); err != nil {
		d.showProcessingError(doc, "Não foi possível preservar a edição: "+err.Error())
		return
	}
	d.invalidateProcessing(doc)
	doc.processBusy, doc.processSource = true, request.Text
	epoch := doc.processGeneration
	ctx, cancel := context.WithTimeout(d.ctx, options.RequestTimeout()+30*time.Second)
	doc.processCancel = cancel
	settings := cloneSettings(d.settings)
	request.OnProgress = func(message string) {
		d.post(func() {
			if !doc.closed && epoch == doc.processGeneration && doc.processBusy && ctx.Err() == nil {
				doc.processStatus.SetText(message)
			}
		})
	}
	doc.processError.Hide()
	doc.processStatus.SetText("Reprocessando o turno escolhido…")
	d.syncDocumentActions(doc)
	d.workers.Go(func() {
		defer cancel()
		var result processing.Result
		err := callOperation(func() error {
			select {
			case d.processSlots <- struct{}{}:
				defer func() { <-d.processSlots }()
			case <-ctx.Done():
				return ctx.Err()
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			var err error
			result, err = d.deps.process(ctx, settings, request)
			return err
		})
		d.post(func() {
			if doc.closed || epoch != doc.processGeneration || !doc.processBusy {
				return
			}
			doc.processBusy, doc.processCancel = false, nil
			if err != nil {
				if errors.Is(err, context.Canceled) {
					doc.processStatus.SetText("Reprocessamento cancelado; a edição e a resposta anterior foram preservadas.")
				} else {
					d.showProcessingError(doc, err.Error())
				}
			} else if strings.TrimSpace(result.Text) == "" {
				d.showProcessingError(doc, "O modelo retornou uma resposta vazia. A resposta anterior foi preservada.")
			} else if err := d.publishHistoricalResult(doc, id, conversation, turnIndex, request, result); err != nil {
				d.showProcessingError(doc, err.Error())
			} else {
				doc.processError.Hide()
				doc.processStatus.SetText("Turno atualizado. Os turnos posteriores e o rascunho atual foram preservados.")
				if reduced {
					doc.processStatus.SetText("Turno atualizado; contexto enviado reduzido. Histórico completo preservado.")
				}
			}
			d.syncDocumentActions(doc)
			d.syncControls()
			d.finishWhenReady()
		})
	})
}

func (d *desktop) publishHistoricalResult(doc *document, id, conversation uint64, turnIndex int, request processing.Request, result processing.Result) error {
	index := slices.IndexFunc(doc.promptSteps, func(step promptStep) bool { return step.ID == id && step.Conversation == conversation })
	if index < 0 {
		return errors.New("o turno não está mais disponível")
	}
	successor, err := detachedWorkspace(d.snapshotWorkspace())
	if err != nil {
		return err
	}
	tabIndex := slices.IndexFunc(successor.Tabs, func(tab documentSnapshot) bool { return tab.ID == doc.id })
	if tabIndex < 0 {
		return errors.New("a aba não está mais disponível")
	}
	tab := &successor.Tabs[tabIndex]
	step := &tab.PromptSteps[index]
	if step.Options != nil {
		step.PreviousOptions = append(step.PreviousOptions, *step.Options)
	}
	wasProcessed := step.Processed
	step.Prompt, step.Response, step.Result, step.Options = request.Text, result.Text, &result, &request.Options
	step.Processed, step.Sent, step.Draft = true, true, nil
	if conversation == doc.currentConversation {
		turn := processing.Turn{Prompt: request.Text, Response: result.Text}
		if wasProcessed && turnIndex < len(tab.Conversation) {
			tab.Conversation[turnIndex] = turn
		} else if !wasProcessed && turnIndex <= len(tab.Conversation) {
			tab.Conversation = slices.Insert(tab.Conversation, turnIndex, turn)
		} else {
			return fmt.Errorf("o contexto deste turno mudou; tente novamente")
		}
	}
	if err := d.writeWorkspace(successor); err != nil {
		return fmt.Errorf("a resposta anterior foi mantida porque não foi possível salvar: %w", err)
	}
	doc.promptSteps, doc.processHistory = tab.PromptSteps, tab.Conversation
	if doc.promptEditingID == id {
		doc.promptEditingID = 0
	}
	if doc.browsingPrompts() && doc.promptSteps[doc.promptCursor].ID == id {
		d.applyPromptTexts(doc, request.Text, result.Text)
	}
	d.syncPromptNavigation(doc)
	return nil
}
