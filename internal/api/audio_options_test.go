package api

import (
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"slices"
	"strings"
	"testing"

	"ai-talker/internal/apiconfig"
	"ai-talker/internal/audio"
)

func TestAudioLanguagesRepeatSeparateFieldsAndAutomaticOmitsBoth(t *testing.T) {
	for _, language := range []string{"pt,en", "", "pt,pt,en"} {
		t.Run(language, func(t *testing.T) {
			connection := apiconfig.Connection{Provider: apiconfig.OpenAI}
			client, err := NewClient(connection, memoryVault{connection.CredentialTarget(), fakeKey}, &http.Client{
				Transport: transportFunc(func(request *http.Request) (*http.Response, error) {
					_, parameters, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
					if err != nil {
						t.Fatal(err)
					}
					form, err := multipart.NewReader(request.Body, parameters["boundary"]).ReadForm(1024)
					if err != nil {
						t.Fatal(err)
					}
					defer form.RemoveAll()
					var expected []string
					if language != "" {
						expected = []string{"pt", "en"}
					}
					if !slices.Equal(form.Value["languages[]"], expected) || len(form.Value["language"]) != 0 {
						t.Fatal("os idiomas foram enviados como uma única string ou o Automático virou código", form.Value)
					}
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"text":"Fala reconhecida"}`)), Header: make(http.Header)}, nil
				}),
			})
			if err != nil {
				t.Fatal(err)
			}
			engine := &transcriber{client: client, model: "gpt-transcribe", language: language}
			if _, err := engine.Transcribe(t.Context(), audio.Chunk{Audio: []byte("synthetic"), Format: audio.FormatMP3}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSingleLanguageAudioContractRejectsCommaListBeforeSending(t *testing.T) {
	for _, provider := range []string{apiconfig.OpenAI, apiconfig.OpenRouter, apiconfig.Generic} {
		connection := apiconfig.Connection{Provider: provider}
		if provider == apiconfig.Generic {
			connection.Endpoint = "https://provider.example/v1"
		}
		client, err := NewClient(connection, memoryVault{connection.CredentialTarget(), fakeKey}, &http.Client{
			Transport: transportFunc(func(*http.Request) (*http.Response, error) {
				t.Fatal("idiomas incompatíveis foram enviados")
				return nil, nil
			}),
		})
		if err != nil {
			t.Fatal(err)
		}
		engine := &transcriber{client: client, model: "whisper-1", language: "pt,en"}
		if _, err := engine.Transcribe(t.Context(), audio.Chunk{Audio: []byte("synthetic"), Format: audio.FormatMP3}); err == nil {
			t.Fatal("uma lista foi aceita por um campo de idioma único", provider)
		}
	}
}

func TestAudioCatalogFiltersChatAudioAndKeepsManualChoiceForUnknownCapabilities(t *testing.T) {
	for _, provider := range []string{apiconfig.OpenAI, apiconfig.Generic, apiconfig.OpenRouter} {
		connection := apiconfig.Connection{Provider: provider}
		if provider == apiconfig.Generic {
			connection.Endpoint = "https://provider.example/v1"
		}
		client, err := NewClient(connection, memoryVault{connection.CredentialTarget(), fakeKey}, &http.Client{
			Transport: transportFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(
					`{"data":[{"id":"text-chat","architecture":{"output_modalities":["text"]}},{"id":"gpt-transcribe"},{"id":"speech","architecture":{"output_modalities":["transcription"]}},{"id":"chat-audio","architecture":{"input_modalities":["audio"],"output_modalities":["text"]}}]}`,
				))}, nil
			}),
		})
		if err != nil {
			t.Fatal(err)
		}
		models, err := client.AudioModels(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, model := range models {
			if model.ID == "text-chat" || model.ID == "chat-audio" {
				t.Fatal("o catálogo confundiu áudio no chat com transcrição")
			}
		}
		if provider == apiconfig.OpenAI && (len(models) != 1 || models[0].ID != "gpt-transcribe") ||
			provider == apiconfig.Generic && (len(models) != 1 || models[0].ID != "speech") {
			t.Fatal("a capacidade confirmada não foi respeitada", models)
		}
	}
}

func TestLegacyAndDiarizationAudioModelsUseTheirOwnMultipartFields(t *testing.T) {
	for _, model := range []string{"whisper-1", "gpt-4o-transcribe", "gpt-4o-transcribe-diarize"} {
		t.Run(model, func(t *testing.T) {
			connection := apiconfig.Connection{Provider: apiconfig.OpenAI}
			client, err := NewClient(connection, memoryVault{connection.CredentialTarget(), fakeKey}, &http.Client{
				Transport: transportFunc(func(request *http.Request) (*http.Response, error) {
					_, parameters, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
					if err != nil {
						t.Fatal(err)
					}
					form, err := multipart.NewReader(request.Body, parameters["boundary"]).ReadForm(1024)
					if err != nil {
						t.Fatal(err)
					}
					defer form.RemoveAll()
					if !slices.Equal(form.Value["language"], []string{"pt"}) || len(form.Value["languages[]"]) != 0 {
						t.Fatal("o contrato de idioma único não foi preservado", form.Value)
					}
					if model == "gpt-4o-transcribe-diarize" {
						if len(form.Value["prompt"]) != 0 || !slices.Equal(form.Value["chunking_strategy"], []string{"auto"}) {
							t.Fatal("o modelo de locutores recebeu um prompt incompatível ou perdeu o corte automático")
						}
					} else if !slices.Equal(form.Value["prompt"], []string{"Vocabulário da gravação"}) {
						t.Fatal("o vocabulário foi perdido por um modelo que aceita prompt")
					}
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"text":"Fala reconhecida"}`)), Header: make(http.Header)}, nil
				}),
			})
			if err != nil {
				t.Fatal(err)
			}
			engine := &transcriber{client: client, model: model, language: "pt", glossary: "Vocabulário da gravação"}
			if _, err := engine.Transcribe(t.Context(), audio.Chunk{Audio: []byte("synthetic"), Format: audio.FormatMP3}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
