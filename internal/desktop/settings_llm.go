package desktop

import (
	"errors"
	"slices"
	"strings"

	"ai-talker/internal/apiconfig"
	"ai-talker/internal/processing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

func (d *desktop) buildLLMSettings(main *settingsForm) fyne.CanvasObject {
	doc := main.document
	options := doc.processOptions.WithLegacyMerge(d.settings.Merge)
	ids := make([]string, 0, len(processing.Models()))
	for _, model := range processing.Models() {
		ids = append(ids, model.ID)
	}
	form := &processingForm{
		document: doc, model: widget.NewSelectEntry(ids), prompt: main.teamsPrompt,
		errorLabel: main.errorLabel, profile: widget.NewEntry(),
		provider: widget.NewSelect([]string{"AWS Bedrock", "API"}, nil),
	}
	main.llm = form
	setUISelected(form.provider, "AWS Bedrock")
	if options.Provider == "api" {
		setUISelected(form.provider, "API")
	}
	form.model.SetText(options.ModelID)
	form.reasoning = reasoningSelect(options.ModelID, options.ReasoningEffort)
	form.outputTokens = outputTokenSelect(options.OutputTokenParameter)
	main.mergeOutputTokens = outputTokenSelect(options.MergeOutputTokenParameter)
	main.mergeModel.SetText(options.MergeModelID)
	setUISelected(main.mergeReasoning, reasoningChoice(options.MergeReasoning))
	form.api = d.newAPIForm(options.API, form.model, func() bool { return d.settingsForm == main }, false)
	for _, profile := range d.settings.TextProfiles {
		if profile.ID == options.ProfileID {
			form.profile.SetText(profile.Name)
		}
	}
	form.profile.SetPlaceHolder(uiText("Nome do perfil"))
	form.save = main.save
	form.saveProfile = widget.NewButton(uiText("Salvar como perfil"), d.guard(func() {
		d.saveLLMProfile(main)
	}))
	outputHint := widget.NewLabel("No Azure, o nome do deployment não identifica o modelo. Se o serviço exigir um parâmetro específico, escolha-o aqui.")
	outputHint.Wrapping = fyne.TextWrapWord
	apiActions := container.NewVBox(form.api.catalog, d.smallText(form.api.status))
	outputFields := container.NewVBox(
		settingLabel(uiText("Parâmetro do limite de saída · API")),
		container.NewGridWithColumns(2,
			container.NewVBox(settingLabel(uiText("Processar")), form.outputTokens),
			container.NewVBox(settingLabel(uiText("Mesclar")), main.mergeOutputTokens)),
		d.smallText(outputHint),
	)
	syncBackend := func() {
		if uiSelected(form.provider) == "API" {
			form.api.content.Show()
			apiActions.Show()
			outputFields.Show()
			form.reasoning.Enable()
			main.mergeReasoning.Enable()
			form.model.SetOptions(nil)
			main.mergeModel.SetOptions(nil)
		} else {
			form.api.content.Hide()
			apiActions.Hide()
			outputFields.Hide()
			form.model.SetOptions(ids)
			main.mergeModel.SetOptions(ids)
			syncReasoningSelect(form.reasoning, strings.TrimSpace(form.model.Text))
			syncReasoningSelect(main.mergeReasoning, strings.TrimSpace(main.mergeModel.Text))
		}
	}
	form.provider.OnChanged = func(string) { syncBackend() }
	form.model.OnChanged = func(model string) {
		if uiSelected(form.provider) != "API" {
			syncReasoningSelect(form.reasoning, strings.TrimSpace(model))
		}
	}
	main.mergeModel.OnChanged = func(model string) {
		if uiSelected(form.provider) != "API" {
			syncReasoningSelect(main.mergeReasoning, strings.TrimSpace(model))
		}
	}
	syncBackend()
	hint := widget.NewLabel("Aplica à aba atual e às novas abas. As outras abas conservam suas opções. Processar e Mesclar usam o mesmo provider, com modelos e raciocínios independentes.")
	hint.Wrapping = fyne.TextWrapWord
	mergeHint := widget.NewLabel("")
	mergeHint.Wrapping = fyne.TextWrapWord
	if options.MergeNeedsReview {
		mergeHint.SetText("O modelo de mesclagem anterior era do Bedrock. Escolha um modelo deste provider antes de Mesclar.")
	}
	return container.NewVBox(
		d.smallText(hint), settingLabel(uiText("LLM para Processar e Mesclar")), form.provider, form.api.content,
		llmModelReasoningRow("Processar · modelo (ID ou ARN no Bedrock)", form.model,
			"Processar · raciocínio", form.reasoning),
		llmModelReasoningRow("Mesclar · modelo (ID ou ARN no Bedrock)", main.mergeModel,
			"Mesclar · raciocínio", main.mergeReasoning),
		apiActions,
		outputFields,
		d.smallText(mergeHint),
		settingLabel(uiText("Perfil das opções da aba")), form.profile, form.saveProfile,
	)
}

func outputTokenSelect(parameter string) *widget.Select {
	control := widget.NewSelect([]string{
		automaticProfile, processing.OutputMaxCompletionTokens, processing.OutputMaxTokens,
	}, nil)
	if parameter == "" {
		setUISelected(control, automaticProfile)
	} else {
		setUISelected(control, parameter)
	}
	return control
}

func selectedOutputTokenParameter(control *widget.Select) string {
	if uiSelected(control) == automaticProfile {
		return ""
	}
	return uiSelected(control)
}

func llmModelReasoningRow(
	modelLabel string,
	model fyne.CanvasObject,
	reasoningLabel string,
	reasoning fyne.CanvasObject,
) fyne.CanvasObject {
	modelField := container.NewVBox(settingLabel(modelLabel), model)
	reasoningField := container.NewVBox(settingLabel(reasoningLabel), reasoning)
	return container.NewBorder(nil, nil, nil, reasoningField, modelField)
}

func (d *desktop) llmOptionsFromForm(main *settingsForm) (processing.Options, error) {
	options := main.document.processOptions.WithLegacyMerge(d.settings.Merge)
	form := main.llm
	if form == nil {
		return options, nil
	}
	if form.api.busy || strings.TrimSpace(form.api.key.Text) != "" {
		return options, errors.New("conclua o salvamento da chave antes de salvar as opções de LLM")
	}
	previousProvider, previousMerge := options.Provider, options.MergeModelID
	options.Provider, options.API = "", apiconfig.Connection{}
	if uiSelected(form.provider) == "API" {
		options.Provider, options.API = "api", form.api.connection()
	}
	options.ModelID, options.ReasoningEffort = strings.TrimSpace(form.model.Text), selectedReasoning(uiSelected(form.reasoning))
	options.MergeModelID, options.MergeReasoning = strings.TrimSpace(main.mergeModel.Text), selectedReasoning(uiSelected(main.mergeReasoning))
	options.OutputTokenParameter = selectedOutputTokenParameter(form.outputTokens)
	options.MergeOutputTokenParameter = selectedOutputTokenParameter(main.mergeOutputTokens)
	options.TeamsPrompt, options.MergePrompt = main.teamsPrompt.Text, main.mergePrompt.Text
	if options.Provider != previousProvider {
		options.MergeNeedsReview = options.Provider == "api" && options.MergeModelID == previousMerge
	} else if options.MergeModelID != previousMerge || options.Provider != "api" {
		options.MergeNeedsReview = false
	}
	if replacement, ok := main.instructions.replacements[options.Mode]; ok {
		options = instructionModeOptions(options, replacement, main.instructions.values)
	}
	if processing.CustomMode(options.Mode) {
		var err error
		options, err = options.ResolveInstruction(main.instructions.values)
		if err != nil {
			return options, err
		}
	}
	if err := options.Validate(); err != nil {
		return options, err
	}
	if !options.MergeNeedsReview {
		if _, err := options.ForMerge(d.settings.Merge); err != nil {
			return options, err
		}
	}
	return options, nil
}

func (d *desktop) saveLLMProfile(main *settingsForm) {
	options, err := d.llmOptionsFromForm(main)
	if err != nil {
		main.errorLabel.SetText(err.Error())
		main.errorLabel.Show()
		return
	}
	name := strings.TrimSpace(main.llm.profile.Text)
	id := options.ProfileID
	if id == "" {
		id = "perfil-" + d.deps.now().Format("20060102-150405.000000000")
	}
	for _, profile := range d.settings.TextProfiles {
		if strings.EqualFold(profile.Name, name) {
			id = profile.ID
			break
		}
	}
	options.ProfileID = id
	updated := cloneSettings(d.settings)
	updated.Instructions = slices.Clone(main.instructions.values)
	profile := processing.Profile{ID: id, Name: name, Options: options}
	index := slices.IndexFunc(updated.TextProfiles, func(p processing.Profile) bool { return p.ID == id })
	if index >= 0 {
		updated.TextProfiles[index] = profile
	} else {
		updated.TextProfiles = append(updated.TextProfiles, profile)
	}
	updated.Processing = options
	if options.Provider == "api" {
		if err := ensureAPIConnection(&updated, options.API, main.llm.api.name.Text, true, false); err != nil {
			main.errorLabel.SetText(err.Error())
			main.errorLabel.Show()
			return
		}
	}
	if err := updated.Validate(); err != nil {
		main.errorLabel.SetText(err.Error())
		main.errorLabel.Show()
		return
	}
	main.llm.saveProfile.Disable()
	main.save.Disable()
	main.saveSerial++
	serial := main.saveSerial
	preferenceEpoch := d.preferenceEpoch.Load() + 1
	d.persistPreferences(updated, func(err error) {
		if d.settingsForm != main || serial != main.saveSerial {
			return
		}
		main.llm.saveProfile.Enable()
		main.save.Enable()
		if preferenceEpoch != d.preferenceEpoch.Load() {
			main.errorLabel.SetText("As opções mudaram durante o salvamento. Revise e salve novamente.")
			main.errorLabel.Show()
			return
		}
		if err != nil {
			main.errorLabel.SetText(err.Error())
			main.errorLabel.Show()
			return
		}
		main.document.processOptions = options
		d.applyInstructionReplacements(main)
		for _, doc := range d.documents {
			d.syncProcessingOptions(doc)
		}
		if err := d.saveWorkspace(); err != nil {
			main.errorLabel.SetText("Perfil salvo; não foi possível salvar as opções da aba: " + err.Error())
			main.errorLabel.Show()
			return
		}
		main.dialog.Hide()
	})
}

func (d *desktop) openSettingsSection(name string) {
	d.openSettings()
	if d.settingsForm == nil {
		return
	}
	for _, item := range d.settingsForm.sections.Items {
		if item.Text == name {
			d.settingsForm.sections.Select(item)
			return
		}
	}
}

// Applied only by an explicit save; an existing tab keeps its own provider,
// models and reasoning when an instruction is renamed or removed.
func (d *desktop) applyInstructionReplacements(main *settingsForm) {
	for _, doc := range d.documents {
		if mode, ok := main.instructions.replacements[doc.processOptions.Mode]; ok {
			doc.processOptions = instructionModeOptions(doc.processOptions, mode, main.instructions.values)
		}
	}
}

func instructionModeOptions(options processing.Options, mode string, instructions []processing.Instruction) processing.Options {
	options.Mode, options.InstructionName, options.InstructionPrompt = mode, "", ""
	if processing.CustomMode(mode) {
		resolved, err := options.ResolveInstruction(instructions)
		if err == nil {
			options = resolved
		}
	}
	return options
}
