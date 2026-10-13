package desktop

import (
	"errors"
	"strings"

	"ai-talker/internal/apiconfig"
	"ai-talker/internal/config"
)

func apiConnectionLabel(connection apiconfig.SavedConnection) string {
	return connection.Name + " · " + apiProviderName(connection.Provider)
}

func apiProviderName(provider string) string {
	switch provider {
	case apiconfig.OpenRouter:
		return openRouterChoice
	case apiconfig.OpenAI:
		return openAIChoice
	case apiconfig.Azure:
		return azureAPIChoice
	case apiconfig.AzureSpeech:
		return azureSpeechChoice
	default:
		return genericAPIChoice
	}
}

func ensureAPIConnection(settings *config.Settings, connection apiconfig.Connection, name string, text, audio bool) error {
	if settings == nil || connection.ID == "" || !text && !audio {
		return errors.New("selecione uma conexão de API e pelo menos um fluxo")
	}
	for index := range settings.APIConnections {
		saved := &settings.APIConnections[index]
		if saved.ID != connection.ID {
			continue
		}
		saved.Name = strings.TrimSpace(name)
		saved.Provider, saved.Endpoint = connection.Provider, connection.Endpoint
		saved.Text, saved.Audio = saved.Text || text, saved.Audio || audio
		return nil
	}
	textCount, audioCount := 0, 0
	for _, saved := range settings.APIConnections {
		if saved.Text {
			textCount++
		}
		if saved.Audio {
			audioCount++
		}
	}
	if text && textCount >= apiconfig.MaxConnectionsPerFlow ||
		audio && audioCount >= apiconfig.MaxConnectionsPerFlow {
		return errors.New("limite de dez conexões para este fluxo; remova uma ou escolha uma já salva")
	}
	settings.APIConnections = append(settings.APIConnections, apiconfig.SavedConnection{
		ID: connection.ID, Name: strings.TrimSpace(name), Provider: connection.Provider, Endpoint: connection.Endpoint,
		Text: text, Audio: audio,
	})
	return nil
}

func updateConnectionSelections(settings *config.Settings, connection apiconfig.Connection) {
	if settings.AudioAPI.ID == connection.ID {
		settings.AudioAPI.Connection = connection
	}
	if settings.Processing.API.ID == connection.ID {
		settings.Processing.API = connection
	}
	for index := range settings.TextProfiles {
		if settings.TextProfiles[index].Options.API.ID == connection.ID {
			settings.TextProfiles[index].Options.API = connection
		}
	}
}

func (d *desktop) apiConnectionInUse(id string) bool {
	if id == "" {
		return false
	}
	if d.settings.AudioAPI.Connection.ID == id ||
		d.settings.Processing.API.ID == id {
		return true
	}
	for _, document := range d.documents {
		if document.processOptions.API.ID == id {
			return true
		}
	}
	for _, profile := range d.settings.TextProfiles {
		if profile.Options.API.ID == id {
			return true
		}
	}
	return false
}

func (d *desktop) apiConnectionUsedByText(id string) bool {
	if id == "" {
		return false
	}
	if saved, ok := d.apiSavedConnection(id); ok && saved.Text {
		return true
	}
	if d.settings.Processing.API.ID == id {
		return true
	}
	for _, document := range d.documents {
		if document.processOptions.API.ID == id {
			return true
		}
	}
	for _, profile := range d.settings.TextProfiles {
		if profile.Options.API.ID == id {
			return true
		}
	}
	return false
}
