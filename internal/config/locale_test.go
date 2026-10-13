package config

import (
	"testing"
)

func TestInterfaceLanguageLegacyAndPersistence(t *testing.T) {
	t.Parallel()
	settings := Default()
	settings.InterfaceLanguage = ""
	if settings.InterfaceLocale() != InterfacePortuguese {
		t.Fatal("legacy preferences changed the default interface language")
	}
	settings.InterfaceLanguage = InterfaceEnglish
	settings.Cloud.Language = "pt-BR"
	directory := t.TempDir()
	if err := Save(directory, settings); err != nil {
		t.Fatal(err)
	}
	saved, err := Load(directory)
	if err != nil {
		t.Fatal(err)
	}
	if saved.InterfaceLocale() != InterfaceEnglish || saved.Cloud.Language != "pt-BR" {
		t.Fatal("saving interface language changed the recognition language", saved.InterfaceLanguage, saved.Cloud.Language)
	}
	settings.InterfaceLanguage = "invalid"
	if err := settings.Validate(); err == nil {
		t.Fatal("invalid interface language was accepted")
	}
}
