package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWhisperLegacyPreferencesDefaultToBedrock(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "settings.json")
	payload := `{"profile":"perfil-antigo","quality":"fast","recorder_backend":"winmm"}`
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	settings, err := Load(directory)
	if err != nil || settings.TranscriptionProvider() != ProviderBedrock || settings.Whisper != DefaultWhisper() {
		t.Fatalf("preferências antigas não preservaram Bedrock e os padrões locais: %+v, %v", settings, err)
	}
	unchanged, err := os.ReadFile(path)
	if err != nil || string(unchanged) != payload {
		t.Fatal("carregar preferências reescreveu o arquivo anterior")
	}
}

func TestWhisperPreferencesRoundTripIndependentlyOfTextModels(t *testing.T) {
	directory := t.TempDir()
	settings := Default()
	settings.Provider = ProviderWhisper
	settings.Whisper = Whisper{
		Model: "custom", BinaryPath: `C:\Motor local\whisper-cli.exe`, ModelPath: `C:\Modelos\português.bin`,
		Language: "pt", Threads: 4, BeamSize: 1,
	}
	if err := Save(directory, settings); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(directory)
	if err != nil || loaded.Whisper != settings.Whisper || loaded.TranscriptionProvider() != ProviderWhisper ||
		loaded.ModelID() != "whisper.cpp/custom" || loaded.Processing != settings.Processing || loaded.Merge != settings.Merge {
		t.Fatalf("opções locais ou de texto mudaram ao reabrir: %+v, %v", loaded, err)
	}
}

func TestWhisperInvalidSettingsAreRecoverable(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Settings)
	}{
		{"provedor", func(s *Settings) { s.Provider = "desconhecido" }},
		{"modelo", func(s *Settings) { s.Whisper.Model = "../outro" }},
		{"arquivo ausente", func(s *Settings) { s.Whisper.Model = "custom" }},
		{"idioma", func(s *Settings) { s.Whisper.Language = "desconhecido" }},
		{"threads negativas", func(s *Settings) { s.Whisper.Threads = -1 }},
		{"threads excessivas", func(s *Settings) { s.Whisper.Threads = 65 }},
		{"busca vazia", func(s *Settings) { s.Whisper.BeamSize = 0 }},
		{"busca excessiva", func(s *Settings) { s.Whisper.BeamSize = 11 }},
		{"caminho inválido", func(s *Settings) { s.Whisper.BinaryPath = "motor\x00.exe" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settings := Default()
			tc.change(&settings)
			if err := settings.Validate(); err == nil {
				t.Fatal("uma opção inválida do Whisper foi aceita")
			}
		})
	}
}
