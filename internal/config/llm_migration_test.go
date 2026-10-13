package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"ai-talker/internal/apiconfig"
	"ai-talker/internal/processing"
)

func TestLegacyLLMProfilesPreserveBothModelsAndRequireExplicitAPIMergeAdjustment(t *testing.T) {
	for _, provider := range []string{"bedrock", "api"} {
		t.Run(provider, func(t *testing.T) {
			settings := Default()
			settings.Processing.Provider, settings.Processing.ModelID = provider, "original-text-model"
			settings.Processing.MergeModelID, settings.Processing.MergeReasoning, settings.Processing.MergePrompt = "", "", ""
			if provider == "api" {
				settings.Processing.API = apiconfig.Connection{Provider: apiconfig.OpenAI}
			}
			settings.Merge.ModelID, settings.Merge.Prompt, settings.Merge.ReasoningEffort = "global.openai.gpt-6.1-sol", "Minha instrução de mesclagem", "high"
			settings.TextProfiles = []processing.Profile{{ID: "old-profile", Name: "Perfil antigo", Options: settings.Processing}}
			data, err := json.Marshal(settings)
			if err != nil {
				t.Fatal(err)
			}
			directory := t.TempDir()
			path := filepath.Join(directory, "settings.json")
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			loaded, err := Load(directory)
			if err != nil {
				t.Fatal(err)
			}
			for _, options := range []processing.Options{loaded.Processing, loaded.TextProfiles[0].Options} {
				if options.Provider != provider || options.ModelID != "original-text-model" ||
					options.MergeModelID != settings.Merge.ModelID || options.MergePrompt != settings.Merge.Prompt || options.MergeReasoning != "high" {
					t.Fatal("a migração substituiu uma configuração anterior", options)
				}
				_, err := options.ForMerge(loaded.Merge)
				if provider == "api" && err == nil || provider == "bedrock" && err != nil {
					t.Fatal("a incompatibilidade mudou de provider silenciosamente", err)
				}
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != string(data) {
				t.Fatal("ler a migração reescreveu as preferências")
			}
		})
	}
}
