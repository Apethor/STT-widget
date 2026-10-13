package desktop

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"ai-talker/internal/processing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const (
	teamsModeChoice   = "Teams"
	freeModeChoice    = "Livre"
	mergeModeChoice   = "Mesclar"
	customModelChoice = "Outro modelo…"
)

type processingForm struct {
	document     *document
	dialog       *dialog.CustomDialog
	model        *widget.SelectEntry
	prompt       *widget.Entry
	save         *widget.Button
	errorLabel   *widget.Label
	reasoning    *widget.Select
	outputTokens *widget.Select
	provider     *widget.Select
	api          *apiForm
	profile      *widget.Entry
	saveProfile  *widget.Button
}

func (d *desktop) buildProcessingPanel(doc *document) fyne.CanvasObject {
	title := label("Processar texto", 16, textColor)
	title.TextStyle.Bold = true
	doc.processCollapse = d.iconButton("Recolher processamento", theme.NavigateBackIcon(), func() {
		d.toggleProcessingPanel(doc)
	})
	doc.processMode = widget.NewSelect(modeChoices(d.settings.Instructions), func(choice string) {
		d.guard(func() {
			if doc.updatingOptions || doc.closed {
				return
			}
			options := doc.processOptions
			mode := modeID(choice, d.settings.Instructions)
			if mode == "" {
				return
			}
			options = instructionModeOptions(options, mode, d.settings.Instructions)
			d.changeProcessingOptions(doc, options)
		})()
	})
	doc.processMode.PlaceHolder = "Modo"
	names := make([]string, 0, len(processing.Models())+1)
	for _, model := range processing.Models() {
		names = append(names, model.Name)
	}
	names = append(names, customModelChoice)
	doc.processModel = widget.NewSelect(names, func(choice string) {
		d.guard(func() {
			if doc.updatingOptions || doc.closed {
				return
			}
			if choice == customModelChoice {
				d.openProcessingSettings(doc)
				d.syncProcessingOptions(doc)
				return
			}
			for _, profile := range d.settings.TextProfiles {
				if choice == "Perfil · "+profile.Name {
					d.changeProcessingOptions(doc, profile.Options)
					return
				}
			}
			for _, preset := range processing.APIPresets() {
				if choice == preset.Name && doc.processOptions.Provider == "api" && doc.processOptions.API == preset.Connection {
					options := doc.processOptions
					options.ProfileID = ""
					if options.Mode == processing.ModeMerge {
						options.MergeModelID, options.MergeReasoning, options.MergeNeedsReview = preset.ModelID, "", false
					} else {
						options.ModelID, options.ReasoningEffort = preset.ModelID, ""
					}
					d.changeProcessingOptions(doc, options)
					return
				}
			}
			for _, model := range processing.Models() {
				if model.Name == choice && doc.processOptions.Provider != "api" {
					options := doc.processOptions
					options.ProfileID = ""
					if options.Mode == processing.ModeMerge {
						options.MergeModelID, options.MergeNeedsReview = model.ID, false
						if !processing.SupportsReasoning(model.ID) {
							options.MergeReasoning = ""
						}
					} else {
						options.ModelID = model.ID
						if !processing.SupportsReasoning(model.ID) {
							options.ReasoningEffort = ""
						}
					}
					d.changeProcessingOptions(doc, options)
					return
				}
			}
		})()
	})
	doc.processModel.PlaceHolder = "Modelo"
	doc.processPrompt = d.iconButton("Configurações de LLM e instruções", theme.SettingsIcon(), func() {
		d.openProcessingSettings(doc)
	})
	doc.processZoomButton = d.editorZoomButton(doc, false)
	doc.processSearch = d.iconButton("Busca web ativada", webSearchIcon, func() {
		options := doc.processOptions
		options.WebSearchDisabled = !options.WebSearchDisabled
		d.changeProcessingOptions(doc, options)
	})
	doc.processReasoning = widget.NewSelect([]string{"Auto", "Baixo", "Médio", "Alto"}, func(choice string) {
		d.guard(func() {
			if doc.updatingOptions || doc.closed {
				return
			}
			effort := map[string]string{"Baixo": "low", "Médio": "medium", "Alto": "high"}[choice]
			options := doc.processOptions
			if options.Mode == processing.ModeMerge {
				options.MergeReasoning = effort
			} else {
				options.ReasoningEffort = effort
			}
			d.changeProcessingOptions(doc, options)
		})()
	})
	doc.processEditor = widget.NewMultiLineEntry()
	doc.processEditor.Wrapping = fyne.TextWrapWord
	doc.processEditor.SetMinRowsVisible(3)
	doc.processEditor.SetPlaceHolder(uiText("O resultado aparece aqui quando você clicar em Processar."))
	doc.processEditor.OnChanged = func(text string) {
		d.guard(func() {
			if doc.historyApplying {
				return
			}
			doc.currentResponse = text
			if doc.processBusy {
				d.invalidateProcessing(doc)
				doc.processStatus.SetText("Resultado editado. Clique em Processar para gerar outra resposta.")
				d.syncControls()
			}
			if !doc.browsingPrompts() && strings.TrimSpace(text) != "" && len(doc.processHistory) > 0 {
				// Keep the user's latest answer for the next continuation, even
				// when Novo áudio subsequently empties the visible result editor.
				doc.processHistory[len(doc.processHistory)-1].Response = text
			}
			d.syncProcessingActions(doc)
			d.updateResultLinks(doc, doc.processReading)
		})()
	}
	doc.processError = widget.NewLabel("")
	doc.processError.Importance = widget.DangerImportance
	doc.processError.Wrapping = fyne.TextWrapWord
	doc.processError.Hide()
	doc.processStatus = widget.NewLabel("Opcional · usa o texto desta aba.")
	doc.processStatus.Wrapping = fyne.TextWrapWord
	doc.processButton = d.iconButton("Processar", processTextIcon, func() { d.processDocument(doc) })
	doc.processButton.Importance = widget.HighImportance
	doc.processFollowButton = d.iconButton("Reprocessar · continuar a conversa desta aba", theme.ViewRefreshIcon(), func() {
		d.continueProcessing(doc)
	})
	doc.processCopyButton = d.iconButton("Copiar resultado", theme.ContentCopyIcon(), func() { d.copyDocumentText(doc, true) })
	doc.processSaveButton = d.iconButton("Salvar resultado", theme.DocumentSaveIcon(), func() { d.saveDocumentText(doc, true) })
	doc.processViewButton = d.iconButton("Ler resultado com links clicáveis", theme.VisibilityIcon(), func() {
		d.updateResultLinks(doc, !doc.processReading)
		if !doc.processReading {
			d.window.Canvas().Focus(doc.processEditor)
		}
	})
	doc.processViewButton.Disable()
	for _, button := range []*actionButton{
		doc.processCollapse, doc.processPrompt, doc.processZoomButton, doc.processSearch,
		doc.processButton, doc.processFollowButton, doc.processCopyButton, doc.processSaveButton,
		doc.processViewButton,
	} {
		button.compact = true
	}
	options := container.NewThemeOverride(
		container.NewBorder(nil, nil, compactSelect(doc.processMode, 74), doc.processReasoning, compactSelect(doc.processModel, 60)),
		headerTheme{Theme: d.palette},
	)
	header := container.NewVBox(
		container.NewBorder(nil, nil, nil, container.NewHBox(doc.processSearch, doc.processPrompt,
			doc.processZoomButton, doc.processCollapse), title),
		options,
	)
	footer := container.NewVBox(
		d.smallText(doc.processError),
		d.smallText(doc.processStatus),
		container.NewBorder(
			nil, nil, container.NewHBox(doc.processButton, doc.processFollowButton),
			container.NewHBox(doc.processViewButton, doc.processCopyButton, doc.processSaveButton),
			layout.NewSpacer(),
		),
	)
	d.syncProcessingOptions(doc)
	doc.processPanel = newCollapsiblePanel(d, doc, panel(container.NewBorder(
		header, footer, nil, nil,
		d.resultPresentation(doc),
	)))
	return doc.processPanel
}

func (d *desktop) syncProcessingOptions(doc *document) {
	doc.updatingOptions = true
	defer func() { doc.updatingOptions = false }()
	mode := teamsModeChoice
	if doc.processOptions.Mode == processing.ModeFree {
		mode = freeModeChoice
	} else if doc.processOptions.Mode == processing.ModeMerge {
		mode = mergeModeChoice
	} else if processing.CustomMode(doc.processOptions.Mode) {
		mode = doc.processOptions.InstructionName
		for _, instruction := range d.settings.Instructions {
			if instruction.ID == doc.processOptions.Mode {
				mode = instruction.Name
			}
		}
	}
	customNames := make([]string, 0, len(d.settings.Instructions))
	for _, instruction := range d.settings.Instructions {
		customNames = append(customNames, instruction.Name)
	}
	preserveUIChoices(doc.processMode, customNames)
	doc.processMode.SetOptions(modeChoices(d.settings.Instructions))
	setUISelected(doc.processMode, mode)
	name := customModelChoice
	var names []string
	modelID, effort := doc.processOptions.ModelID, doc.processOptions.ReasoningEffort
	if doc.processOptions.Mode == processing.ModeMerge {
		modelID, effort = doc.processOptions.MergeModelID, doc.processOptions.MergeReasoning
	}
	if doc.processOptions.Provider != "api" {
		for _, model := range processing.Models() {
			names = append(names, model.Name)
			if model.ID == modelID {
				name = model.Name
			}
		}
	}
	if doc.processOptions.Provider == "api" {
		name = "API · " + modelID
		names = append(names, name)
	}
	for _, preset := range processing.APIPresets() {
		if doc.processOptions.Provider != "api" || doc.processOptions.API != preset.Connection {
			continue
		}
		names = append(names, preset.Name)
		if modelID == preset.ModelID {
			name = preset.Name
		}
	}
	for _, profile := range d.settings.TextProfiles {
		choice := "Perfil · " + profile.Name
		names = append(names, choice)
		if profile.ID == doc.processOptions.ProfileID && profile.Options == doc.processOptions {
			name = choice
		}
	}
	names = append(names, customModelChoice)
	doc.processModel.SetOptions(names)
	setUISelected(doc.processModel, name)
	reasoning := map[string]string{"low": "Baixo", "medium": "Médio", "high": "Alto"}[effort]
	if reasoning == "" {
		reasoning = "Auto"
	}
	setUISelected(doc.processReasoning, reasoning)
	setEnabled(doc.processReasoning, doc.processOptions.Provider == "api" || processing.SupportsReasoning(modelID))
	doc.processSearch.Importance = widget.HighImportance
	doc.processSearch.setHint(uiText("Busca web ativada · clicar para desativar"))
	if doc.processOptions.WebSearchDisabled {
		doc.processSearch.Importance = widget.LowImportance
		doc.processSearch.setHint(uiText("Busca web desativada · clicar para ativar"))
	}
	doc.processSearch.Refresh()
	if doc.processOptions.Mode == processing.ModeMerge {
		doc.processSearch.Hide()
	} else {
		doc.processSearch.Show()
	}
	d.syncProcessingActions(doc)
}

func (d *desktop) syncProcessingActions(doc *document) {
	hasOutput := strings.TrimSpace(doc.processEditor.Text) != ""
	setEnabled(doc.processCopyButton, hasOutput)
	setEnabled(doc.processSaveButton, hasOutput)
	setEnabled(doc.processButton, doc.processBusy || strings.TrimSpace(doc.editor.Text) != "")
	if doc.processFollowButton != nil {
		setEnabled(doc.processFollowButton, !doc.processBusy && len(doc.processHistory) > 0 && strings.TrimSpace(doc.editor.Text) != "")
		doc.processFollowButton.setHint(fmt.Sprintf("Reprocessar · continuar a conversa desta aba (%d respostas)", len(doc.processHistory)))
	}
	if doc.processOptions.Mode == processing.ModeMerge && !doc.processBusy && !doc.browsingPrompts() {
		doc.processFollowButton.Hide()
		setEnabled(doc.processButton, d.mergeBusy || d.canCombine())
	} else if !doc.browsingPrompts() {
		doc.processFollowButton.Show()
	}
	doc.processButton.setHint(uiText("Processar"))
	doc.processButton.SetIcon(processTextIcon)
	doc.processButton.Importance = widget.HighImportance
	if doc.processOptions.Mode == processing.ModeMerge {
		doc.processButton.setHint(uiText("Mesclar abas do grupo"))
		doc.processButton.SetIcon(mergeIcon)
		if d.mergeBusy && d.mergeGroup == doc.group {
			doc.processButton.setHint(uiText("Cancelar mesclagem"))
			doc.processButton.SetIcon(theme.ContentClearIcon())
		}
	}
	if doc.processBusy {
		doc.processButton.setHint(uiText("Cancelar processamento"))
		doc.processButton.SetIcon(theme.ContentClearIcon())
		doc.processButton.Importance = widget.MediumImportance
	}
	doc.processButton.Refresh()
	if doc.browsingPrompts() {
		if doc.processBusy {
			doc.processButton.Show()
		} else {
			doc.processButton.Hide()
		}
		if doc.editingHistoricalPrompt() {
			doc.processFollowButton.Show()
			setEnabled(doc.processFollowButton, !doc.processBusy && stringsTrimmedPrompt(doc) != "")
			doc.processFollowButton.setHint(uiText("Reprocessar este turno · usar as opções atuais da aba"))
		} else {
			doc.processFollowButton.Hide()
		}
	}
}

func (d *desktop) invalidateProcessing(doc *document) {
	if doc.processCancel != nil {
		doc.processCancel()
		doc.processCancel = nil
	}
	doc.processGeneration++
	doc.processBusy = false
	if doc.processEditor != nil {
		d.syncProcessingActions(doc)
	}
}

func (d *desktop) processDocument(doc *document) {
	if doc.processOptions.Mode == processing.ModeMerge && !doc.processBusy && !doc.browsingPrompts() {
		d.mergeTranscripts()
		return
	}
	d.startProcessing(doc, false)
}

func (d *desktop) continueProcessing(doc *document) {
	if doc.closed || doc.processBusy {
		return
	}
	if doc.editingHistoricalPrompt() {
		d.startHistoricalProcessing(doc)
		return
	}
	if len(doc.processHistory) == 0 {
		d.showProcessingError(doc, "Clique em Processar para iniciar a conversa desta aba.")
		return
	}
	d.startProcessing(doc, true)
}

func (d *desktop) startProcessing(doc *document, continuation bool) {
	if doc.closed || d.closing {
		return
	}
	if doc.processBusy {
		d.invalidateProcessing(doc)
		doc.processStatus.SetText("Processamento cancelado.")
		d.syncControls()
		return
	}
	if doc.browsingPrompts() {
		return
	}
	options, err := doc.processOptions.ResolveInstruction(d.settings.Instructions)
	if err != nil {
		d.showProcessingError(doc, err.Error())
		return
	}
	request := processing.Request{Text: doc.editor.Text, Options: options, WebSearchLimit: d.settings.WebSearchLimit}
	if continuation {
		request.History = slices.Clone(doc.processHistory)
	}
	history := slices.Clone(request.History)
	var contextReduced bool
	request, contextReduced = request.ContextWindow()
	if err := request.Validate(); err != nil {
		d.showProcessingError(doc, err.Error())
		return
	}
	if d.deps.process == nil {
		d.showProcessingError(doc, "O serviço de processamento não está disponível.")
		return
	}
	if err := d.saveDocumentDrafts(doc); err != nil {
		d.showProcessingError(doc, err.Error())
		return
	}
	d.invalidateProcessing(doc)
	promptID := d.prepareProcessingPrompt(doc, continuation)
	doc.promptSteps[len(doc.promptSteps)-1].Options = &options
	if !continuation {
		// A failed new conversation cannot revive its predecessor's context.
		// Historical prompt steps remain available for consultation.
		doc.processHistory = nil
	}
	doc.processBusy = true
	doc.processSource = request.Text
	epoch := doc.processGeneration
	ctx, cancel := context.WithTimeout(d.ctx, request.Options.RequestTimeout()+30*time.Second)
	doc.processCancel = cancel
	request.OnProgress = func(message string) {
		d.post(func() {
			if !doc.closed && epoch == doc.processGeneration && doc.processBusy && ctx.Err() == nil {
				doc.processStatus.SetText(message)
			}
		})
	}
	settings := cloneSettings(d.settings)
	doc.processError.Hide()
	doc.processStatus.SetText("Processando o texto desta aba…")
	d.syncProcessingActions(doc)
	d.syncControls()
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
			defer d.enterCurrentPrompt(doc)()
			doc.processBusy = false
			doc.processCancel = nil
			if err != nil {
				if errors.Is(err, context.Canceled) {
					doc.processStatus.SetText("Processamento cancelado.")
				} else {
					d.showProcessingError(doc, err.Error())
				}
			} else if strings.TrimSpace(result.Text) == "" {
				d.showProcessingError(doc, "O modelo retornou uma resposta vazia. Tente novamente.")
			} else if saveErr := d.saveDocumentDrafts(doc); saveErr != nil {
				d.showProcessingError(doc, saveErr.Error())
			} else if saveErr := d.writeDraft(
				strings.TrimSpace(doc.processEditor.Text), "", &doc.processDraftText, "Processado",
			); saveErr != nil {
				d.showProcessingError(doc, saveErr.Error())
			} else {
				doc.processResult = &result
				doc.processHistory = append(history, processing.Turn{
					Prompt: request.Text, Response: result.Text,
				})
				doc.processEditor.SetText(result.Text)
				d.finishProcessingPrompt(doc, promptID, result)
				d.updateResultLinks(doc, true)
				doc.processError.Hide()
				doc.processStatus.SetText(fmt.Sprintf("Pronto · conversa desta aba com %d respostas.", len(doc.processHistory)))
				if contextReduced {
					doc.processStatus.SetText("Pronto · contexto enviado reduzido às mensagens recentes; histórico completo preservado.")
				}
			}
			d.syncProcessingActions(doc)
			d.syncControls()
			d.finishWhenReady()
		})
	})
}

func (d *desktop) showProcessingError(doc *document, message string) {
	doc.processError.SetText(shortMessage(message))
	doc.processError.Show()
	doc.processStatus.SetText("Você pode ajustar o texto, trocar o modelo e tentar novamente.")
	d.syncProcessingActions(doc)
}

func (d *desktop) changeProcessingOptions(doc *document, options processing.Options) {
	options = options.WithLegacyMerge(d.settings.Merge)
	if err := options.Validate(); err != nil {
		d.showProcessingError(doc, err.Error())
		d.syncProcessingOptions(doc)
		return
	}
	d.invalidateProcessing(doc)
	doc.processOptions = options
	doc.processError.Hide()
	doc.processStatus.SetText("Opcional · usa o texto desta aba.")
	d.syncProcessingOptions(doc)
	updated := cloneSettings(d.settings)
	updated.Processing = options
	generation, processGeneration := doc.generation, doc.processGeneration
	d.persistPreferences(updated, func(err error) {
		if err != nil && !doc.closed && generation == doc.generation && processGeneration == doc.processGeneration {
			d.showProcessingError(doc, "A seleção vale nesta sessão, mas não foi possível salvar: "+err.Error())
		}
	})
}

func (d *desktop) openProcessingSettings(doc *document) {
	if doc.closed {
		return
	}
	d.selectDocument(doc)
	d.openSettingsSection("LLM")
}
