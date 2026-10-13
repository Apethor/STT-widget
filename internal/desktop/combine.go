package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"ai-talker/internal/processing"
	"fyne.io/fyne/v2/theme"
)

type combineSource struct {
	document   *document
	text       string
	generation uint64
}

func (d *desktop) combineSources() []combineSource {
	documents := d.groupDocuments(d.document.group)
	sources := make([]combineSource, 0, len(documents))
	for _, doc := range documents {
		sources = append(sources, combineSource{
			document: doc, text: doc.editor.Text, generation: doc.generation,
		})
	}
	return sources
}

func (d *desktop) sourcesCurrent(sources []combineSource) bool {
	if len(sources) == 0 {
		return false
	}
	documents := d.groupDocuments(sources[0].document.group)
	if len(sources) != len(documents) {
		return false
	}
	for index, source := range sources {
		if source.document != documents[index] || source.document.closed ||
			source.document.generation != source.generation || source.document.editor.Text != source.text {
			return false
		}
	}
	return true
}

func (d *desktop) canCombine() bool {
	if d.document == nil {
		return false
	}
	documents := d.groupDocuments(d.document.group)
	if len(documents) < 2 || d.capture != captureIdle {
		return false
	}
	hasText := false
	for _, doc := range documents {
		if doc.busy {
			return false
		}
		hasText = hasText || strings.TrimSpace(doc.editor.Text) != ""
	}
	return hasText
}

func (d *desktop) syncCombineControls() {
	if d.joinButton == nil {
		return
	}
	setEnabled(d.joinButton, !d.mergeBusy && d.canCombine())
	setEnabled(d.mergeButton, d.mergeBusy || d.canCombine())
	d.joinButton.setHint("Juntar transcrições do " + d.groupName(d.document.group) + " na primeira aba")
	d.mergeButton.SetIcon(mergeIcon)
	d.mergeButton.setHint("Mesclar transcrições do " + d.groupName(d.document.group) + " por LLM na primeira aba")
	if d.mergeBusy {
		d.mergeButton.SetIcon(theme.ContentClearIcon())
		d.mergeButton.setHint("Cancelar mesclagem · " + d.groupName(d.mergeGroup))
	}
	for _, doc := range d.documents {
		if doc.processOptions.Mode == processing.ModeMerge && doc.processButton != nil {
			d.syncProcessingActions(doc)
		}
	}
}

func (d *desktop) joinTranscripts() {
	if d.mergeBusy || !d.canCombine() {
		return
	}
	sources := d.combineSources()
	texts := make([]string, 0, len(sources))
	for _, source := range sources {
		if strings.TrimSpace(source.text) != "" {
			texts = append(texts, source.text)
		}
	}
	if err := d.applyCombinedText(sources, strings.Join(texts, "\n\n")); err != nil {
		d.showError(err.Error(), "")
	}
}

func (d *desktop) mergeTranscripts() {
	if d.mergeBusy {
		d.cancelMerge("Mesclagem cancelada. Os textos foram mantidos.")
		return
	}
	if !d.canCombine() {
		return
	}
	if d.deps.process == nil {
		d.showError("O serviço de processamento não está disponível.", "")
		return
	}
	sources := d.combineSources()
	type input struct {
		Name string `json:"aba"`
		Text string `json:"texto"`
	}
	inputs := make([]input, 0, len(sources))
	for _, source := range sources {
		if strings.TrimSpace(source.text) != "" {
			inputs = append(inputs, input{Name: source.document.item.Text, Text: source.text})
		}
	}
	payload, err := json.Marshal(inputs)
	if err != nil {
		d.showError("Não foi possível preparar os textos: "+err.Error(), "")
		return
	}
	options, err := d.document.processOptions.ForMerge(d.settings.Merge)
	if err != nil {
		d.showProcessingError(d.document, err.Error())
		return
	}
	request := processing.Request{Text: string(payload), Options: options}
	if err := request.Validate(); err != nil {
		d.showError(err.Error(), "")
		return
	}
	d.mergeGeneration++
	epoch := d.mergeGeneration
	ctx, cancel := context.WithTimeout(d.ctx, 150*time.Second)
	d.mergeCancel = cancel
	d.mergeBusy = true
	d.mergeGroup = sources[0].document.group
	settings := cloneSettings(d.settings)
	d.status.SetText("Mesclando as transcrições. Os originais continuam nas abas até terminar.")
	d.syncCombineControls()
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
			if epoch != d.mergeGeneration || !d.mergeBusy {
				return
			}
			d.mergeBusy = false
			d.mergeCancel = nil
			if err != nil {
				if errors.Is(err, context.Canceled) {
					d.status.SetText("Mesclagem cancelada. Os textos foram mantidos.")
				} else {
					d.showError("Não foi possível mesclar. Os textos foram mantidos. "+err.Error(), "")
					d.reveal()
				}
			} else if !d.sourcesCurrent(sources) {
				d.status.SetText("As abas mudaram. Clique em Mesclar para usar os textos atuais.")
			} else if strings.TrimSpace(result.Text) == "" {
				d.showError("A mesclagem retornou texto vazio. Os originais foram mantidos.", "")
				d.reveal()
			} else if err := d.applyCombinedTextWithLabel(sources, result.Text, "Mesclar"); err != nil {
				d.showError(err.Error(), "")
				d.reveal()
			}
			d.syncCombineControls()
			d.finishWhenReady()
		})
	})
}

func (d *desktop) cancelMerge(message string) {
	if !d.mergeBusy {
		return
	}
	if d.mergeCancel != nil {
		d.mergeCancel()
		d.mergeCancel = nil
	}
	d.mergeGeneration++
	d.mergeBusy = false
	d.status.SetText(message)
	d.syncCombineControls()
}
