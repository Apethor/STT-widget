package desktop

import (
	"ai-talker/internal/config"
	"ai-talker/internal/whisper"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const (
	bedrockProviderChoice = "AWS Cloud"
	whisperProviderChoice = "Whisper local"
	apiProviderChoice     = "API de áudio"
)

func (d *desktop) chooseTranscriptionProvider() {
	if d.documentAudioMode(d.document) == voiceModeLive {
		d.openSettingsSection("Áudio")
		return
	}
	items := make([]*fyne.MenuItem, 0, 2)
	for _, choice := range []struct{ name, provider string }{
		{bedrockProviderChoice, config.ProviderBedrock},
		{whisperProviderChoice, config.ProviderWhisper},
		{apiProviderChoice, config.ProviderAPI},
	} {
		item := fyne.NewMenuItem(uiText(choice.name), d.guard(func() { d.setTranscriptionProvider(choice.provider) }))
		item.Checked = choice.provider == d.settings.TranscriptionProvider()
		items = append(items, item)
	}
	anchor := d.providerButton
	menu := widget.NewPopUpMenu(fyne.NewMenu(uiText("Transcrever com"), items...), d.window.Canvas())
	menu.ShowAtRelativePosition(fyne.NewPos(0, anchor.Size().Height), anchor)
}

func (d *desktop) chooseTranscriptionModel() {
	if d.documentAudioMode(d.document) == voiceModeLive {
		var items []*fyne.MenuItem
		for _, id := range []string{config.NovaSonic25, config.NovaSonic} {
			model, _ := config.CloudModelByID(id)
			item := fyne.NewMenuItem(uiText(cloudMenuName(model)), d.guard(func() {
				d.changeConversationPreference(d.document, func(settings *config.Settings) {
					settings.Conversation.ModelID = id
				}, true)
			}))
			item.Checked = id == d.settings.Conversation.Model()
			items = append(items, item)
		}
		popup := widget.NewPopUpMenu(fyne.NewMenu(uiText("Modelo da conversa ao vivo"), items...), d.window.Canvas())
		popup.ShowAtRelativePosition(fyne.NewPos(0, d.qualityBars.Size().Height), d.qualityBars)
		return
	}
	if d.settings.TranscriptionProvider() == config.ProviderAPI {
		d.openAudioAPISettings()
		return
	}
	if d.settings.TranscriptionProvider() != config.ProviderWhisper {
		d.chooseCloudModel()
		return
	}
	var items []*fyne.MenuItem
	for _, model := range whisper.Models() {
		item := fyne.NewMenuItem(uiText(whisperModelChoice(model)), d.guard(func() {
			updated := cloneSettings(d.settings)
			updated.Whisper.Model, updated.Whisper.ModelPath = model.ID, ""
			d.persistPreferences(updated, func(err error) {
				if err != nil {
					d.showError(err.Error(), "")
				}
			})
		}))
		item.Checked = d.settings.Whisper.Model == model.ID
		items = append(items, item)
	}
	items = append(items, fyne.NewMenuItem(uiText("Arquivo local / configurar…"), d.guard(d.openWhisperSettings)))
	menu := widget.NewPopUpMenu(fyne.NewMenu(uiText("Modelo Whisper"), items...), d.window.Canvas())
	menu.ShowAtRelativePosition(fyne.NewPos(0, d.qualityBars.Size().Height), d.qualityBars)
}

func (d *desktop) openWhisperSettings() {
	d.openSettingsSection("Whisper")
}

func (d *desktop) cloudModelsMenu(stream bool) *fyne.Menu {
	selected := d.settings.CloudModelID()
	if stream {
		selected = d.settings.StreamModelID()
	}
	var items []*fyne.MenuItem
	for _, model := range config.CloudModels() {
		if stream && !model.Stream || !stream && !model.Batch {
			continue
		}
		item := fyne.NewMenuItem(uiText(cloudMenuName(model)), d.guard(func() { d.setCloudModel(model.ID, stream) }))
		item.Checked = model.ID == selected
		items = append(items, item)
	}
	return fyne.NewMenu(uiText("Modelo de transcrição"), items...)
}

// Keep both levels readable together even in the narrow column. Full engine
// names and descriptions remain in the provider hint and Cloud settings.
func cloudMenuName(model config.CloudModel) string {
	switch model.ID {
	case config.TranscribeBatch:
		return "Transcribe · S3"
	case config.TranscribeStreaming:
		return "Transcribe · direto"
	case config.NovaSonic:
		return "Nova 2 Sonic (exp.)"
	case config.NovaSonic25:
		return "Nova 2.5 Sonic (exp.)"
	default:
		return model.Name
	}
}

func (d *desktop) chooseCloudModel() {
	menu := widget.NewPopUpMenu(d.cloudModelsMenu(d.documentAudioMode(d.document) == voiceModeStream), d.window.Canvas())
	menu.ShowAtRelativePosition(fyne.NewPos(0, d.qualityBars.Size().Height), d.qualityBars)
}

func (d *desktop) setCloudModel(id string, stream bool) {
	model, ok := config.CloudModelByID(id)
	if !ok || stream && !model.Stream || !stream && !model.Batch {
		return
	}
	if id == config.TranscribeBatch && !d.settings.Cloud.S3Enabled() {
		d.openTranscribeSetup(nil)
		return
	}
	updated := cloneSettings(d.settings)
	updated.Provider = config.ProviderBedrock
	if stream {
		updated.Cloud.StreamModelID = id
	} else {
		updated.Cloud.ModelID = id
		if id == config.VoxtralMini {
			updated.Quality = "fast"
		} else if id == config.VoxtralSmall {
			updated.Quality = "quality"
		}
	}
	d.persistPreferences(updated, func(err error) {
		if err != nil {
			d.showError(err.Error(), "")
		}
	})
}

func (d *desktop) setTranscriptionProvider(provider string) {
	if provider == config.ProviderAPI && d.settings.AudioAPI.Validate() != nil {
		d.openAudioAPISettings()
		return
	}
	updated := cloneSettings(d.settings)
	updated.Provider = provider
	d.persistPreferences(updated, func(err error) {
		if err != nil {
			d.showError(err.Error(), "")
		} else {
			d.checkAWS()
		}
	})
}

func (d *desktop) syncDocumentProvider(doc *document) {
	if d.documentAudioMode(doc) == voiceModeLive {
		doc.providerButton.SetIcon(cloudIcon)
		doc.providerButton.setHint("AWS Bedrock · " + d.settings.Conversation.Model() + " · " + uiText("Conversa ao vivo"))
		doc.qualityBars.levels = 3
		doc.qualityBars.hint.text = d.settings.Conversation.Model() + " · " + uiText("Modelo da conversa ao vivo")
		doc.qualityBars.Refresh()
		return
	}
	if d.settings.TranscriptionProvider() == config.ProviderAPI {
		doc.providerButton.SetIcon(cloudIcon)
		doc.providerButton.setHint(uiText("API de áudio · ") + d.settings.AudioAPI.Model + uiText(" · clicar para escolher o motor"))
		doc.qualityBars.levels = 3
		doc.qualityBars.hint.text = "API · " + d.settings.AudioAPI.Model + uiText(" · clicar para configurar o modelo")
		doc.qualityBars.Refresh()
		return
	}
	if d.settings.TranscriptionProvider() == config.ProviderWhisper {
		name := d.settings.Whisper.Model
		if model, ok := whisper.ModelByID(name); ok {
			name = model.Name
		}
		doc.providerButton.SetIcon(theme.ComputerIcon())
		doc.providerButton.setHint(uiText("Whisper local · ") + name + uiText(" · clicar para escolher o motor"))
		doc.qualityBars.setWhisperModel(d.settings.Whisper.Model)
		return
	}
	doc.providerButton.SetIcon(cloudIcon)
	id := d.settings.CloudModelID()
	stream := d.documentAudioMode(doc) == voiceModeStream
	if stream {
		id = d.settings.StreamModelID()
	}
	model, _ := config.CloudModelByID(id)
	engine := "AWS Bedrock"
	if model.Backend == config.CloudTranscribe || model.Backend == config.CloudStreaming {
		engine = "Amazon Transcribe"
	}
	doc.providerButton.setHint(engine + " · " + model.Name + uiText(" · clicar para escolher o motor"))
	doc.qualityBars.setCloudModel(model, stream)
}
