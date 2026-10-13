package desktop

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"ai-talker/internal/config"
	"ai-talker/internal/platform"
	"ai-talker/internal/whisper"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const customWhisperChoice = "Arquivo local (GGML)"

type whisperForm struct {
	provider       *widget.Select
	model          *widget.Select
	modelSource    *widget.Label
	downloadSource *widget.RadioGroup
	language       *widget.Select
	threads        *widget.Entry
	beam           *widget.Select
	binary         *widget.Entry
	modelPath      *widget.Entry
	engineDownload *widget.Button
	modelDownload  *widget.Button
	testButton     *widget.Button
	cancelButton   *widget.Button
	status         *widget.Label
	progress       *widget.ProgressBar
	busy           bool
	cancel         context.CancelFunc
}

var whisperLanguages = []struct{ name, code string }{
	{"Automático", "auto"}, {"Português", "pt"}, {"Inglês", "en"}, {"Espanhol", "es"},
	{"Francês", "fr"}, {"Alemão", "de"}, {"Italiano", "it"}, {"Japonês", "ja"}, {"Chinês", "zh"},
}

func whisperModelChoice(model whisper.Model) string { return model.Name + " · " + model.Disk }

func (d *desktop) buildWhisperSettings(form *settingsForm, settings config.Settings) fyne.CanvasObject {
	local := &whisperForm{
		provider: widget.NewSelect([]string{bedrockProviderChoice, whisperProviderChoice, apiProviderChoice}, nil),
		threads:  widget.NewEntry(), binary: widget.NewEntry(), modelPath: widget.NewEntry(),
		status:      widget.NewLabel("Tiny Q5 e Base Q5 usam menos memória e podem ser baixados pelo GitHub."),
		modelSource: widget.NewLabel(""),
		progress:    widget.NewProgressBar(),
	}
	form.whisper = local
	local.downloadSource = widget.NewRadioGroup([]string{"GitHub", "Hugging Face"}, nil)
	local.downloadSource.Required = true
	local.downloadSource.SetSelected("GitHub")
	if settings.WhisperDownloadSource == whisper.SourceHuggingFace {
		local.downloadSource.SetSelected("Hugging Face")
	}
	local.status.Wrapping = fyne.TextWrapWord
	local.modelSource.Wrapping = fyne.TextWrapWord
	local.progress.Hide()
	setUISelected(local.provider, bedrockProviderChoice)
	if settings.TranscriptionProvider() == config.ProviderWhisper {
		setUISelected(local.provider, whisperProviderChoice)
	} else if settings.TranscriptionProvider() == config.ProviderAPI {
		setUISelected(local.provider, apiProviderChoice)
	}
	local.provider.OnChanged = func(string) {
		setEnabled(form.quality, uiSelected(local.provider) == bedrockProviderChoice)
	}
	setEnabled(form.quality, uiSelected(local.provider) == bedrockProviderChoice)
	choices := make([]string, 0, len(whisper.Models())+1)
	selected := customWhisperChoice
	for _, model := range whisper.Models() {
		choice := whisperModelChoice(model)
		choices = append(choices, choice)
		if model.ID == settings.Whisper.Model {
			selected = choice
		}
	}
	choices = append(choices, customWhisperChoice)
	local.model = widget.NewSelect(choices, nil)
	setUISelected(local.model, selected)
	updateModelSource := func() {
		local.modelSource.SetText("Selecione um arquivo GGML já baixado.")
		for _, model := range whisper.Models() {
			if whisperModelChoice(model) == uiSelected(local.model) {
				local.modelSource.SetText(model.DownloadDescriptionFrom(whisperSource(local)))
				break
			}
		}
	}
	updateModelSource()
	local.model.OnChanged = func(string) {
		local.modelPath.SetText("")
		updateModelSource()
		syncWhisperDownload(local)
	}
	local.downloadSource.OnChanged = func(string) { updateModelSource(); syncWhisperDownload(local) }
	names := make([]string, 0, len(whisperLanguages))
	selectedLanguage := "Automático"
	for _, language := range whisperLanguages {
		names = append(names, language.name)
		if language.code == settings.Whisper.Language {
			selectedLanguage = language.name
		}
	}
	local.language = widget.NewSelect(names, nil)
	setUISelected(local.language, selectedLanguage)
	local.threads.SetText(strconv.Itoa(settings.Whisper.Threads))
	local.binary.SetText(settings.Whisper.BinaryPath)
	local.binary.SetPlaceHolder(uiText("Automático após baixar o motor"))
	local.modelPath.SetText(settings.Whisper.ModelPath)
	local.modelPath.SetPlaceHolder(uiText("Automático após baixar o modelo"))
	local.beam = widget.NewSelect([]string{"Rápido (1)", "Equilibrado (5)", "Mais alternativas (10)"}, nil)
	setUISelected(local.beam, "Equilibrado (5)")
	if settings.Whisper.BeamSize == 1 {
		setUISelected(local.beam, "Rápido (1)")
	} else if settings.Whisper.BeamSize != 5 {
		local.beam.Options = append(local.beam.Options, fmt.Sprintf("Personalizado (%d)", settings.Whisper.BeamSize))
		setUISelected(local.beam, local.beam.Options[len(local.beam.Options)-1])
	}
	local.engineDownload = widget.NewButton(uiText("Baixar motor Whisper"), d.guard(func() { d.downloadWhisper(form, true) }))
	local.modelDownload = widget.NewButton(uiText("Baixar modelo selecionado"), d.guard(func() { d.downloadWhisper(form, false) }))
	local.testButton = widget.NewButtonWithIcon(uiText("Testar motor"), theme.ConfirmIcon(), d.guard(func() { d.testWhisper(form) }))
	syncWhisperDownload(local)
	local.cancelButton = widget.NewButton(uiText("Cancelar download"), d.guard(func() {
		if local.cancel != nil {
			local.cancel()
			local.status.SetText("Cancelando…")
		}
	}))
	local.cancelButton.Hide()
	explanation := widget.NewLabel("Transcrição local usa a CPU deste computador e funciona sem AWS após instalar o motor e o modelo. O processamento opcional de texto usa o Bedrock ou a API escolhida na seção LLM.")
	explanation.Wrapping = fyne.TextWrapWord
	threadsHint := widget.NewLabel("Threads: 0 escolhe automaticamente até 8. Modelos maiores usam mais memória e tempo.")
	threadsHint.Wrapping = fyne.TextWrapWord
	testHint := widget.NewLabel("O teste transcreve uma amostra curta em inglês com o modelo selecionado. Não usa seu microfone nem a AWS.")
	testHint.Wrapping = fyne.TextWrapWord
	return container.NewVBox(
		explanation,
		settingLabel(uiText("Modelo local")), local.model,
		settingLabel(uiText("Fonte do download do modelo")), local.downloadSource,
		d.smallText(local.modelSource), local.modelDownload,
		settingLabel(uiText("Idioma da fala")), local.language,
		settingLabel(uiText("Uso de CPU · threads")), local.threads, d.smallText(threadsHint),
		settingLabel(uiText("Busca de alternativas")), local.beam,
		settingLabel(uiText("Motor local · whisper-cli.exe")),
		container.NewBorder(nil, nil, nil, d.iconButton("Selecionar whisper-cli.exe", theme.FolderOpenIcon(), func() {
			d.chooseWhisperFile(form, true)
		}), local.binary),
		local.engineDownload,
		settingLabel(uiText("Arquivo do modelo · GGML")),
		container.NewBorder(nil, nil, nil, d.iconButton("Selecionar modelo GGML", theme.FolderOpenIcon(), func() {
			d.chooseWhisperFile(form, false)
		}), local.modelPath),
		local.testButton, d.smallText(testHint),
		d.smallText(local.status), local.progress, local.cancelButton,
	)
}

func readWhisperForm(local *whisperForm, settings *config.Settings) error {
	settings.WhisperDownloadSource = whisperSource(local)
	if local.busy {
		return errors.New("aguarde ou cancele a operação do Whisper antes de salvar")
	}
	settings.Provider = config.ProviderBedrock
	if uiSelected(local.provider) == whisperProviderChoice {
		settings.Provider = config.ProviderWhisper
	} else if uiSelected(local.provider) == apiProviderChoice {
		settings.Provider = config.ProviderAPI
	}
	threads, err := strconv.Atoi(strings.TrimSpace(local.threads.Text))
	if err != nil {
		return errors.New("informe o número de threads do Whisper")
	}
	beam := 5
	start, end := strings.Index(uiSelected(local.beam), "("), strings.Index(uiSelected(local.beam), ")")
	if start >= 0 && end > start {
		beam, err = strconv.Atoi(uiSelected(local.beam)[start+1 : end])
	}
	if err != nil {
		return errors.New("selecione a busca de alternativas do Whisper")
	}
	model := "custom"
	for _, candidate := range whisper.Models() {
		if whisperModelChoice(candidate) == uiSelected(local.model) {
			model = candidate.ID
			break
		}
	}
	language := ""
	for _, candidate := range whisperLanguages {
		if candidate.name == uiSelected(local.language) {
			language = candidate.code
			break
		}
	}
	settings.Whisper = config.Whisper{
		Model: model, Language: language, Threads: threads, BeamSize: beam,
		BinaryPath: strings.TrimSpace(local.binary.Text), ModelPath: strings.TrimSpace(local.modelPath.Text),
	}
	return settings.Whisper.Validate()
}

func (d *desktop) chooseWhisperFile(form *settingsForm, engine bool) {
	if d.settingsForm != form || form.whisper.busy {
		return
	}
	if d.deps.chooseFile != nil {
		filter := platform.FileFilter{Name: "Modelo Whisper GGML", Pattern: "*.bin"}
		title := "Selecionar modelo Whisper"
		if engine {
			filter, title = platform.FileFilter{Name: "Motor Whisper", Pattern: "*.exe"}, "Selecionar whisper-cli.exe"
		}
		d.selectFile(platform.FileDialogOptions{
			Title: title, NewestFirst: true, Filters: []platform.FileFilter{filter},
		}, func(path string, err error) {
			if d.settingsForm != form || form.whisper.busy {
				return
			}
			if err != nil {
				form.whisper.status.SetText("Não foi possível selecionar o arquivo: " + err.Error())
			} else if path != "" {
				if engine {
					form.whisper.binary.SetText(path)
				} else {
					setUISelected(form.whisper.model, customWhisperChoice)
					form.whisper.modelPath.SetText(path)
				}
			}
		})
		return
	}
	selection := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
		d.guard(func() {
			if reader == nil || err != nil {
				if err != nil {
					form.whisper.status.SetText("Não foi possível selecionar o arquivo.")
				}
				return
			}
			path, pathErr := localPath(reader.URI())
			d.operation(reader.Close, func(closeErr error) {
				if d.settingsForm != form || form.whisper.busy {
					return
				}
				if errors.Join(pathErr, closeErr) != nil {
					form.whisper.status.SetText("Não foi possível abrir o arquivo selecionado.")
				} else if engine {
					form.whisper.binary.SetText(path)
				} else {
					setUISelected(form.whisper.model, customWhisperChoice)
					form.whisper.modelPath.SetText(path)
				}
			})
		})()
	}, d.window)
	extensions := []string{".bin"}
	if engine {
		extensions = []string{".exe"}
	}
	selection.SetFilter(storage.NewExtensionFileFilter(extensions))
	selection.Show()
	selection.Resize(dialogSize(d.window))
}

func (d *desktop) downloadWhisper(form *settingsForm, engine bool) {
	if d.settingsForm != form || form.whisper.busy {
		return
	}
	local := form.whisper
	settings := cloneSettings(d.settings)
	// A custom model is not a downloadable catalog entry. Reading the form
	// validates paths and numeric options before starting a transfer.
	if err := readWhisperForm(local, &settings); err != nil && !(engine && uiSelected(local.model) == customWhisperChoice) {
		local.status.SetText(err.Error())
		return
	}
	if (engine && d.deps.downloadWhisperEngine == nil) || (!engine && d.deps.downloadWhisperModel == nil) {
		local.status.SetText("O download Whisper não está disponível nesta instalação.")
		return
	}
	ctx, cancel := context.WithTimeout(d.ctx, time.Hour)
	local.cancel, local.busy = cancel, true
	local.engineDownload.Disable()
	local.modelDownload.Disable()
	local.testButton.Disable()
	local.model.Disable()
	local.downloadSource.Disable()
	form.save.Disable()
	local.cancelButton.Show()
	local.cancelButton.SetText("Cancelar download")
	local.progress.SetValue(0)
	local.progress.Show()
	setWhisperStatus(local, "Preparando o download…", widget.MediumImportance)
	update := func(progress whisper.DownloadProgress) {
		d.post(func() {
			if d.settingsForm != form || !local.busy || ctx.Err() != nil {
				return
			}
			message := progress.Message
			if progress.Total > 1 {
				message = fmt.Sprintf("%s %.1f / %.1f MiB", message, float64(progress.Bytes)/(1<<20), float64(progress.Total)/(1<<20))
				local.progress.SetValue(min(1, float64(progress.Bytes)/float64(progress.Total)))
			} else if progress.Total == 1 {
				local.progress.SetValue(1)
			}
			local.status.SetText(message)
		})
	}
	var path string
	d.operation(func() error {
		defer cancel()
		var err error
		if engine {
			path, err = d.deps.downloadWhisperEngine(ctx, d.directory, update)
		} else {
			path, err = d.deps.downloadWhisperModel(ctx, d.directory, settings.Whisper.Model, settings.WhisperDownloadSource, update)
		}
		return err
	}, func(err error) {
		if d.settingsForm != form {
			return
		}
		local.busy, local.cancel = false, nil
		local.engineDownload.Enable()
		local.testButton.Enable()
		syncWhisperDownload(local)
		local.model.Enable()
		local.downloadSource.Enable()
		local.cancelButton.Hide()
		if !form.loading {
			form.save.Enable()
		}
		switch {
		case errors.Is(err, context.Canceled):
			local.status.SetText("Download cancelado. Você pode tentar novamente.")
		case err != nil:
			setWhisperStatus(local, shortMessage(err.Error()), widget.DangerImportance)
		case engine:
			local.binary.SetText(path)
			local.status.SetText("Motor Whisper instalado. Salve as configurações para usar.")
			local.progress.SetValue(1)
		default:
			local.modelPath.SetText(path)
			local.status.SetText("Modelo instalado. Salve as configurações para usar.")
			local.progress.SetValue(1)
		}
	})
}

func setWhisperStatus(local *whisperForm, text string, importance widget.Importance) {
	local.status.Importance = importance
	local.status.SetText(text)
	local.status.Refresh()
}

func whisperSource(local *whisperForm) string {
	if local.downloadSource != nil && local.downloadSource.Selected == "Hugging Face" {
		return whisper.SourceHuggingFace
	}
	return whisper.SourceGitHub
}

func syncWhisperDownload(local *whisperForm) {
	if local.modelDownload == nil {
		return
	}
	available := false
	for _, model := range whisper.Models() {
		if whisperModelChoice(model) == uiSelected(local.model) {
			_, _, _, err := model.DownloadFrom(whisperSource(local))
			available = err == nil
		}
	}
	setEnabled(local.modelDownload, available && !local.busy)
}

func (d *desktop) testWhisper(form *settingsForm) {
	if d.settingsForm != form || form.whisper.busy {
		return
	}
	local := form.whisper
	settings := cloneSettings(d.settings)
	if err := readWhisperForm(local, &settings); err != nil {
		setWhisperStatus(local, "Teste falhou: "+err.Error(), widget.DangerImportance)
		return
	}
	if d.deps.testWhisper == nil {
		setWhisperStatus(local, "O teste Whisper não está disponível nesta instalação.", widget.DangerImportance)
		return
	}
	ctx, cancel := context.WithTimeout(d.ctx, 3*time.Minute)
	local.cancel, local.busy = cancel, true
	local.engineDownload.Disable()
	local.modelDownload.Disable()
	local.testButton.Disable()
	local.model.Disable()
	local.language.Disable()
	local.threads.Disable()
	local.beam.Disable()
	local.binary.Disable()
	local.modelPath.Disable()
	form.save.Disable()
	local.progress.Hide()
	local.cancelButton.SetText("Cancelar teste")
	local.cancelButton.Show()
	setWhisperStatus(local, "Verificando os arquivos e transcrevendo a amostra…", widget.MediumImportance)
	var result whisper.CheckResult
	d.operation(func() error {
		defer cancel()
		var err error
		result, err = d.deps.testWhisper(ctx, d.directory, settings)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}, func(err error) {
		if d.settingsForm != form {
			return
		}
		local.cancel, local.busy = nil, false
		local.engineDownload.Enable()
		syncWhisperDownload(local)
		local.testButton.Enable()
		local.model.Enable()
		local.language.Enable()
		local.threads.Enable()
		local.beam.Enable()
		local.binary.Enable()
		local.modelPath.Enable()
		local.cancelButton.Hide()
		if !form.loading {
			form.save.Enable()
		}
		switch {
		case errors.Is(err, context.Canceled):
			setWhisperStatus(local, "Teste cancelado. Você pode tentar novamente.", widget.MediumImportance)
		case errors.Is(err, context.DeadlineExceeded):
			setWhisperStatus(local, "Teste falhou: limite de 3 minutos atingido. Tente um modelo menor ou confira o uso de memória.", widget.DangerImportance)
		case err != nil:
			setWhisperStatus(local, "Teste falhou: "+err.Error(), widget.DangerImportance)
		default:
			setWhisperStatus(local, fmt.Sprintf("Teste aprovado em %.1f s. O motor reconheceu a amostra: %s",
				result.ElapsedSeconds, result.Text), widget.SuccessImportance)
		}
	})
}
