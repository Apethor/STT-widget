package config

import (
	"fmt"
	"strings"

	"ai-talker/internal/apiconfig"
)

func migrateAPIConnections(settings *Settings) {
	if settings == nil {
		return
	}
	connections := append([]apiconfig.SavedConnection(nil), settings.APIConnections...)
	for index := range connections {
		if connections[index].Provider == apiconfig.Azure {
			// Earlier catalogs let a text-only Azure connection advertise
			// Audio. Keep its identity/key and correct only that flag.
			connections[index].Text, connections[index].Audio = true, false
		}
	}
	ensure := func(selected apiconfig.Connection, name string, text, audio bool) apiconfig.Connection {
		if selected.Provider == "" {
			return selected
		}
		for _, saved := range connections {
			connection := saved.Connection()
			if selected.ID != "" && selected.ID == saved.ID ||
				selected.ID == "" && connection.Provider == selected.Provider &&
					connection.BaseURL() == selected.BaseURL() {
				if text && !saved.Text && countConnections(connections, true) >= apiconfig.MaxConnectionsPerFlow ||
					audio && !saved.Audio && countConnections(connections, false) >= apiconfig.MaxConnectionsPerFlow {
					return selected
				}
				markConnectionFlow(connections, saved.ID, text, audio)
				// The catalog owns endpoint/provider metadata; an active flow may
				// still contain the endpoint from before the connection was edited.
				return saved.Connection()
			}
		}
		if selected.ID == "" {
			selected.ID = apiconfig.LegacyConnectionID(selected)
		}
		if text && countConnections(connections, true) >= apiconfig.MaxConnectionsPerFlow ||
			audio && countConnections(connections, false) >= apiconfig.MaxConnectionsPerFlow {
			selected.ID = ""
			return selected
		}
		flowName := "Áudio"
		if text && !audio {
			flowName = "Texto"
		}
		if strings.TrimSpace(name) == "" {
			name = flowName + " · " + apiProviderLabel(selected.Provider)
		}
		name = availableConnectionName(connections, name)
		connections = append(connections, apiconfig.SavedConnection{
			ID: selected.ID, Name: name, Provider: selected.Provider, Endpoint: selected.Endpoint,
			Text: text, Audio: audio,
		})
		return selected
	}

	settings.AudioAPI.Connection = ensure(settings.AudioAPI.Connection, "", false, true)
	settings.Processing.API = ensure(settings.Processing.API, "", true, false)
	for index := range settings.TextProfiles {
		settings.TextProfiles[index].Options.API = ensure(
			settings.TextProfiles[index].Options.API, settings.TextProfiles[index].Name, true, false,
		)
	}
	settings.APIConnections = connections
}

func availableConnectionName(connections []apiconfig.SavedConnection, preferred string) string {
	baseRunes := []rune(strings.TrimSpace(preferred))
	base := string(baseRunes[:min(len(baseRunes), 80)])
	for number := 1; ; number++ {
		name := base
		if number > 1 {
			suffix := fmt.Sprintf(" (%d)", number)
			runes := []rune(base)
			name = string(runes[:min(len(runes), 80-len(suffix))]) + suffix
		}
		duplicate := false
		for _, saved := range connections {
			if strings.EqualFold(strings.TrimSpace(saved.Name), name) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			return name
		}
	}
}

func apiProviderLabel(provider string) string {
	switch provider {
	case apiconfig.OpenRouter:
		return "OpenRouter"
	case apiconfig.OpenAI:
		return "OpenAI"
	case apiconfig.Azure:
		return "Azure OpenAI / Foundry"
	case apiconfig.AzureSpeech:
		return "Azure Speech · MAI-Transcribe-2"
	default:
		return "API compatível"
	}
}

func countConnections(connections []apiconfig.SavedConnection, text bool) int {
	count := 0
	for _, connection := range connections {
		if text && connection.Text || !text && connection.Audio {
			count++
		}
	}
	return count
}

func markConnectionFlow(connections []apiconfig.SavedConnection, id string, text, audio bool) {
	for index := range connections {
		if connections[index].ID == id {
			connections[index].Text = connections[index].Text || text
			connections[index].Audio = connections[index].Audio || audio
			return
		}
	}
}
