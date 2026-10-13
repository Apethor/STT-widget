package desktop

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"ai-talker/internal/apiconfig"
	"ai-talker/internal/config"
	"ai-talker/internal/platform"
	"ai-talker/internal/processing"
	"ai-talker/internal/recorder"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const (
	automaticProfile  = "Automático"
	defaultMicrophone = "Padrão do sistema"
	qualityChoice     = "Voxtral Small 24B"
	fastChoice        = "Voxtral Mini 3B"
)

type settingsForm struct {
	dialog            *dialog.CustomDialog
	profile           *widget.SelectEntry
	region            *widget.Entry
	quality           *widget.Select
	streamModel       *widget.Select
	cloudLanguage     *widget.Select
	cloudBucket       *widget.Entry
	cloudUseBucket    *widget.Check
	cloudTestBucket   *widget.Button
	cloudBucketResult *widget.Label
	bucketCancel      context.CancelFunc
	bucketSerial      uint64
	dialogSize        fyne.Size
	cloudDirect       *widget.Button
	microphone        *widget.Select
	playback          *widget.Select
	deviceRefresh     *actionButton
	playbackChoices   map[string]recorder.Device
	devicesLoading    bool
	profilesLoading   bool
	videoPreset       *widget.Select
	chunk             *widget.Entry
	searchLimit       *widget.Entry
	concurrency       *widget.Select
	fallback          *widget.Check
	glossary          *widget.Entry
	save              *widget.Button
	errorLabel        *widget.Label
	devices           map[string]int
	endpoints         map[string]string
	defaultProfile    string
	loading           bool
	layout            *widget.Select
	interfaceLanguage *widget.Select
	conversationModel *widget.Select
	recordingSize     *widget.Select
	invert            *widget.Check
	fontFamily        *widget.Select
	fontSize          *widget.Select
	connect           *widget.Button
	connection        *widget.Label
	mergeModel        *widget.SelectEntry
	mergePrompt       *widget.Entry
	mergeReasoning    *widget.Select
	mergeOutputTokens *widget.Select
	sections          *settingsSections
	whisper           *whisperForm
	audioAPI          *apiForm
	document          *document
	llm               *processingForm
	teamsPrompt       *widget.Entry
	instructions      *instructionForm
	cleanup           *cleanupForm
	saveSerial        uint64
}

func (d *desktop) openSettings() {
	if d.settingsForm != nil {
		d.settingsForm.dialog.Show()
		return
	}
	settings := cloneSettings(d.settings)
	form := &settingsForm{
		profile:         widget.NewSelectEntry([]string{automaticProfile}),
		region:          widget.NewEntry(),
		quality:         widget.NewSelect([]string{qualityChoice, fastChoice}, nil),
		microphone:      widget.NewSelect([]string{defaultMicrophone}, nil),
		playback:        widget.NewSelect([]string{defaultMicrophone}, nil),
		chunk:           widget.NewEntry(),
		searchLimit:     widget.NewEntry(),
		glossary:        widget.NewMultiLineEntry(),
		errorLabel:      widget.NewLabel(""),
		devices:         map[string]int{defaultMicrophone: -1},
		endpoints:       make(map[string]string),
		playbackChoices: map[string]recorder.Device{defaultMicrophone: {ID: -1}},
		loading:         true,
		profilesLoading: true,
		layout:          widget.NewSelect([]string{inlineChoice, stackedChoice, columnChoice}, nil),
		recordingSize:   widget.NewSelect([]string{smallChoice, mediumChoice, largeChoice}, nil),
		document:        d.document,
	}
	setUISelected(form.layout, stackedChoice)
	form.interfaceLanguage = widget.NewSelect([]string{"Português (Brasil)", "Inglês"}, nil)
	setUISelected(form.interfaceLanguage, "Português (Brasil)")
	if settings.InterfaceLocale() == config.InterfaceEnglish {
		setUISelected(form.interfaceLanguage, "Inglês")
	}
	form.interfaceLanguage.OnChanged = d.guardValue(func(value string) {
		language := config.InterfacePortuguese
		if uiSource(value) == "Inglês" {
			language = config.InterfaceEnglish
		}
		d.changeInterfaceLanguage(language)
	})
	form.invert = widget.NewCheck(uiText("Inverter transcrição e processamento"), nil)
	form.invert.SetChecked(settings.Appearance.InvertPanels)
	form.concurrency = widget.NewSelect([]string{"Automático · 2", "1", "2", "3", "4"}, nil)
	setUISelected(form.concurrency, "Automático · 2")
	if settings.Recovery.Concurrency != 0 {
		setUISelected(form.concurrency, strconv.Itoa(settings.Recovery.Concurrency))
	}
	form.fallback = widget.NewCheck(uiText("Recuperar falhas do Voxtral 24B pelo Whisper configurado"), nil)
	form.fallback.SetChecked(!settings.Recovery.WhisperDisabled)
	if settings.Appearance.Layout == config.LayoutInline {
		setUISelected(form.layout, inlineChoice)
	} else if settings.Appearance.Layout == config.LayoutColumn {
		setUISelected(form.layout, columnChoice)
	}
	setUISelected(form.recordingSize, largeChoice)
	if settings.Appearance.RecordingSize == config.RecordingMedium {
		setUISelected(form.recordingSize, mediumChoice)
	} else if settings.Appearance.RecordingSize == config.RecordingSmall {
		setUISelected(form.recordingSize, smallChoice)
	}
	form.fontFamily = widget.NewSelect([]string{"Segoe UI", "Arial", "Consolas"}, nil)
	setUISelected(form.fontFamily, settings.Appearance.EditorFontFamily())
	fontSizes := []string{"10", "11", "12", "13", "14", "15", "16", "18", "20", "24", "28", "32"}
	form.fontSize = widget.NewSelect(fontSizes, nil)
	setUISelected(form.fontSize, strconv.Itoa(settings.Appearance.EditorFontSize()))
	modelIDs := make([]string, 0, len(processing.Models()))
	for _, model := range processing.Models() {
		modelIDs = append(modelIDs, model.ID)
	}
	form.mergeModel = widget.NewSelectEntry(modelIDs)
	form.mergeModel.SetText(settings.Merge.ModelID)
	form.mergeReasoning = reasoningSelect(settings.Merge.ModelID, settings.Merge.ReasoningEffort)
	form.mergeModel.OnChanged = func(model string) {
		syncReasoningSelect(form.mergeReasoning, strings.TrimSpace(model))
	}
	form.mergePrompt = widget.NewMultiLineEntry()
	form.mergePrompt.Wrapping = fyne.TextWrapWord
	form.mergePrompt.SetMinRowsVisible(8)
	form.mergePrompt.SetText(settings.Merge.Prompt)
	form.connection = widget.NewLabel("O perfil escolhido será usado nas próximas chamadas.")
	form.connection.Wrapping = fyne.TextWrapWord
	form.connect = widget.NewButton(uiText("Conectar com este perfil"), d.guard(func() { d.connectFromForm(form) }))
	form.connect.Disable()
	form.profile.SetText(automaticProfile)
	if settings.Profile != "" {
		form.profile.SetText(settings.Profile)
	}
	form.region.SetText(settings.Region)
	setUISelected(form.quality, qualityChoice)
	if settings.Quality == "fast" {
		setUISelected(form.quality, fastChoice)
	}
	setUISelected(form.microphone, defaultMicrophone)
	form.microphone.Disable()
	setUISelected(form.playback, defaultMicrophone)
	form.playback.Disable()
	form.deviceRefresh = d.iconButton("Atualizar dispositivos", theme.ViewRefreshIcon(), func() {
		d.reloadSettingsDevices(form)
	})
	form.chunk.SetText(strconv.Itoa(settings.ChunkSeconds))
	limit := settings.WebSearchLimit
	if limit == 0 {
		limit = processing.DefaultWebSearches
	}
	form.searchLimit.SetText(strconv.Itoa(limit))
	form.glossary.SetText(settings.Glossary)
	form.glossary.Wrapping = fyne.TextWrapWord
	form.glossary.SetMinRowsVisible(6)
	form.errorLabel.Wrapping = fyne.TextWrapWord
	form.errorLabel.Importance = widget.DangerImportance
	form.errorLabel.Hide()
	form.save = widget.NewButton(uiText("Salvar configurações"), d.guard(func() { d.applySettings(form) }))
	form.save.Importance = widget.HighImportance
	form.save.Disable()
	hint := widget.NewLabel("Até 300 segundos por parte. O áudio completo pode ser muito maior.")
	hint.Wrapping = fyne.TextWrapWord
	visual := container.NewVBox(
		settingLabel(uiText("Idioma da interface")), form.interfaceLanguage,
		d.smallText(widget.NewLabel("A mudança é imediata e não altera o idioma da transcrição ou os textos.")),
		settingLabel(uiText("Disposição dos boxes")), form.layout,
		form.invert,
		settingLabel(uiText("Tamanho do box de gravação")), form.recordingSize,
		settingLabel(uiText("Fonte comum aos editores")), form.fontFamily,
		settingLabel(uiText("Tamanho base da fonte")), form.fontSize,
	)
	connection := container.NewVBox(
		label("Conexão com a AWS.", 13, mutedColor),
		settingLabel(uiText("Perfil AWS")), form.profile,
		settingLabel(uiText("Região")), form.region,
		form.connect, d.smallText(form.connection),
	)
	connection.Add(d.buildBucketSettings(form, settings))
	audioCapture := container.NewVBox(
		container.NewHBox(settingLabel(uiText("Dispositivos de áudio")), form.deviceRefresh),
		settingLabel(uiText("Microfone")), form.microphone,
		settingLabel(uiText("Saída capturada como Sistema")), form.playback,
		settingLabel(uiText("Duração de cada parte, em segundos")), form.chunk,
		hint,
		settingLabel(uiText("Transcrições simultâneas · avançado")), form.concurrency, form.fallback,
	)
	whisperSettings := d.buildWhisperSettings(form, settings)
	audioHeader := container.NewVBox(
		settingLabel(uiText("Transcrever com")), form.whisper.provider,
		d.smallText(widget.NewLabel("Escolha o motor usado para gravações completas, importações e retranscrições.")),
		settingLabel(uiText("Termos que você costuma falar")),
		d.smallText(widget.NewLabel("Vocabulário geral de áudio, independente do provider escolhido.")),
	)
	conversationMode, conversationSettings := d.buildConversationSettings(form)
	audioHeader.Add(conversationMode)
	audio := container.NewBorder(audioHeader, nil, nil, nil, form.glossary)
	cloudSettings := d.buildCloudSettings(form, settings)
	_, apiAudioSettings := d.buildAudioAPISettings(form)
	instructionPages := d.buildInstructionSettings(form)
	cleanupSettings := d.buildCleanupSettings(form)
	instructionsHeader := container.NewVBox(
		settingLabel(uiText("Busca web · máximo por pedido (1 a 100)")), form.searchLimit,
		d.smallText(widget.NewLabel("O limite se aplica aos pedidos com busca web habilitada na aba.")),
	)
	instructions := container.NewVBox(instructionsHeader, instructionPages.root)
	llm := d.buildLLMSettings(form)
	apiConnectionSettings := d.buildAPIConnectionManager(form)
	about := d.buildAboutSettings()
	videoSettings, videoPreset := d.buildVideoSettings(settings)
	form.videoPreset = videoPreset
	connectionOverview := container.NewVBox(
		widget.NewLabel("Perfis AWS e endpoints de API são configurados aqui. A escolha do motor fica em Áudio."),
	)
	dataOverview := container.NewVBox(
		widget.NewLabel("Limpe manualmente arquivos e registros internos do STT-widget."),
	)
	pages := []settingsPage{
		{id: "connections", title: "Conexões", description: "Credenciais, perfis e endereços usados pelo aplicativo.", content: connectionOverview},
		{id: "connections/aws", title: "AWS", description: "Perfil e região usados para autenticar nos serviços AWS.", content: connection},
		{id: "connections/api", title: "API", description: "Endpoints e chaves ficam separados das preferências e dos modelos.", content: apiConnectionSettings},
		{id: "audio", title: "Áudio", description: "Escolha o motor de transcrição e ajuste o vocabulário geral.", content: audio},
		{id: "audio/conversation", title: "Conversa ao vivo", description: "Modelo da conversa ao vivo", content: conversationSettings},
		{id: "audio/capture", title: "Microfone e captura", description: "Fontes de gravação, tamanho das partes e processamento simultâneo.", content: audioCapture},
		{id: "audio/aws", title: "AWS Cloud", description: "Modelos de gravação completa e Stream, além do idioma e S3.", content: cloudSettings},
		{id: "audio/api", title: "API", description: "Modelo e idioma usados para transcrição por arquivo via API.", content: apiAudioSettings},
		{id: "audio/whisper", title: "Whisper", description: "Motor, modelo, idioma e uso de CPU do reconhecimento local.", content: whisperSettings},
		{id: "visual", title: "Visual", description: "Tema, disposição, altura do gravador e leitura dos editores.", content: visual},
		{id: "video", title: "Vídeo", description: "Qualidade e tamanho das capturas de vídeo.", content: videoSettings},
		{id: "llm", title: "LLM", description: "Provider, modelos, raciocínio e perfis de Processar/Mesclar.", content: llm},
		{id: "instructions", title: "Instruções", description: "Busca web e instruções usadas para escrever e combinar textos.", content: instructions},
		{id: "instructions/teams", title: "Teams", description: "Instrução usada para escrever respostas no modo Teams.", content: instructionPages.teams},
		{id: "instructions/merge", title: "Mesclar", description: "Instrução usada ao combinar abas do grupo.", content: instructionPages.merge},
		{id: "instructions/custom", title: "Personalizadas", description: "Crie e organize instruções de processamento próprias.", content: instructionPages.custom},
		{id: "data", title: "Dados", description: "Arquivos internos, histórico e privacidade local.", content: dataOverview},
		{id: "data/cleanup", title: "Limpeza", description: "Escolha uma categoria, um período e os itens internos a remover.", content: cleanupSettings},
		{id: "about", title: "Sobre", description: "Versão e revisão do aplicativo aberto.", content: about},
	}
	compatibility := []*container.TabItem{
		container.NewTabItem("AWS", connection),
		container.NewTabItem("Áudio", audio),
		container.NewTabItem("Visual", visual),
		container.NewTabItem("LLM", llm),
		container.NewTabItem("Instruções", instructions),
		container.NewTabItem("Whisper", whisperSettings),
		container.NewTabItem("Cloud", cloudSettings),
		container.NewTabItem("APIs", apiConnectionSettings),
		container.NewTabItem("Sobre", about),
	}
	form.sections = newSettingsSections(d.window.Canvas(), pages, compatibility,
		[]string{"connections/aws", "audio", "visual", "llm", "instructions", "audio/whisper", "audio/aws", "connections/api", "about"})
	connectionOverview.Add(form.sections.link("Configurar perfil e região AWS", "connections/aws"))
	connectionOverview.Add(form.sections.link("Gerenciar conexões de API", "connections/api"))
	connectionOverview.Add(form.sections.link("Escolher motor e modelos de transcrição", "audio"))
	dataOverview.Add(form.sections.link("Selecionar dados internos para limpeza", "data/cleanup"))
	audioHeader.Add(form.sections.link("Configurar os modelos AWS Cloud", "audio/aws"))
	audioHeader.Add(form.sections.link("Modelo da conversa ao vivo", "audio/conversation"))
	audioHeader.Add(form.sections.link("Configurar modelo e idioma da API", "audio/api"))
	audioHeader.Add(form.sections.link("Configurar o Whisper local", "audio/whisper"))
	cloudSettings.(*fyne.Container).Add(form.sections.link("Configurar o bucket S3 em Conexões → AWS", "connections/aws"))
	form.instructions.sections = &instructionSectionSelector{selectIndex: func(index int) {
		ids := []string{"instructions/teams", "instructions/merge", "instructions/custom"}
		if index >= 0 && index < len(ids) {
			form.sections.selectID(ids[index])
		}
	}}
	form.sections.addSearchEntries(
		settingsSearchEntry{"Conversa ao vivo", "Modos sem Stream, Stream e conversa ao vivo com respostas em texto e áudio.", "audio", conversationMode},
		settingsSearchEntry{"Modelo da conversa ao vivo", "Conversa ao vivo", "audio/conversation", conversationSettings},
		settingsSearchEntry{"Idioma da interface", "Português e inglês nos textos do aplicativo, independente da transcrição.", "visual", form.interfaceLanguage},
		settingsSearchEntry{"Perfil AWS", "Autenticação e região para os serviços AWS.", "connections/aws", form.profile},
		settingsSearchEntry{"Região AWS", "Região usada por Bedrock, Cloud e transcrição.", "connections/aws", form.region},
		settingsSearchEntry{"API", "Provider, endpoint e credencial. Modelos ficam nas páginas de Áudio e LLM.", "connections/api", form.audioAPI.manager.provider},
		settingsSearchEntry{"Endpoint da API", "Endereço base associado a uma chave segura.", "connections/api", form.audioAPI.manager.endpoint},
		settingsSearchEntry{"Chave da API", "Salve a chave no Gerenciador de Credenciais do Windows.", "connections/api", form.audioAPI.manager.key},
		settingsSearchEntry{"Transcrever com", "Escolha AWS Cloud, Whisper local ou API.", "audio", form.whisper.provider},
		settingsSearchEntry{"Termos de áudio", "Vocabulário comum para reconhecimento de fala.", "audio", form.glossary},
		settingsSearchEntry{"Microfone", "Selecione a entrada usada para gravar.", "audio/capture", form.microphone},
		settingsSearchEntry{"Saída capturada como Sistema", "Selecione o dispositivo de som usado pelo loopback e Equipe.", "audio/capture", form.playback},
		settingsSearchEntry{"Atualizar dispositivos", "Atualização manual de entradas e saídas conectadas.", "audio/capture", form.deviceRefresh},
		settingsSearchEntry{"Duração das partes", "Tamanho do áudio enviado por chamada.", "audio/capture", form.chunk},
		settingsSearchEntry{"Transcrições simultâneas", "Limite avançado de chamadas paralelas.", "audio/capture", form.concurrency},
		settingsSearchEntry{"Modelo AWS de gravação completa", "Modelo usado ao parar, importar e retranscrever.", "audio/aws", form.quality},
		settingsSearchEntry{"Modelo AWS de Stream", "Modelo usado durante a gravação.", "audio/aws", form.streamModel},
		settingsSearchEntry{"Idioma AWS", "Idioma indicado ao Amazon Transcribe.", "audio/aws", form.cloudLanguage},
		settingsSearchEntry{"Bucket S3", "Habilitar, configurar e testar escrita, leitura e limpeza no perfil/região AWS.", "connections/aws", form.cloudBucket},
		settingsSearchEntry{"Modelo API de áudio", "ID do modelo para transcrição de arquivos.", "audio/api", form.audioAPI.model},
		settingsSearchEntry{"Idioma API de áudio", "Idioma para transcrição via API; vazio usa detecção automática.", "audio/api", form.audioAPI.language},
		settingsSearchEntry{"Modelo Whisper", "Modelo local de reconhecimento de fala.", "audio/whisper", form.whisper.model},
		settingsSearchEntry{"Idioma Whisper", "Idioma do reconhecimento local.", "audio/whisper", form.whisper.language},
		settingsSearchEntry{"Fonte", "Família e tamanho base comuns aos editores.", "visual", form.fontFamily},
		settingsSearchEntry{"Tamanho da fonte", "Tamanho base de Transcrição e Processar texto.", "visual", form.fontSize},
		settingsSearchEntry{"Zoom", "Ajuste independente em cada caixa e aba.", "visual", form.fontSize},
		settingsSearchEntry{"Qualidade do vídeo", "Presets Baixa, Média e Alta; mudanças valem para a próxima gravação.", "video", form.videoPreset},
		settingsSearchEntry{"Busca web", "Máximo de consultas permitido por pedido.", "instructions", form.searchLimit},
		settingsSearchEntry{"Instrução Teams", "Prompt de redação do modo Teams.", "instructions/teams", form.teamsPrompt},
		settingsSearchEntry{"Instrução Mesclar", "Prompt para combinar as abas selecionadas.", "instructions/merge", form.mergePrompt},
		settingsSearchEntry{"Instruções personalizadas", "Criar, editar e remover modos próprios.", "instructions/custom", form.instructions.prompt},
		settingsSearchEntry{"Limpeza de dados", "Selecionar screenshots, gravações, transcrições ou histórico interno.", "data/cleanup", form.cleanup.category},
		settingsSearchEntry{"Modelo de Processar", "Modelo e raciocínio independentes para a aba.", "llm", form.llm.model},
		settingsSearchEntry{"Modelo de Mesclar", "Modelo e raciocínio usados para combinar as abas.", "llm", form.mergeModel},
		settingsSearchEntry{"Limite de saída da API", "Parâmetros independentes de Processar e Mesclar, incluindo deployments Azure.", "llm", form.llm.outputTokens},
		settingsSearchEntry{"Versão e revisão", "Identificação do executável aberto, em Sobre.", "about", about},
	)
	form.dialog = dialog.NewCustom(
		uiText("Configurações"),
		uiText("Fechar"),
		container.NewBorder(nil, container.NewVBox(form.errorLabel, form.save), nil, nil, form.sections.Object()),
		d.window,
	)
	form.dialogSize = dialogSize(d.window)
	form.dialog.Resize(form.dialogSize)
	form.llm.dialog = form.dialog
	d.processingForm = form.llm
	form.dialog.SetOnClosed(func() {
		d.guard(func() {
			if d.settingsForm == form {
				if form.whisper.cancel != nil {
					form.whisper.cancel()
				}
				if form.bucketCancel != nil {
					form.bucketCancel()
				}
				d.settingsForm = nil
				d.processingForm = nil
			}
		})()
	})
	d.settingsForm = form
	form.dialog.Show()
	d.reloadCleanupItems(form.cleanup)

	var profiles []string
	var defaultProfile string
	var profileErr error
	d.operation(func() error {
		profiles, profileErr = d.deps.profiles()
		defaultProfile = config.DefaultProfile()
		return profileErr
	}, func(err error) {
		if d.settingsForm != form {
			return
		}
		form.profilesLoading = false
		form.loading = form.devicesLoading
		form.defaultProfile = defaultProfile
		form.profile.SetOptions(append([]string{automaticProfile}, profiles...))
		if !form.whisper.busy {
			form.save.Enable()
		}
		form.connect.Enable()
		if err != nil {
			form.errorLabel.SetText("Não foi possível listar todos os perfis. Você pode informar o perfil manualmente.")
			form.errorLabel.Show()
		}
	})
	d.reloadSettingsDevices(form)
}

func settingLabel(text string) *widget.Label {
	result := widget.NewLabel(text)
	result.TextStyle.Bold = true
	return result
}

func (d *desktop) applySettings(form *settingsForm) {
	if d.settingsForm != form || form.loading || form.devicesLoading {
		return
	}
	if form.bucketCancel != nil {
		form.errorLabel.SetText("Conclua ou cancele o teste do bucket antes de salvar as configurações.")
		form.errorLabel.Show()
		return
	}
	apiForms := []*apiForm{form.audioAPI}
	if form.audioAPI != nil {
		apiForms = append(apiForms, form.audioAPI.manager)
	}
	if form.llm != nil {
		apiForms = append(apiForms, form.llm.api)
	}
	for _, api := range apiForms {
		if api != nil && (api.busy || strings.TrimSpace(api.key.Text) != "") {
			form.errorLabel.SetText("Conclua o salvamento da chave antes de salvar estas configurações.")
			form.errorLabel.Show()
			return
		}
	}
	updated, err := d.settingsFromForm(form)
	if err != nil {
		form.errorLabel.SetText(shortMessage(err.Error()))
		form.errorLabel.Show()
		return
	}
	form.save.Disable()
	form.llm.saveProfile.Disable()
	form.saveSerial++
	serial := form.saveSerial
	preferenceEpoch := d.preferenceEpoch.Load() + 1
	form.errorLabel.Hide()
	d.persistPreferences(updated, func(err error) {
		if d.settingsForm != form || serial != form.saveSerial {
			return
		}
		if preferenceEpoch != d.preferenceEpoch.Load() {
			form.save.Enable()
			form.llm.saveProfile.Enable()
			form.errorLabel.SetText("As opções mudaram durante o salvamento. Revise e salve novamente.")
			form.errorLabel.Show()
			return
		}
		if err != nil {
			form.save.Enable()
			form.llm.saveProfile.Enable()
			form.errorLabel.SetText(shortMessage(err.Error()))
			form.errorLabel.Show()
			return
		}
		d.unavailableMicrophone = uiSelected(form.microphone) == unavailableDevice
		d.unavailablePlayback = uiSelected(form.playback) == unavailableDevice
		d.settingsForm = nil
		d.processingForm = nil
		form.dialog.Hide()
		if !form.document.closed {
			d.invalidateProcessing(form.document)
			form.document.processOptions = updated.Processing
			d.applyInstructionReplacements(form)
			for _, doc := range d.documents {
				d.syncProcessingOptions(doc)
			}
			if err := d.saveWorkspace(); err != nil {
				d.showError("Configurações salvas; não foi possível salvar as opções das abas: "+err.Error(), "")
			}
		}
		d.checkAWS()
	})
}

func (d *desktop) settingsFromForm(form *settingsForm) (config.Settings, error) {
	updated := cloneSettings(d.settings)
	seconds, err := strconv.Atoi(strings.TrimSpace(form.chunk.Text))
	if err != nil {
		return updated, errors.New("informe a duração de cada parte em segundos")
	}
	device, ok := form.devices[uiSelected(form.microphone)]
	if !ok {
		return updated, errors.New("selecione um microfone válido")
	}
	output, ok := form.playbackChoices[uiSelected(form.playback)]
	if !ok {
		return updated, errors.New("selecione uma saída de som válida")
	}
	updated.Profile = strings.TrimSpace(form.profile.Text)
	if updated.Profile == automaticProfile || updated.Profile == "" {
		updated.Profile = form.defaultProfile
	}
	updated.Region = strings.TrimSpace(form.region.Text)
	if err := readCloudSettings(form, &updated); err != nil {
		return updated, err
	}
	updated.ChunkSeconds = seconds
	updated.Recovery.WhisperDisabled = !form.fallback.Checked
	updated.Recovery.Concurrency = 0
	if uiSelected(form.concurrency) != "Automático · 2" {
		updated.Recovery.Concurrency, err = strconv.Atoi(uiSelected(form.concurrency))
		if err != nil {
			return updated, errors.New("selecione a quantidade de transcrições simultâneas")
		}
	}
	limit, err := strconv.Atoi(strings.TrimSpace(form.searchLimit.Text))
	if err != nil || limit < 1 || limit > 100 {
		return updated, errors.New("use entre 1 e 100 buscas por pedido")
	}
	updated.WebSearchLimit = limit
	updated.Glossary = strings.TrimSpace(form.glossary.Text)
	updated.Appearance.InvertPanels = form.invert.Checked
	updated.VideoPreset = videoPresetValue(uiSelected(form.videoPreset))
	updated.Appearance.FontFamily = uiSelected(form.fontFamily)
	updated.Appearance.FontSize, err = strconv.Atoi(uiSelected(form.fontSize))
	if err != nil {
		return updated, errors.New("selecione o tamanho base da fonte")
	}
	options, err := d.llmOptionsFromForm(form)
	if err != nil {
		return updated, err
	}
	updated.Processing = options
	if options.Provider == "api" {
		if err := ensureAPIConnection(&updated, options.API, form.llm.api.name.Text, true, false); err != nil {
			return updated, err
		}
	}
	updated.Instructions = slices.Clone(form.instructions.values)
	if options.Provider != "api" {
		updated.Merge = processing.MergeOptions{ModelID: options.MergeModelID, Prompt: options.MergePrompt, ReasoningEffort: options.MergeReasoning}
	}
	if err := readWhisperForm(form.whisper, &updated); err != nil {
		return updated, err
	}
	if form.audioAPI != nil && (updated.TranscriptionProvider() == config.ProviderAPI || strings.TrimSpace(form.audioAPI.model.Text) != "") {
		updated.AudioAPI = apiconfig.Audio{
			Connection: form.audioAPI.connection(), Model: strings.TrimSpace(form.audioAPI.model.Text),
			Language: strings.TrimSpace(form.audioAPI.language.Text),
		}
		if err := ensureAPIConnection(
			&updated, updated.AudioAPI.Connection, form.audioAPI.name.Text,
			form.audioAPI.textEnabled.Checked, true,
		); err != nil {
			return updated, err
		}
		if err := updated.AudioAPI.Validate(); err != nil {
			return updated, err
		}
	}
	updated.Microphone = nil
	updated.Capture.MicrophoneEndpoint = form.endpoints[uiSelected(form.microphone)]
	updated.Capture.PlaybackEndpoint = output.EndpointID
	if device >= 0 {
		updated.Microphone = &device
	}
	switch uiSelected(form.layout) {
	case stackedChoice:
		updated.Appearance.Layout = config.LayoutStacked
	case inlineChoice:
		updated.Appearance.Layout = config.LayoutInline
	case columnChoice:
		updated.Appearance.Layout = config.LayoutColumn
	default:
		return updated, errors.New("selecione a disposição dos boxes")
	}
	switch uiSelected(form.recordingSize) {
	case largeChoice:
		updated.Appearance.RecordingSize = config.RecordingLarge
	case mediumChoice:
		updated.Appearance.RecordingSize = config.RecordingMedium
	case smallChoice:
		updated.Appearance.RecordingSize = config.RecordingSmall
	default:
		return updated, errors.New("selecione o tamanho do box de gravação")
	}
	if err := updated.Validate(); err != nil {
		return updated, err
	}
	return updated, nil
}

func (d *desktop) persistPreferences(updated config.Settings, complete func(error)) {
	previous := cloneSettings(d.settings)
	d.settings = cloneSettings(updated)
	d.syncConcurrency(updated)
	epoch := d.preferenceEpoch.Add(1)
	d.refreshQualityIndicators(updated.Quality)
	d.syncPin()
	d.applyAppearance(previous.Appearance, updated.Appearance)
	d.syncCaptureOptions()
	d.syncPlaybackVolume()
	if previous.Merge != updated.Merge {
		d.cancelMerge("As opções da mesclagem mudaram. Clique em Mesclar novamente.")
	}
	d.operation(func() error {
		d.preferenceMu.Lock()
		defer d.preferenceMu.Unlock()
		if epoch != d.preferenceEpoch.Load() {
			return nil
		}
		pinChanged := previous.Pinned != updated.Pinned
		if pinChanged {
			if err := d.deps.setPinned(updated.Pinned); err != nil {
				return fmt.Errorf("não foi possível fixar a janela: %w", err)
			}
		}
		if err := config.Save(d.directory, updated); err != nil {
			if pinChanged {
				err = errors.Join(err, d.deps.setPinned(previous.Pinned))
			}
			return fmt.Errorf("não foi possível salvar as configurações: %w", err)
		}
		return nil
	}, func(err error) {
		if epoch != d.preferenceEpoch.Load() {
			complete(nil)
			return
		}
		if err != nil {
			d.settings = previous
			d.syncConcurrency(previous)
			d.refreshQualityIndicators(previous.Quality)
			d.syncPin()
			d.applyAppearance(updated.Appearance, previous.Appearance)
			d.syncCaptureOptions()
			d.syncPlaybackVolume()
		}
		complete(err)
	})
}

func (d *desktop) syncPin() {
	d.pinButton.Importance = widget.MediumImportance
	d.pinButton.setHint(uiText("Fixar janela"))
	if d.settings.Pinned {
		d.pinButton.Importance = widget.HighImportance
		d.pinButton.setHint(uiText("Desafixar janela"))
	}
	d.pinButton.Refresh()
}

func (d *desktop) togglePin() {
	updated := cloneSettings(d.settings)
	updated.Pinned = !updated.Pinned
	d.persistPreferences(updated, func(err error) {
		if err != nil {
			d.showError(err.Error(), "")
		}
	})
}

func (d *desktop) applyNativeWindow() {
	pinned := d.settings.Pinned
	dark := d.settings.Appearance.Theme != config.ThemeLight
	background := paletteFor(d.settings.Appearance.Theme).background
	header := d.windowHeader()
	epoch := d.preferenceEpoch.Load()
	d.operation(func() error {
		if err := waitContext(d.ctx, 120*time.Millisecond); err != nil {
			return err
		}
		d.preferenceMu.Lock()
		defer d.preferenceMu.Unlock()
		if epoch != d.preferenceEpoch.Load() {
			return nil
		}
		if d.deps.windowFrame != nil {
			if err := d.deps.windowFrame(dark, background, header); err != nil && !errors.Is(err, platform.ErrUnsupported) {
				slog.Debug("não foi possível integrar a barra de título", "error", err)
			}
		} else if d.deps.darkTitlebar != nil {
			if err := d.deps.darkTitlebar(dark); err != nil && !errors.Is(err, platform.ErrUnsupported) {
				// Title bar tint is cosmetic; pin and the rest of the desktop
				// remain available even on Windows versions without it.
				slog.Debug("não foi possível aplicar a barra de título escura", "error", err)
			}
		}
		return d.deps.setPinned(pinned)
	}, func(err error) {
		if err != nil && epoch == d.preferenceEpoch.Load() && !errors.Is(err, context.Canceled) {
			d.showError(fmt.Sprintf("Não foi possível configurar a janela: %v", err), "")
		}
	})
}

func (d *desktop) connectFromForm(form *settingsForm) {
	if d.settingsForm != form || form.loading || form.connect.Disabled() {
		return
	}
	updated := cloneSettings(d.settings)
	updated.Profile = strings.TrimSpace(form.profile.Text)
	if updated.Profile == "" || updated.Profile == automaticProfile {
		updated.Profile = form.defaultProfile
	}
	updated.Region = strings.TrimSpace(form.region.Text)
	if err := updated.Validate(); err != nil {
		form.connection.SetText(err.Error())
		return
	}
	form.connect.Disable()
	form.connection.SetText("Tentando autenticar com o perfil selecionado…")
	d.persistPreferences(updated, func(err error) {
		if err != nil {
			if d.settingsForm == form {
				form.connection.SetText(err.Error())
				form.connect.Enable()
			}
			return
		}
		d.checkAWSResult(func(err error) {
			if d.settingsForm != form {
				return
			}
			form.connect.Enable()
			if err != nil {
				form.connection.SetText(err.Error())
			} else {
				form.connection.SetText("AWS conectada com o perfil " + d.settings.Profile + ".")
			}
		})
	})
}

func (d *desktop) checkAWS() {
	if d.settings.TranscriptionProvider() != config.ProviderBedrock {
		d.authEpoch++
		if d.authCancel != nil {
			d.authCancel()
			d.authCancel = nil
		}
		message := "AWS opcional · texto pode usar AWS ou API"
		if d.settings.TranscriptionProvider() == config.ProviderAPI {
			message = "Transcrição por API · AWS opcional"
		}
		d.setAWSStatus(message, mutedColor)
		return
	}
	d.checkAWSResult(nil)
}

func (d *desktop) checkAWSResult(complete func(error)) {
	if d.deps.verify == nil {
		d.setAWSStatus("AWS Cloud", mutedColor)
		if complete != nil {
			complete(errors.New("o serviço de autenticação AWS não está disponível"))
		}
		return
	}
	d.authEpoch++
	epoch := d.authEpoch
	generation := d.generation
	doc := d.document
	if d.authCancel != nil {
		d.authCancel()
	}
	ctx, cancel := context.WithTimeout(d.ctx, 45*time.Second)
	d.authCancel = cancel
	settings := cloneSettings(d.settings)
	d.setAWSStatus("Verificando AWS…", mutedColor)
	d.operation(func() error {
		defer cancel()
		return d.deps.verify(ctx, settings)
	}, func(err error) {
		if epoch != d.authEpoch {
			if complete != nil {
				complete(context.Canceled)
			}
			return
		}
		d.authCancel = nil
		if complete != nil {
			complete(err)
		}
		if err == nil {
			d.setAWSStatus("AWS conectada", accentColor)
			return
		}
		d.setAWSStatus("AWS: verificar login", recordingColor)
		if d.settings.TranscriptionProvider() == config.ProviderBedrock && doc == d.document && generation == d.generation && !d.busy &&
			d.capture == captureIdle && !d.errorLabel.Visible() {
			d.errorLabel.SetText(shortMessage("AWS: " + err.Error()))
			d.errorLabel.Show()
			d.status.SetText("Verifique seu perfil AWS nas configurações.")
		}
	})
}

func (d *desktop) refreshQualityIndicators(quality string) {
	for _, doc := range d.documents {
		d.syncDocumentProvider(doc)
	}
}

// availableProfiles reads profile names without retrieving credentials or
// starting credential_process. The editable selector also accepts new names.
func availableProfiles() ([]string, error) {
	userHome, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("não foi possível localizar os perfis AWS: %w", err)
	}
	names := make(map[string]bool)
	var readError error
	for _, file := range []struct {
		name     string
		override string
		config   bool
	}{
		{name: "config", override: "AWS_CONFIG_FILE", config: true},
		{name: "credentials", override: "AWS_SHARED_CREDENTIALS_FILE"},
	} {
		path := os.Getenv(file.override)
		if path == "" {
			path = filepath.Join(userHome, ".aws", file.name)
		}
		if err := readProfiles(path, file.config, names); err != nil {
			readError = errors.Join(readError, err)
		}
	}
	profiles := make([]string, 0, len(names))
	for name := range names {
		profiles = append(profiles, name)
	}
	slices.Sort(profiles)
	return profiles, readError
}

func readProfiles(path string, configuration bool, names map[string]bool) (err error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("não foi possível listar os perfis AWS: %w", err)
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "[") || !strings.HasSuffix(line, "]") {
			continue
		}
		name := strings.TrimSpace(line[1 : len(line)-1])
		if configuration && name != "default" {
			if !strings.HasPrefix(name, "profile ") {
				continue
			}
			name = strings.TrimSpace(strings.TrimPrefix(name, "profile "))
		}
		if name != "" {
			names[name] = true
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("não foi possível ler os perfis AWS: %w", err)
	}
	return nil
}
