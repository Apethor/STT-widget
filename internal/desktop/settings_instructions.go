package desktop

import (
	"crypto/rand"
	"encoding/hex"
	"slices"
	"strings"

	"ai-talker/internal/processing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

type instructionForm struct {
	values       []processing.Instruction
	replacements map[string]string
	selectedID   string
	selection    *widget.Select
	name         *widget.Entry
	prompt       *widget.Entry
	replacement  *widget.Select
	status       *widget.Label
	save         *widget.Button
	remove       *widget.Button
	sections     *instructionSectionSelector
	customPage   fyne.CanvasObject
}

type instructionSectionSelector struct {
	selectIndex func(int)
}

func (s *instructionSectionSelector) SelectIndex(index int) {
	if s != nil && s.selectIndex != nil {
		s.selectIndex(index)
	}
}

type instructionSettingsPages struct {
	root, teams, merge, custom fyne.CanvasObject
}

func modeChoices(instructions []processing.Instruction) []string {
	choices := []string{freeModeChoice, mergeModeChoice, teamsModeChoice}
	for _, instruction := range instructions {
		choices = append(choices, instruction.Name)
	}
	return choices
}

func modeID(choice string, instructions []processing.Instruction) string {
	switch choice {
	case freeModeChoice:
		return processing.ModeFree
	case mergeModeChoice:
		return processing.ModeMerge
	case teamsModeChoice:
		return processing.ModeTeams
	}
	for _, instruction := range instructions {
		if choice == instruction.Name {
			return instruction.ID
		}
	}
	return ""
}

func (d *desktop) buildInstructionSettings(main *settingsForm) instructionSettingsPages {
	f := &instructionForm{
		values: slices.Clone(d.settings.Instructions), replacements: make(map[string]string),
		selection: widget.NewSelect(nil, nil), name: widget.NewEntry(),
		prompt: widget.NewMultiLineEntry(), replacement: widget.NewSelect(nil, nil),
		status: widget.NewLabel("Até dez instruções personalizadas. Escolher um modo não altera provider, modelo ou raciocínio."),
	}
	main.instructions = f
	main.teamsPrompt = widget.NewMultiLineEntry()
	main.teamsPrompt.SetText(main.document.processOptions.TeamsPrompt)
	main.teamsPrompt.Wrapping, main.mergePrompt.Wrapping, f.prompt.Wrapping = fyne.TextWrapWord, fyne.TextWrapWord, fyne.TextWrapWord
	main.teamsPrompt.SetMinRowsVisible(5)
	main.mergePrompt.SetMinRowsVisible(5)
	main.mergePrompt.SetText(main.document.processOptions.WithLegacyMerge(d.settings.Merge).MergePrompt)
	f.prompt.SetMinRowsVisible(5)
	f.name.SetPlaceHolder(uiText("Nome que aparece no seletor de modos"))
	f.selection.PlaceHolder = "Escolha uma instrução para editar"
	f.replacement.PlaceHolder = "Escolha outro modo antes de excluir uma instrução em uso"
	f.status.Wrapping = fyne.TextWrapWord
	refresh := func() {
		var names []string
		var replacements []processing.Instruction
		for _, instruction := range f.values {
			names = append(names, instruction.Name)
			if instruction.ID != f.selectedID {
				replacements = append(replacements, instruction)
			}
		}
		f.selection.SetOptions(names)
		preserveUIChoices(f.selection, names)
		replacementNames := make([]string, 0, len(replacements))
		for _, instruction := range replacements {
			replacementNames = append(replacementNames, instruction.Name)
		}
		preserveUIChoices(f.replacement, replacementNames)
		f.replacement.SetOptions(modeChoices(replacements))
	}
	f.selection.OnChanged = func(name string) {
		for _, instruction := range f.values {
			if instruction.Name == name {
				f.selectedID = instruction.ID
				f.name.SetText(instruction.Name)
				f.prompt.SetText(instruction.Prompt)
				f.replacement.ClearSelected()
				refresh()
				setEnabled(f.remove, true)
				return
			}
		}
	}
	newInstruction := widget.NewButton(uiText("Nova instrução"), d.guard(func() {
		f.selectedID = ""
		f.selection.ClearSelected()
		f.name.SetText("")
		f.prompt.SetText("")
		f.replacement.ClearSelected()
		setEnabled(f.remove, false)
		refresh()
	}))
	f.save = widget.NewButton(uiText("Guardar instrução"), d.guard(func() {
		id := f.selectedID
		if id == "" {
			if len(f.values) >= processing.MaxCustomInstructions {
				f.status.SetText("Já há dez instruções personalizadas. Edite ou exclua uma antes de criar outra.")
				return
			}
			var identifier [12]byte
			if _, err := rand.Read(identifier[:]); err != nil {
				f.status.SetText("Não foi possível criar esta instrução. Tente novamente.")
				return
			}
			id = "custom-" + hex.EncodeToString(identifier[:])
		}
		instruction := processing.Instruction{ID: id, Name: strings.TrimSpace(f.name.Text), Prompt: f.prompt.Text}
		values := slices.Clone(f.values)
		index := slices.IndexFunc(values, func(value processing.Instruction) bool { return value.ID == id })
		if index >= 0 {
			values[index] = instruction
		} else {
			values = append(values, instruction)
		}
		if err := processing.ValidateInstructions(values); err != nil {
			f.status.SetText(err.Error())
			return
		}
		f.values, f.selectedID = values, id
		refresh()
		setUISelected(f.selection, instruction.Name)
		f.status.SetText("Instrução preparada. Salve as configurações para aplicar.")
	}))
	f.remove = widget.NewButton(uiText("Excluir instrução"), d.guard(func() {
		if f.selectedID == "" {
			return
		}
		used := d.settings.Processing.Mode == f.selectedID || main.document.processOptions.Mode == f.selectedID
		for _, doc := range d.documents {
			used = used || doc.processOptions.Mode == f.selectedID
		}
		replacement := modeID(uiSelected(f.replacement), f.values)
		if used && (replacement == "" || replacement == f.selectedID) {
			f.status.SetText("Esta instrução está em uso. Escolha outro modo para os próximos pedidos antes de excluir.")
			return
		}
		if used {
			f.replacements[f.selectedID] = replacement
		}
		for original, mode := range f.replacements {
			if mode == f.selectedID {
				if replacement == "" {
					f.status.SetText("Esta instrução substitui outra excluída. Escolha o próximo modo.")
					return
				}
				f.replacements[original] = replacement
			}
		}
		f.values = slices.DeleteFunc(f.values, func(value processing.Instruction) bool { return value.ID == f.selectedID })
		f.selectedID = ""
		f.selection.ClearSelected()
		f.name.SetText("")
		f.prompt.SetText("")
		refresh()
		f.remove.Disable()
		f.status.SetText("Exclusão preparada. O histórico permanece preservado. Salve as configurações para aplicar.")
	}))
	f.remove.Disable()
	refresh()
	freeHint := widget.NewLabel("Livre envia o texto exato, sem instrução adicional. Mesclar combina as abas do grupo e devolve o resultado à Transcrição, sem busca web.")
	freeHint.Wrapping = fyne.TextWrapWord
	teams := container.NewBorder(d.smallText(freeHint),
		widget.NewButton(uiText("Restaurar Teams"), d.guard(func() { main.teamsPrompt.SetText(processing.DefaultTeamsPrompt) })),
		nil, nil, main.teamsPrompt)
	merge := container.NewBorder(d.smallText(freeHint),
		widget.NewButton(uiText("Restaurar Mesclar"), d.guard(func() { main.mergePrompt.SetText(processing.DefaultMergePrompt) })),
		nil, nil, main.mergePrompt)
	customHeader := container.NewVBox(f.selection, newInstruction, settingLabel(uiText("Nome")), f.name, settingLabel(uiText("Instrução")))
	customFooter := container.NewVBox(f.save, settingLabel(uiText("Modo após excluir")), f.replacement, f.remove, d.smallText(f.status))
	custom := container.NewBorder(customHeader, customFooter, nil, nil, f.prompt)
	f.customPage = custom
	root := container.NewVBox(
		d.smallText(widget.NewLabel("As instruções ficam separadas por finalidade. Escolha Teams, Mesclar ou Personalizadas na árvore à esquerda.")),
	)
	return instructionSettingsPages{root: root, teams: teams, merge: merge, custom: custom}
}
