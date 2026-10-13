package desktop

import (
	"errors"
	"strings"

	"ai-talker/internal/config"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

var transcribeLanguages = []struct{ name, code string }{
	{"Português · Brasil", "pt-BR"}, {"Português · Portugal", "pt-PT"},
	{"Automático · 5 idiomas", "auto"},
	{"Inglês", "en-US"}, {"Espanhol", "es-US"}, {"Francês", "fr-FR"},
	{"Alemão", "de-DE"}, {"Italiano", "it-IT"}, {"Japonês", "ja-JP"}, {"Chinês", "zh-CN"},
}

func cloudChoices(stream bool) []string {
	var names []string
	for _, model := range config.CloudModels() {
		if stream && model.Stream || !stream && model.Batch {
			names = append(names, model.Name)
		}
	}
	return names
}

func cloudChoiceID(name string) string {
	for _, model := range config.CloudModels() {
		if model.Name == name {
			return model.ID
		}
	}
	return ""
}

func cloudChoiceName(id string) string {
	model, _ := config.CloudModelByID(id)
	return model.Name
}

func (d *desktop) buildCloudSettings(form *settingsForm, settings config.Settings) fyne.CanvasObject {
	form.streamModel = widget.NewSelect(cloudChoices(true), nil)
	setUISelected(form.streamModel, cloudChoiceName(settings.StreamModelID()))
	form.quality.SetOptions(cloudChoices(false))
	setUISelected(form.quality, cloudChoiceName(settings.CloudModelID()))
	form.cloudLanguage = widget.NewSelect(nil, nil)
	for _, language := range transcribeLanguages {
		form.cloudLanguage.Options = append(form.cloudLanguage.Options, language.name)
		if language.code == settings.Cloud.TranscribeLanguage() {
			setUISelected(form.cloudLanguage, language.name)
		}
	}
	description := widget.NewLabel("")
	description.Wrapping = fyne.TextWrapWord
	streamDescription := widget.NewLabel("")
	streamDescription.Wrapping = fyne.TextWrapWord
	bucketHint := widget.NewLabel("O uso e o nome do bucket S3 ficam em Conexões → AWS. Se estiver desabilitado, escolha explicitamente a conexão direta ou configure o S3 antes de transcrever.")
	bucketHint.Wrapping = fyne.TextWrapWord
	form.cloudDirect = widget.NewButton(uiText("Usar Transcribe sem S3"), d.guard(func() {
		setUISelected(form.quality, cloudChoiceName(config.TranscribeStreaming))
	}))
	bucket := container.NewVBox(
		d.smallText(bucketHint), form.cloudDirect,
	)
	language := container.NewVBox(settingLabel(uiText("Idioma · Amazon Transcribe")), form.cloudLanguage)
	refresh := func() {
		batch, _ := config.CloudModelByID(cloudChoiceID(uiSelected(form.quality)))
		stream, _ := config.CloudModelByID(cloudChoiceID(uiSelected(form.streamModel)))
		description.SetText(batch.Description)
		streamDescription.SetText(stream.Description)
		bucket.Hide()
		if batch.Backend == config.CloudTranscribe {
			bucket.Show()
		}
		language.Hide()
		if batch.Backend == config.CloudTranscribe || batch.Backend == config.CloudStreaming || stream.Backend == config.CloudStreaming {
			language.Show()
		}
	}
	form.quality.OnChanged = func(string) { refresh() }
	form.streamModel.OnChanged = func(string) { refresh() }
	refresh()
	hint := widget.NewLabel("O botão Stream escolhe quando transcrever. Cada modo guarda seu próprio modelo; importar e retranscrever usam o modelo de gravações completas.")
	hint.Wrapping = fyne.TextWrapWord
	return container.NewVBox(
		settingLabel(uiText("Gravações completas · importar e retranscrever")), form.quality, d.smallText(description),
		settingLabel(uiText("Durante a gravação · Stream")), form.streamModel, d.smallText(streamDescription),
		d.smallText(hint), language, bucket,
	)
}

func readCloudSettings(form *settingsForm, settings *config.Settings) error {
	model := cloudChoiceID(uiSelected(form.quality))
	stream := cloudChoiceID(uiSelected(form.streamModel))
	if model == "" || stream == "" {
		return errors.New("selecione os modelos Cloud para gravação completa e Stream")
	}
	settings.Cloud.ModelID, settings.Cloud.StreamModelID = model, stream
	switch model {
	case config.VoxtralMini:
		settings.Quality = "fast"
	case config.VoxtralSmall:
		settings.Quality = "quality"
	}
	settings.Cloud.S3Bucket = strings.TrimSpace(form.cloudBucket.Text)
	useS3 := form.cloudUseBucket.Checked
	settings.Cloud.UseS3 = &useS3
	settings.Cloud.Language = ""
	for _, language := range transcribeLanguages {
		if uiSelected(form.cloudLanguage) == language.name {
			settings.Cloud.Language = language.code
		}
	}
	if settings.Cloud.Language == "" {
		return errors.New("selecione um idioma para o Amazon Transcribe")
	}
	return settings.Cloud.Validate()
}
