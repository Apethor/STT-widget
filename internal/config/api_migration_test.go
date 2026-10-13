package config

import (
	"path/filepath"
	"strings"
	"testing"

	"ai-talker/internal/apiconfig"
	"ai-talker/internal/processing"
	"ai-talker/internal/store"
)

func TestLoadMigratesDifferentLegacyEndpointsWithoutNameCollisions(t *testing.T) {
	directory := t.TempDir()
	settings := Default()
	for index, endpoint := range []string{"https://one.example/v1", "https://two.example/v1"} {
		options := processing.Default().WithLegacyMerge(settings.Merge)
		options.Provider, options.ModelID = "api", "manual-model"
		options.MergeModelID, options.MergeNeedsReview = "manual-merge", false
		options.API = apiconfig.Connection{Provider: apiconfig.Generic, Endpoint: endpoint}
		settings.TextProfiles = append(settings.TextProfiles, processing.Profile{
			ID: strings.Repeat(string(rune('a'+index)), 32), Name: "Perfil " + string(rune('A'+index)), Options: options,
		})
	}
	if err := store.WriteJSON(filepath.Join(directory, "settings.json"), settings); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(directory)
	if err != nil {
		t.Fatalf("a migração rejeitou perfis válidos com endpoints diferentes: %v", err)
	}
	if len(loaded.APIConnections) != 2 || loaded.APIConnections[0].Name == loaded.APIConnections[1].Name {
		t.Fatalf("a migração criou nomes duplicados: %+v", loaded.APIConnections)
	}
	for index, profile := range loaded.TextProfiles {
		expected := settings.TextProfiles[index].Options
		if profile.Options.API.ID == "" || profile.Options.API.Endpoint != expected.API.Endpoint ||
			profile.Options.ModelID != expected.ModelID {
			t.Fatalf("a migração perdeu a seleção ou as opções do perfil: %+v", profile)
		}
	}
}

func TestLoadKeepsCatalogIdentityWhenSavedConnectionEndpointChanges(t *testing.T) {
	directory := t.TempDir()
	settings := Default()
	connection := apiconfig.Connection{ID: strings.Repeat("a", 32), Provider: apiconfig.Generic, Endpoint: "https://old.example/v1"}
	settings.Processing.Provider, settings.Processing.ModelID = "api", "manual-model"
	settings.Processing.MergeModelID, settings.Processing.MergeNeedsReview = "manual-merge", false
	settings.Processing.API = connection
	settings.APIConnections = []apiconfig.SavedConnection{{
		ID: connection.ID, Name: "Conta da aula", Provider: apiconfig.Generic,
		Endpoint: "https://new.example/v1", Text: true,
	}}
	if err := store.WriteJSON(filepath.Join(directory, "settings.json"), settings); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(directory)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Processing.API.ID != connection.ID || loaded.Processing.API.Endpoint != "https://new.example/v1" ||
		loaded.Processing.ModelID != "manual-model" {
		t.Fatalf("a seleção ativa continuou no endpoint antigo: %+v", loaded.Processing)
	}
}

func TestLoadCorrectsFormerAzureAudioFlagWithoutDroppingTextConnection(t *testing.T) {
	directory := t.TempDir()
	settings := Default()
	connection := apiconfig.SavedConnection{
		ID: strings.Repeat("b", 32), Name: "Azure legado", Provider: apiconfig.Azure,
		Endpoint: "https://legacy.openai.azure.com/openai/v1", Text: true, Audio: true,
	}
	settings.APIConnections = []apiconfig.SavedConnection{connection}
	if err := store.WriteJSON(filepath.Join(directory, "settings.json"), settings); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(directory)
	if err != nil {
		t.Fatal("o catálogo legado permitido pela versão anterior não pôde ser carregado", err)
	}
	if len(loaded.APIConnections) != 1 || loaded.APIConnections[0].ID != connection.ID ||
		loaded.APIConnections[0].Endpoint != connection.Endpoint || !loaded.APIConnections[0].Text ||
		loaded.APIConnections[0].Audio {
		t.Fatal("a compatibilidade corrigida trocou a identidade/endpoint ou manteve Azure de texto no áudio")
	}
}
