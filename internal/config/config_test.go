package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ai-talker/internal/apiconfig"
	"ai-talker/internal/processing"
	"ai-talker/internal/store"
)

func TestLoadPythonPreferences(t *testing.T) {
	directory := t.TempDir()
	payload := `{"profile":"old-profile","region":"us-west-2","quality":"fast",` +
		`"chunk_seconds":60,"overlap_seconds":1.5,"glossary":"TypeScript, AWS",` +
		`"microphone":null,"pinned":true,"future_setting":123}`
	path := filepath.Join(directory, "settings.json")
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	settings, err := Load(directory)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Profile != "old-profile" || settings.Region != "us-west-2" || settings.ChunkSeconds != 60 {
		t.Fatalf("Python preferences were not preserved: %+v", settings)
	}
	if settings.ModelID() != "mistral.voxtral-mini-3b-2507" || !settings.Pinned || settings.Device() != -1 {
		t.Fatalf("loaded behavior differs from Python: %+v", settings)
	}
	unchanged, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(unchanged) != payload {
		t.Fatal("loading preferences rewrote the user's file")
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Settings)
	}{
		{name: "unknown quality", change: func(s *Settings) { s.Quality = "unknown" }},
		{name: "short chunk", change: func(s *Settings) { s.ChunkSeconds = 14 }},
		{name: "long chunk", change: func(s *Settings) { s.ChunkSeconds = 301 }},
		{name: "negative overlap", change: func(s *Settings) { s.OverlapSeconds = -1 }},
		{name: "blank region", change: func(s *Settings) { s.Region = " " }},
		{name: "long glossary", change: func(s *Settings) { s.Glossary = strings.Repeat("é", 4001) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			settings := Default()
			test.change(&settings)
			if err := settings.Validate(); err == nil {
				t.Fatal("invalid setting was accepted")
			}
		})
	}
}

func TestInvalidPreferencesArePreserved(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "settings.json")
	original := []byte(`{"chunk_seconds":"broken"}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(directory); err == nil {
		t.Fatal("invalid saved settings need a recoverable error")
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != string(original) {
		t.Fatal("invalid saved settings were overwritten")
	}
}

func TestLegacyAudioLanguageKeepsPreferencesUntilExplicitCorrection(t *testing.T) {
	for _, hint := range []string{"pt,en", "pt-BR", "eng"} {
		t.Run(hint, func(t *testing.T) {
			directory := t.TempDir()
			settings := Default()
			settings.Profile, settings.Region, settings.Glossary = "perfil-preservado", "us-west-2", "Termos preservados"
			settings.Appearance.InvertPanels = true
			settings.Provider = ProviderAPI
			settings.AudioAPI = apiconfig.Audio{Connection: apiconfig.Connection{Provider: apiconfig.OpenRouter},
				Model: "openai/whisper-1", Language: hint}
			path := filepath.Join(directory, "settings.json")
			if err := store.WriteJSON(path, settings); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			loaded, err := Load(directory)
			if err != nil {
				t.Fatal("carregamento das preferências legadas falhou", err)
			}
			legacyAudio := settings.AudioAPI
			legacyAudio.ID = loaded.AudioAPI.ID
			if loaded.AudioAPI != legacyAudio || loaded.Profile != settings.Profile ||
				loaded.Region != settings.Region || loaded.Glossary != settings.Glossary || !loaded.Appearance.InvertPanels {
				t.Fatal("um idioma legado descartou outras preferências", err)
			}
			if loaded.Validate() == nil || Save(directory, loaded) == nil {
				t.Fatal("o contrato incompatível foi aplicado sem correção explícita")
			}
			after, err := os.ReadFile(path)
			if err != nil || string(before) != string(after) {
				t.Fatal("o carregamento reescreveu preferências legadas")
			}
			loaded.AudioAPI.Language = ""
			if err := Save(directory, loaded); err != nil {
				t.Fatal("Automático não permitiu corrigir o contrato", err)
			}
		})
	}
}

func TestSaveAndDataDir(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("AI_TALKER_DATA_DIR", directory)
	got, err := DataDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != directory {
		t.Fatalf("data directory = %q, want %q", got, directory)
	}
	settings := Default()
	settings.Profile = "isolated"
	if err := Save(directory, settings); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(directory)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Profile != settings.Profile || loaded.Glossary != settings.Glossary {
		t.Fatalf("preferences did not round-trip: %+v", loaded)
	}
}

func TestLegacyMicrophoneIsResetAndOriginalPreferencesAreBackedUp(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "settings.json")
	original := []byte(`{"profile":"legacy","microphone":7}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	settings, err := Load(directory)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Device() != -1 {
		t.Fatal("a PortAudio index was used as a WinMM device")
	}
	device := 2
	settings.Microphone = &device
	if err := Save(directory, settings); err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(filepath.Join(directory, "settings-python.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(backup) != string(original) {
		t.Fatal("original Python preferences were not preserved")
	}
	loaded, err := Load(directory)
	if err != nil || loaded.Device() != 2 {
		t.Fatalf("native microphone selection was not retained: %+v %v", loaded, err)
	}
}

func TestProcessingPreferencesDefaultOnLegacyAndSurviveAudioChanges(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "settings.json")
	original := `{"profile":"isolated","recorder_backend":"winmm","region":"us-east-1"}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	settings, err := Load(directory)
	if err != nil || settings.Processing != Default().Processing {
		t.Fatalf("as preferências antigas não receberam os padrões de processamento: %+v, %v", settings, err)
	}
	settings.Processing.Mode = processing.ModeFree
	settings.Processing.ModelID = processing.Models()[0].ID
	settings.Processing.TeamsPrompt = "Minha instrução de redação."
	settings.Quality = "fast"
	if err := Save(directory, settings); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(directory)
	if err != nil || loaded.Processing != settings.Processing || loaded.Quality != "fast" {
		t.Fatalf("as opções de texto e de áudio não foram preservadas: %+v, %v", loaded, err)
	}
}
