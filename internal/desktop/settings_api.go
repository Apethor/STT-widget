package desktop

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"ai-talker/internal/api"
	"ai-talker/internal/apiconfig"
	"ai-talker/internal/secrets"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

const (
	openRouterChoice  = "OpenRouter"
	openAIChoice      = "OpenAI"
	genericAPIChoice  = "API compatível"
	azureAPIChoice    = "Azure OpenAI / Foundry"
	azureSpeechChoice = "Azure Speech · MAI-Transcribe-2"
	newAPIChoice      = "＋ Nova conexão"
)

type apiForm struct {
	provider         *widget.Select
	endpoint         *widget.Entry
	name             *widget.Entry
	managerSelect    *widget.Select
	activeSelect     *widget.Select
	model            *widget.SelectEntry
	key              *widget.Entry
	status           *widget.Label
	saveKey          *widget.Button
	remove           *widget.Button
	removeConnection *widget.Button
	saveConnection   *widget.Button
	newConnection    *widget.Button
	catalog          *widget.Button
	textEnabled      *widget.Check
	audioEnabled     *widget.Check
	language         *widget.Entry
	languageChoice   *widget.Select
	help             *widget.Label
	content          fyne.CanvasObject
	managerContent   fyne.CanvasObject
	connectionID     string
	managerAll       bool
	manager          *apiForm
	lastProvider     string
	flowAudio        bool
	syncing          bool
	choices          map[string]string
	alive            func() bool
	epoch            uint64
	busy             bool
}

func (f *apiForm) connection() apiconfig.Connection {
	provider := apiconfig.OpenRouter
	switch uiSelected(f.provider) {
	case openAIChoice:
		provider = apiconfig.OpenAI
	case genericAPIChoice:
		provider = apiconfig.Generic
	case azureAPIChoice:
		provider = apiconfig.Azure
	case azureSpeechChoice:
		provider = apiconfig.AzureSpeech
	}
	connection := apiconfig.Connection{Provider: provider}
	connection.ID = f.connectionID
	if provider == apiconfig.Generic || provider == apiconfig.Azure || provider == apiconfig.AzureSpeech {
		connection.Endpoint = strings.TrimSpace(f.endpoint.Text)
	}
	return connection
}

func (d *desktop) newAPIForm(connection apiconfig.Connection, model *widget.SelectEntry, alive func() bool, audio bool) *apiForm {
	if connection.ID == "" {
		for _, saved := range d.settings.APIConnections {
			if (audio && saved.Audio || !audio && saved.Text) && saved.Provider == connection.Provider &&
				saved.Connection().BaseURL() == connection.BaseURL() {
				connection.ID = saved.ID
				break
			}
		}
		if connection.ID == "" {
			id, err := apiconfig.NewConnectionID()
			if err == nil {
				connection.ID = id
			}
		}
	}
	providerOptions := []string{openRouterChoice, openAIChoice, genericAPIChoice}
	if !audio {
		providerOptions = append(providerOptions, azureAPIChoice)
	} else {
		providerOptions = append(providerOptions, azureSpeechChoice)
	}
	f := &apiForm{
		provider: widget.NewSelect(providerOptions, nil),
		endpoint: widget.NewEntry(), name: widget.NewEntry(),
		managerSelect: widget.NewSelect(nil, nil), activeSelect: widget.NewSelect(nil, nil),
		model: model, key: widget.NewPasswordEntry(), connectionID: connection.ID, flowAudio: audio,
		status:  widget.NewLabel("A chave fica no Gerenciador de Credenciais do Windows, separada das preferências."),
		alive:   alive,
		choices: make(map[string]string),
	}
	f.status.Wrapping = fyne.TextWrapWord
	f.name.SetPlaceHolder(uiText("Nome desta conexão"))
	f.endpoint.SetPlaceHolder(uiText("https://servidor/v1"))
	f.key.SetPlaceHolder(uiText("Cole uma chave para salvar ou substituir"))
	f.textEnabled = widget.NewCheck(uiText("Disponível para Texto"), nil)
	f.audioEnabled = widget.NewCheck(uiText("Disponível para Áudio"), nil)
	f.textEnabled.SetChecked(!audio)
	f.audioEnabled.SetChecked(audio)
	f.saveKey = widget.NewButton(uiText("Salvar chave"), d.guard(func() { d.saveAPIKey(f, false) }))
	f.remove = widget.NewButton(uiText("Remover chave"), d.guard(func() { d.saveAPIKey(f, true) }))
	f.removeConnection = widget.NewButton(uiText("Remover conexão"), d.guard(func() { d.removeAPIConnection(f) }))
	f.saveConnection = widget.NewButton(uiText("Salvar conexão"), d.guard(func() { d.saveAPIConnection(f) }))
	f.newConnection = widget.NewButton(uiText("＋ Nova conexão"), d.guard(func() { d.newAPIConnection(f) }))
	f.catalog = widget.NewButton(uiText("Consultar modelos / testar conexão"), d.guard(func() { d.loadAPIModels(f) }))
	setUISelected(f.provider, openRouterChoice)
	if connection.Provider == apiconfig.OpenAI {
		setUISelected(f.provider, openAIChoice)
	} else if connection.Provider == apiconfig.Generic {
		setUISelected(f.provider, genericAPIChoice)
	} else if connection.Provider == apiconfig.Azure && !audio {
		setUISelected(f.provider, azureAPIChoice)
	} else if connection.Provider == apiconfig.AzureSpeech && audio {
		setUISelected(f.provider, azureSpeechChoice)
	}
	f.endpoint.SetText(connection.BaseURL())
	if connection.Provider == "" {
		f.endpoint.SetText((apiconfig.Connection{Provider: apiconfig.OpenRouter}).BaseURL())
	}
	f.lastProvider = uiSelected(f.provider)
	f.name.SetText(d.apiConnectionName(connection.ID))
	if f.name.Text == "" {
		f.name.SetText(d.defaultAPIConnectionName(f.flowAudio))
	}
	if saved, ok := d.apiSavedConnection(connection.ID); ok {
		f.textEnabled.SetChecked(saved.Text)
		f.audioEnabled.SetChecked(saved.Audio)
	}
	d.syncAPIFormChoices(f)
	syncEndpoint := func() {
		if f.syncing {
			return
		}
		f.epoch++
		f.key.SetText("")
		f.status.SetText("Áudio e texto podem usar a mesma chave quando o endpoint é o mesmo.")
		if !f.managerAll && f.flowAudio && uiSelected(f.provider) == azureSpeechChoice &&
			f.lastProvider != azureSpeechChoice && d.apiConnectionUsedByText(f.connectionID) {
			if id, err := apiconfig.NewConnectionID(); err == nil {
				f.connectionID = id
				f.name.SetText(d.defaultAPIConnectionName(true))
				f.textEnabled.SetChecked(false)
				f.audioEnabled.SetChecked(true)
				f.status.SetText("Azure Speech ganhou uma conexão separada para preservar a conexão de Texto e sua chave.")
			}
		}
		switch uiSelected(f.provider) {
		case genericAPIChoice, azureAPIChoice, azureSpeechChoice:
			f.endpoint.Enable()
			if uiSelected(f.provider) == azureAPIChoice && f.lastProvider != azureAPIChoice {
				f.endpoint.SetText("")
				f.endpoint.SetPlaceHolder(uiText("https://recurso.openai.azure.com/openai/v1"))
			} else if uiSelected(f.provider) == azureSpeechChoice && f.lastProvider != azureSpeechChoice {
				f.endpoint.SetText("")
				f.endpoint.SetPlaceHolder(uiText("https://<região>.api.cognitive.microsoft.com"))
			} else if uiSelected(f.provider) == genericAPIChoice {
				f.endpoint.SetPlaceHolder(uiText("https://servidor/v1"))
			}
		default:
			f.endpoint.Disable()
			f.endpoint.SetText(f.connection().BaseURL())
		}
		d.syncAPIProviderControls(f)
		f.lastProvider = uiSelected(f.provider)
		d.syncAPIFormChoices(f)
	}
	f.provider.OnChanged = func(string) { syncEndpoint() }
	f.endpoint.OnChanged = func(string) {
		if f.syncing {
			return
		}
		f.epoch++
		f.key.SetText("")
		f.status.SetText("Salve ou use a chave deste endpoint. A chave de outro endereço não é enviada.")
	}
	if uiSelected(f.provider) != genericAPIChoice {
		if uiSelected(f.provider) != azureAPIChoice && uiSelected(f.provider) != azureSpeechChoice {
			f.endpoint.Disable()
		}
	}
	d.syncAPIProviderControls(f)
	selectConnection := func(choice string) {
		if f.syncing {
			return
		}
		if choice == newAPIChoice {
			d.newAPIConnection(f)
			return
		}
		id := f.choices[choice]
		if id != "" {
			d.applyAPIConnection(f, id)
		}
	}
	f.managerSelect.OnChanged = selectConnection
	f.activeSelect.OnChanged = selectConnection
	f.content = container.NewVBox(
		settingLabel(uiText("Conexão")),
		f.activeSelect,
		widget.NewButton(uiText("Gerenciar conexões…"), d.guard(func() { d.openSettingsSection("APIs") })),
	)
	f.managerContent = container.NewVBox(
		f.newConnection,
		settingLabel(uiText("Conexão selecionada")), f.managerSelect,
		settingLabel(uiText("Nome")), f.name,
		settingLabel(uiText("Provider")), f.provider,
		settingLabel(uiText("Endpoint")), f.endpoint,
		f.textEnabled, f.audioEnabled,
		settingLabel(uiText("Chave/token")), f.key,
		container.NewHBox(f.saveKey, f.remove),
		container.NewHBox(f.saveConnection, f.removeConnection),
		f.catalog, d.smallText(f.status),
	)
	return f
}

// The manager edits the shared catalog. Audio/LLM forms continue to choose
// their active connection independently and show only compatible entries.
func (d *desktop) buildAPIConnectionManager(main *settingsForm) fyne.CanvasObject {
	connection := d.settings.AudioAPI.Connection
	if connection.Provider == "" && main.document.processOptions.Provider == "api" {
		connection = main.document.processOptions.API
	}
	if connection.Provider == "" && len(d.settings.APIConnections) > 0 {
		connection = d.settings.APIConnections[0].Connection()
	}
	manager := d.newAPIForm(connection, widget.NewSelectEntry(nil),
		func() bool { return d.settingsForm == main }, true)
	manager.managerAll = true
	manager.provider.SetOptions([]string{openRouterChoice, openAIChoice, genericAPIChoice, azureAPIChoice, azureSpeechChoice})
	if main.audioAPI != nil {
		main.audioAPI.manager = manager
	}
	if connection.ID != "" {
		d.applyAPIConnection(manager, connection.ID)
	}
	d.syncAPIFormChoices(manager)
	note := widget.NewLabel("Catálogo compartilhado de conexões. Texto e Áudio escolhem a conexão ativa nas suas páginas. Modelos e idioma pertencem ao fluxo; a chave fica no Windows.")
	note.Wrapping = fyne.TextWrapWord
	return container.NewVBox(note, manager.managerContent)
}

func (d *desktop) defaultAPIConnectionName(audio bool) string {
	prefix := "Texto"
	if audio {
		prefix = "Áudio"
	}
	for number := len(d.settings.APIConnections) + 1; ; number++ {
		name := fmt.Sprintf("%s %d", prefix, number)
		if !slices.ContainsFunc(d.settings.APIConnections, func(saved apiconfig.SavedConnection) bool {
			return strings.EqualFold(name, strings.TrimSpace(saved.Name))
		}) {
			return name
		}
	}
}

func (d *desktop) apiConnectionName(id string) string {
	if saved, ok := d.apiSavedConnection(id); ok {
		return saved.Name
	}
	return ""
}

func (d *desktop) apiSavedConnection(id string) (apiconfig.SavedConnection, bool) {
	for _, saved := range d.settings.APIConnections {
		if saved.ID == id {
			return saved, true
		}
	}
	return apiconfig.SavedConnection{}, false
}

func (d *desktop) syncAPIFormChoices(form *apiForm) {
	if form == nil {
		return
	}
	choices := make([]string, 0, len(d.settings.APIConnections)+2)
	form.choices = make(map[string]string)
	hasCurrent := false
	for _, saved := range d.settings.APIConnections {
		if !form.managerAll && (form.flowAudio && !saved.Audio || !form.flowAudio && !saved.Text) {
			continue
		}
		choice := apiConnectionLabel(saved)
		choices = append(choices, choice)
		form.choices[choice] = saved.ID
		hasCurrent = hasCurrent || saved.ID == form.connectionID
	}
	if !hasCurrent && form.connectionID != "" {
		current := form.connection()
		name := strings.TrimSpace(form.name.Text)
		if name == "" {
			name = d.defaultAPIConnectionName(form.flowAudio)
		}
		choice := name + " · " + apiProviderName(current.Provider) + " (não salva)"
		choices = append(choices, choice)
		form.choices[choice] = form.connectionID
	}
	preserveUIChoices(form.managerSelect, choices)
	preserveUIChoices(form.activeSelect, choices)
	choices = append(choices, newAPIChoice)
	form.syncing = true
	form.managerSelect.SetOptions(choices)
	form.activeSelect.SetOptions(slices.DeleteFunc(slices.Clone(choices), func(choice string) bool {
		return choice == newAPIChoice
	}))
	selected := ""
	for choice, id := range form.choices {
		if id == form.connectionID {
			selected = choice
			break
		}
	}
	setUISelected(form.managerSelect, selected)
	setUISelected(form.activeSelect, selected)
	form.syncing = false
}

func (d *desktop) applyAPIConnection(form *apiForm, id string) {
	saved, ok := d.apiSavedConnection(id)
	if !ok || form == nil {
		return
	}
	form.syncing = true
	form.connectionID = saved.ID
	form.name.SetText(saved.Name)
	setUISelected(form.provider, apiProviderName(saved.Provider))
	form.endpoint.SetText(saved.Connection().BaseURL())
	if saved.Provider == apiconfig.Generic || saved.Provider == apiconfig.Azure || saved.Provider == apiconfig.AzureSpeech {
		form.endpoint.Enable()
	} else {
		form.endpoint.Disable()
	}
	form.textEnabled.SetChecked(saved.Text)
	form.audioEnabled.SetChecked(saved.Audio)
	form.key.SetText("")
	form.lastProvider = uiSelected(form.provider)
	form.syncing = false
	form.epoch++
	d.syncAPIProviderControls(form)
	form.status.SetText("Conexão selecionada. A chave permanece no armazenamento seguro do Windows.")
	d.syncAPIFormChoices(form)
}

func (d *desktop) syncAPIProviderControls(form *apiForm) {
	if form == nil || form.textEnabled == nil {
		return
	}
	if uiSelected(form.provider) == azureSpeechChoice {
		form.textEnabled.SetChecked(false)
		form.textEnabled.Disable()
		form.audioEnabled.SetChecked(true)
		form.audioEnabled.Disable()
		form.catalog.Hide()
		form.status.SetText("Azure Speech é somente para áudio; o modelo MAI-Transcribe-2 é fixo.")
		return
	}
	if uiSelected(form.provider) == azureAPIChoice {
		form.textEnabled.SetChecked(true)
		form.textEnabled.Disable()
		form.audioEnabled.SetChecked(false)
		form.audioEnabled.Disable()
		form.catalog.Show()
		return
	}
	form.textEnabled.Enable()
	form.audioEnabled.Enable()
	form.catalog.Show()
}

func (d *desktop) newAPIConnection(form *apiForm) {
	if form == nil || !form.alive() || form.busy {
		return
	}
	id, err := apiconfig.NewConnectionID()
	if err != nil {
		form.status.SetText(err.Error())
		return
	}
	form.syncing = true
	form.connectionID = id
	form.name.SetText(d.defaultAPIConnectionName(form.flowAudio))
	setUISelected(form.provider, genericAPIChoice)
	form.endpoint.SetText("")
	form.endpoint.SetPlaceHolder(uiText("https://servidor/v1"))
	form.endpoint.Enable()
	form.key.SetText("")
	form.textEnabled.SetChecked(!form.flowAudio)
	form.audioEnabled.SetChecked(form.flowAudio)
	if form.managerAll {
		form.textEnabled.SetChecked(true)
		form.audioEnabled.SetChecked(true)
	}
	form.syncing = false
	form.lastProvider = genericAPIChoice
	form.epoch++
	d.syncAPIProviderControls(form)
	form.status.SetText("Informe o endpoint, escolha os fluxos e salve a conexão.")
	d.syncAPIFormChoices(form)
}

func (d *desktop) refreshAPIForms() {
	if d.settingsForm == nil {
		return
	}
	d.syncAPIFormChoices(d.settingsForm.audioAPI)
	if d.settingsForm.audioAPI != nil && d.settingsForm.audioAPI.manager != nil {
		d.syncAPIFormChoices(d.settingsForm.audioAPI.manager)
	}
	if d.settingsForm.llm != nil {
		d.syncAPIFormChoices(d.settingsForm.llm.api)
	}
}

func (d *desktop) saveAPIConnection(form *apiForm) {
	if !form.alive() || form.busy {
		return
	}
	connection := form.connection()
	if err := connection.Validate(); err != nil {
		form.status.SetText(err.Error())
		return
	}
	name := strings.TrimSpace(form.name.Text)
	if name == "" || len([]rune(name)) > 80 || strings.ContainsAny(name, "\x00\r\n\t") {
		form.status.SetText("Informe um nome de até 80 caracteres para a conexão.")
		return
	}
	if !form.textEnabled.Checked && !form.audioEnabled.Checked {
		form.status.SetText("Marque Texto, Áudio ou os dois fluxos para esta conexão.")
		return
	}
	for _, doc := range d.documents {
		if doc.processOptions.Provider == "api" && doc.processOptions.API.ID == connection.ID && !form.textEnabled.Checked {
			form.status.SetText("Esta conexão está selecionada para Texto em uma aba. Escolha outra conexão nessa aba antes de desmarcar Texto.")
			return
		}
	}
	updated := cloneSettings(d.settings)
	saved := apiconfig.SavedConnection{
		ID: connection.ID, Name: name, Provider: connection.Provider, Endpoint: connection.Endpoint,
		Text: form.textEnabled.Checked, Audio: form.audioEnabled.Checked,
	}
	index := -1
	for candidate := range updated.APIConnections {
		if updated.APIConnections[candidate].ID == saved.ID {
			index = candidate
			break
		}
	}
	if index >= 0 {
		updated.APIConnections[index] = saved
	} else {
		updated.APIConnections = append(updated.APIConnections, saved)
	}
	updateConnectionSelections(&updated, saved.Connection())
	if err := updated.Validate(); err != nil {
		form.status.SetText(err.Error())
		return
	}
	form.busy = true
	form.saveConnection.Disable()
	d.persistPreferences(updated, func(err error) {
		form.busy = false
		form.saveConnection.Enable()
		if err != nil {
			form.status.SetText("Não foi possível salvar a conexão: " + shortMessage(err.Error()))
			return
		}
		form.status.SetText("Conexão salva. O nome e o endpoint ficam nas preferências; a chave permanece no Windows.")
		for _, doc := range d.documents {
			if doc.processOptions.API.ID == connection.ID {
				doc.processOptions.API = connection
				d.syncProcessingOptions(doc)
			}
		}
		if main := d.settingsForm; main != nil {
			activeForms := []*apiForm{main.audioAPI}
			if main.llm != nil {
				activeForms = append(activeForms, main.llm.api)
			}
			for _, active := range activeForms {
				if active != nil && active.connectionID == connection.ID {
					d.applyAPIConnection(active, connection.ID)
				}
			}
		}
		if err := d.saveWorkspace(); err != nil {
			form.status.SetText("Conexão salva, mas não foi possível atualizar as seleções das abas.")
		}
		d.refreshAPIForms()
	})
}

func (d *desktop) removeAPIConnection(form *apiForm) {
	if !form.alive() || form.busy {
		return
	}
	connection := form.connection()
	saved, exists := d.apiSavedConnection(connection.ID)
	if !exists {
		form.status.SetText("A conexão ainda não está salva.")
		return
	}
	connection = saved.Connection()
	if d.apiConnectionInUse(connection.ID) {
		form.status.SetText("Esta conexão está em uso. Escolha outra no fluxo correspondente e salve as configurações antes de removê-la.")
		return
	}
	updated := cloneSettings(d.settings)
	updated.APIConnections = slices.DeleteFunc(updated.APIConnections, func(saved apiconfig.SavedConnection) bool {
		return saved.ID == connection.ID
	})
	catalog := slices.Clone(d.settings.APIConnections)
	form.busy = true
	form.removeConnection.Disable()
	var restoreSecrets func() error
	d.operation(func() error {
		var err error
		restoreSecrets, err = d.deleteAPIConnectionSecrets(connection, catalog)
		return err
	}, func(err error) {
		if err != nil {
			form.busy = false
			form.removeConnection.Enable()
			form.status.SetText("Não foi possível remover a conexão do Windows e das preferências.")
			return
		}
		d.persistPreferences(updated, func(saveErr error) {
			if saveErr == nil {
				form.busy = false
				form.removeConnection.Enable()
				form.status.SetText("Conexão removida.")
				d.refreshAPIForms()
				return
			}
			d.operation(restoreSecrets, func(restoreErr error) {
				form.busy = false
				form.removeConnection.Enable()
				message := "Não foi possível salvar a remoção. A conexão e sua chave foram preservadas."
				if restoreErr != nil {
					message = "Não foi possível salvar a remoção nem restaurar a chave no Windows."
				}
				form.status.SetText(message)
			})
		})
	})
}

func (d *desktop) deleteAPIConnectionSecrets(
	connection apiconfig.Connection, catalog []apiconfig.SavedConnection,
) (func() error, error) {
	targets := []string{connection.CredentialTarget()}
	if connection.ID == apiconfig.LegacyConnectionID(connection) {
		targets = append(targets, connection.LegacyCredentialTarget())
	}
	values := make(map[string]string, len(targets))
	for _, target := range targets {
		value, err := d.deps.vault.Read(target)
		if err != nil && !errors.Is(err, secrets.ErrNotFound) {
			return nil, errors.New("não foi possível ler a credencial segura da conexão")
		}
		if err == nil {
			values[target] = value
		}
	}
	if legacy, exists := values[connection.LegacyCredentialTarget()]; exists {
		for _, saved := range catalog {
			other := saved.Connection()
			if other.ID == connection.ID || other.ID != apiconfig.LegacyConnectionID(other) ||
				other.BaseURL() != connection.BaseURL() {
				continue
			}
			if _, err := d.deps.vault.Read(other.CredentialTarget()); err == nil {
				continue
			} else if !errors.Is(err, secrets.ErrNotFound) {
				return nil, errors.New("não foi possível verificar a credencial segura de outra conexão legada")
			}
			if err := d.deps.vault.Write(other.CredentialTarget(), legacy); err != nil {
				return nil, errors.New("não foi possível preservar a credencial segura de outra conexão legada")
			}
		}
	}
	restore := func() error {
		var err error
		for target, value := range values {
			err = errors.Join(err, d.deps.vault.Write(target, value))
		}
		return err
	}
	for _, target := range targets {
		if err := d.deps.vault.Delete(target); err != nil && !errors.Is(err, secrets.ErrNotFound) {
			if restoreErr := restore(); restoreErr != nil {
				return nil, errors.New("não foi possível remover nem restaurar a credencial segura")
			}
			return nil, errors.New("não foi possível remover a credencial segura")
		}
	}
	return restore, nil
}

func (d *desktop) saveAPIKey(f *apiForm, remove bool) {
	if !f.alive() || f.busy {
		return
	}
	connection := f.connection()
	if err := connection.Validate(); err != nil {
		f.status.SetText(err.Error())
		return
	}
	key := strings.TrimSpace(f.key.Text)
	if !remove && (key == "" || len(key) > 2560 || strings.ContainsAny(key, "\x00\r\n")) {
		f.status.SetText("Cole uma chave válida antes de salvar.")
		return
	}
	epoch := f.epoch
	catalog := slices.Clone(d.settings.APIConnections)
	f.busy = true
	f.saveKey.Disable()
	f.remove.Disable()
	f.catalog.Disable()
	f.status.SetText("Atualizando a chave no Windows…")
	d.operation(func() error {
		if remove {
			_, err := d.deleteAPIConnectionSecrets(connection, catalog)
			return err
		}
		return d.deps.vault.Write(connection.CredentialTarget(), key)
	}, func(err error) {
		if !f.alive() {
			return
		}
		f.busy = false
		f.saveKey.Enable()
		f.remove.Enable()
		f.catalog.Enable()
		if epoch != f.epoch {
			return
		}
		if err != nil {
			f.status.SetText("Não foi possível atualizar a chave no armazenamento seguro do Windows.")
			return
		}
		f.key.SetText("")
		f.status.SetText("Chave salva no Windows. Ela não é incluída nas configurações ou exportações.")
		if remove {
			f.status.SetText("Chave removida deste endpoint.")
		}
	})
}

func (d *desktop) loadAPIModels(f *apiForm) {
	if !f.alive() || f.busy {
		return
	}
	connection := f.connection()
	if err := connection.Validate(); err != nil {
		f.status.SetText(err.Error())
		return
	}
	if strings.TrimSpace(f.key.Text) != "" {
		f.status.SetText("Salve a chave antes de testar a conexão.")
		return
	}
	epoch := f.epoch
	audioCatalog := f.language != nil || f.managerAll && f.audioEnabled.Checked && !f.textEnabled.Checked
	f.busy = true
	f.catalog.Disable()
	f.saveKey.Disable()
	f.remove.Disable()
	f.status.SetText("Consultando o catálogo deste endpoint…")
	var models []api.Model
	d.operation(func() error {
		client, err := api.NewClient(connection, d.deps.vault, nil)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(d.ctx, 30*time.Second)
		defer cancel()
		if audioCatalog {
			models, err = client.AudioModels(ctx)
		} else {
			models, err = client.Models(ctx)
		}
		return err
	}, func(err error) {
		if !f.alive() {
			return
		}
		f.busy = false
		f.catalog.Enable()
		f.saveKey.Enable()
		f.remove.Enable()
		if epoch != f.epoch {
			return
		}
		if err != nil {
			if errors.Is(err, secrets.ErrNotFound) {
				f.status.SetText("Ainda não há chave salva para este endpoint.")
			} else {
				f.status.SetText("Catálogo indisponível. Você pode informar o ID do modelo manualmente. " + shortMessage(err.Error()))
			}
			return
		}
		ids := make([]string, 0, len(models))
		for _, model := range models {
			ids = append(ids, model.ID)
		}
		f.model.SetOptions(ids)
		if f.language != nil && len(ids) == 0 {
			f.status.SetText("Este catálogo não confirmou modelos de transcrição de arquivo. Consulte o provider e informe o ID manualmente.")
			return
		}
		f.status.SetText("Conexão testada. Escolha um ID compatível com este recurso ou informe outro manualmente.")
	})
}

func (d *desktop) buildAudioAPISettings(form *settingsForm) (fyne.CanvasObject, fyne.CanvasObject) {
	model := widget.NewSelectEntry(nil)
	model.SetText(d.settings.AudioAPI.Model)
	f := d.newAPIForm(d.settings.AudioAPI.Connection, model, func() bool { return d.settingsForm == form }, true)
	f.language = widget.NewEntry()
	f.language.SetPlaceHolder(uiText("Automático · ou código, por exemplo pt"))
	f.language.SetText(d.settings.AudioAPI.Language)
	f.help = widget.NewLabel("")
	f.help.Wrapping = fyne.TextWrapWord
	languages := []struct{ name, code string }{
		{"Automático", ""}, {"Português", "pt"}, {"Inglês", "en"},
		{"Espanhol", "es"}, {"Francês", "fr"}, {"Alemão", "de"},
		{"Português + Inglês", "pt,en"},
	}
	f.languageChoice = widget.NewSelect(nil, func(choice string) {
		for _, language := range languages {
			if language.name == choice {
				f.language.SetText(language.code)
				break
			}
		}
	})
	f.languageChoice.PlaceHolder = "Sugestões de idioma"
	syncingModel := false
	syncModel := func() bool {
		if uiSelected(f.provider) == azureSpeechChoice {
			if f.model.Text != apiconfig.AzureSpeechModel {
				syncingModel = true
				f.model.SetText(apiconfig.AzureSpeechModel)
				syncingModel = false
			}
			f.model.Disable()
			f.catalog.Hide()
			return true
		}
		if f.model.Text == apiconfig.AzureSpeechModel {
			syncingModel = true
			f.model.SetText("")
			syncingModel = false
		}
		f.model.Enable()
		f.catalog.Show()
		return false
	}
	syncHelp := func() {
		audio := apiconfig.Audio{Connection: f.connection(), Model: strings.TrimSpace(f.model.Text), Language: f.language.Text}
		if syncModel() {
			note := "Azure Speech · MAI-Transcribe-2 é experimental e está em public preview, sem SLA. Exige um recurso Speech e uma chave autorizada pela administração. Use o endpoint regional do recurso e escolha um idioma ou Automático. O app solicita diarização e mantém timestamps nos dados da transcrição. Disponível somente para gravações concluídas, importações e retranscrições; sem Stream."
			if _, err := audio.LanguageCodes(); err != nil {
				note += "\n" + err.Error()
			}
			f.help.SetText(note)
			f.languageChoice.SetOptions([]string{"Automático", "Português", "Inglês", "Espanhol", "Francês", "Alemão"})
			return
		}
		note := "Automático deixa o serviço detectar o idioma. Português: pt · Inglês: en · Espanhol: es · Francês: fr · Alemão: de."
		var choices []string
		for index, language := range languages {
			if index < 6 || audio.MultipleLanguages() {
				choices = append(choices, language.name)
			}
		}
		f.languageChoice.SetOptions(choices)
		if audio.MultipleLanguages() {
			note += "\nEste modelo aceita vários códigos, separados por vírgulas, por exemplo pt,en."
			note += " As variantes chinesas documentadas são zh-cn, zh-tw e zh-hk."
		} else {
			note += "\nInforme um único código ou escolha Automático para áudio com idiomas misturados."
			if audio.Provider != apiconfig.Generic {
				note += " Use um código de duas letras."
			}
		}
		if audio.Provider == apiconfig.OpenAI {
			note += "\nSugestões de modelo: gpt-transcribe, gpt-4o-transcribe, gpt-4o-mini-transcribe e whisper-1. O catálogo e o acesso dependem da sua conta."
		} else if audio.Provider == apiconfig.OpenRouter {
			note += "\nConsulte o catálogo de transcrição deste provider; um modelo de chat com áudio pode usar outro formato de pedido."
		} else {
			note += "\nConfirme /audio/transcriptions e seus idiomas no endpoint. Catálogos sem essa capacidade declarada mantêm a entrada manual."
		}
		if _, err := audio.LanguageCodes(); err != nil {
			note += "\n" + err.Error()
		}
		f.help.SetText(note)
	}
	providerChanged := f.provider.OnChanged
	f.provider.OnChanged = func(value string) { providerChanged(value); syncHelp() }
	f.model.OnChanged = func(string) {
		if !syncingModel {
			syncHelp()
		}
	}
	f.language.OnChanged = func(string) { syncHelp() }
	syncHelp()
	form.audioAPI = f
	note := widget.NewLabel("OpenAI, OpenRouter e API compatível usam /audio/transcriptions. Azure Speech usa o adaptador MAI-Transcribe-2 e credencial próprios. Todos os fluxos de áudio são por arquivo, sem Stream.")
	note.Wrapping = fyne.TextWrapWord
	connectionPage := container.NewVBox(
		note, f.managerContent,
		d.smallText(widget.NewLabel("A configuração do modelo e do idioma fica em Áudio → API.")),
	)
	audioPage := container.NewVBox(
		f.content,
		settingLabel(uiText("Modelo de transcrição · ID")), f.model,
		f.catalog, d.smallText(f.status),
		settingLabel(uiText("Idioma")), f.languageChoice, f.language, d.smallText(f.help),
	)
	return connectionPage, audioPage
}

func (d *desktop) openAudioAPISettings() {
	d.openSettingsSection("APIs")
	if d.settingsForm != nil {
		setUISelected(d.settingsForm.whisper.provider, apiProviderChoice)
	}
}
