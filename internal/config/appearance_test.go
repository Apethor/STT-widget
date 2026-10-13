package config

import (
	"os"
	"path/filepath"
	"testing"

	"ai-talker/internal/processing"
)

func TestAppearanceAndMergeDefaultsPreserveLegacySettings(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "settings.json")
	original := `{"profile":"isolated","recorder_backend":"winmm","processing":{"mode":"free"}}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	settings, err := Load(directory)
	if err != nil || settings.Appearance != DefaultAppearance() || settings.Merge != processing.DefaultMerge() {
		t.Fatalf("os padrões não preservaram as preferências antigas: %+v, %v", settings, err)
	}
	if settings.Appearance.Layout != LayoutColumn || settings.Appearance.Theme != ThemeDark {
		t.Fatal("novas instalações e preferências sem aparência devem abrir em coluna com tema Escuro")
	}
	settings.Appearance = Appearance{Theme: ThemeLight, Layout: LayoutStacked, RecordingSize: RecordingMedium}
	settings.Merge.ReasoningEffort = "high"
	settings.Merge.Prompt = "Mescle preservando os fatos."
	if err := Save(directory, settings); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(directory)
	if err != nil || loaded.Appearance != settings.Appearance || loaded.Merge != settings.Merge ||
		loaded.Processing.Mode != processing.ModeFree {
		t.Fatalf("a aparência e mesclagem não foram restauradas: %+v, %v", loaded, err)
	}
}

func TestAppearanceRejectsInvalidChoices(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		change func(*Appearance)
	}{
		{name: "tema", change: func(a *Appearance) { a.Theme = "purple" }},
		{name: "disposição", change: func(a *Appearance) { a.Layout = "grid" }},
		{name: "tamanho", change: func(a *Appearance) { a.RecordingSize = "tiny" }},
		{name: "fonte", change: func(a *Appearance) { a.FontFamily = "Comic Sans" }},
		{name: "tamanho de fonte", change: func(a *Appearance) { a.FontSize = 40 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settings := Default()
			tc.change(&settings.Appearance)
			if err := settings.Validate(); err == nil {
				t.Fatal("uma escolha inválida de aparência foi aceita")
			}
		})
	}
}
