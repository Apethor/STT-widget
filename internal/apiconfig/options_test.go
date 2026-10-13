package apiconfig

import (
	"slices"
	"strings"
	"testing"
)

func TestEndpointValidationAndCredentialBinding(t *testing.T) {
	for _, endpoint := range []string{
		"http://remote.example/v1", "https://user:secret@example.com/v1",
		"https://example.com/v1?key=secret", "https://example.com/v1#secret", "https://example.com\nsecret",
	} {
		if (Connection{Provider: Generic, Endpoint: endpoint}).Validate() == nil {
			t.Error("endpoint inseguro foi aceito")
		}
	}
	first := Connection{Provider: Generic, Endpoint: "http://127.0.0.1:3210/v1"}
	second := Connection{Provider: Generic, Endpoint: "http://127.0.0.1:3210/other"}
	if first.Validate() != nil || first.CredentialTarget() == second.CredentialTarget() ||
		strings.Contains(first.CredentialTarget(), "127.0.0.1") {
		t.Fatal("a credencial não ficou vinculada ao destino completo")
	}
	for _, provider := range []string{OpenRouter, OpenAI} {
		connection := Connection{Provider: provider, Endpoint: "https://other.example/v1"}
		if connection.Validate() == nil {
			t.Fatal("o preset fixo aceitou outro servidor")
		}
		connection.Endpoint = ""
		if connection.Validate() != nil {
			t.Fatal("o preset original foi alterado")
		}
	}
}

func TestAudioLanguageHintsRespectEachProviderAndModel(t *testing.T) {
	for _, tc := range []struct {
		name, provider, model, input string
		want                         []string
		invalid                      bool
	}{
		{"automático", OpenAI, "gpt-transcribe", " AUTO ", nil, false},
		{"lista multilíngue", OpenAI, "gpt-transcribe", "pt,en,pt", []string{"pt", "en"}, false},
		{"códigos documentados", OpenAI, "gpt-transcribe", "eng,zh-cn,zh-tw,zh-hk", []string{"eng", "zh-cn", "zh-tw", "zh-hk"}, false},
		{"locale não documentado", OpenAI, "gpt-transcribe", "pt-BR", nil, true},
		{"legado único", OpenAI, "whisper-1", "pt", []string{"pt"}, false},
		{"legado sem lista", OpenAI, "whisper-1", "pt,en", nil, true},
		{"legado ISO 639-1", OpenAI, "whisper-1", "eng", nil, true},
		{"OpenRouter sem locale", OpenRouter, "openai/whisper-1", "pt-BR", nil, true},
		{"compatível preserva extensão", Generic, "modelo-manual", "pt-BR", []string{"pt-BR"}, false},
		{"compatível sem lista confirmada", Generic, "modelo-manual", "pt,en", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			audio := Audio{Connection: Connection{Provider: tc.provider}, Model: tc.model, Language: tc.input}
			actual, err := audio.LanguageCodes()
			if tc.invalid && err == nil || !tc.invalid && (err != nil || !slices.Equal(actual, tc.want)) {
				t.Fatalf("idiomas: %v / %v", actual, err)
			}
		})
	}
}
