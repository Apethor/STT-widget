package api

import (
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"ai-talker/internal/apiconfig"
	"ai-talker/internal/audio"
)

func TestAzureSpeechUsesResourceKeyMultipartAndStructuredDiarization(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost ||
			request.URL.Path != "/speechtotext/transcriptions:transcribe" ||
			request.URL.Query().Get("api-version") != "2025-10-15" ||
			request.Header.Get("Ocp-Apim-Subscription-Key") != fakeKey ||
			request.Header.Get("Authorization") != "" {
			t.Errorf("endpoint ou autenticação Azure Speech incorretos: %s %s", request.Method, request.URL)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if err := request.ParseMultipartForm(1024 * 1024); err != nil {
			t.Error(err)
			return
		}
		defer request.MultipartForm.RemoveAll()
		var definition struct {
			Locales     []string `json:"locales"`
			Diarization struct {
				Enabled bool `json:"enabled"`
			} `json:"diarization"`
			PhraseList struct {
				Phrases []string `json:"phrases"`
			} `json:"phraseList"`
			EnhancedMode struct {
				Enabled      bool   `json:"enabled"`
				Model        string `json:"model"`
				ModelOptions struct {
					Timestamps string `json:"timestamps"`
				} `json:"modelOptions"`
			} `json:"enhancedMode"`
		}
		if err := json.Unmarshal([]byte(request.FormValue("definition")), &definition); err != nil {
			t.Error(err)
			return
		}
		file, header, err := request.FormFile("audio")
		if err != nil {
			t.Error(err)
			return
		}
		defer file.Close()
		payload, _ := io.ReadAll(file)
		if header.Filename != "audio.wav" || string(payload) != "synthetic wav" ||
			!slices.Equal(definition.Locales, []string{"pt"}) ||
			!definition.Diarization.Enabled || !definition.EnhancedMode.Enabled ||
			definition.EnhancedMode.Model != apiconfig.AzureSpeechModel ||
			definition.EnhancedMode.ModelOptions.Timestamps != "word" ||
			!slices.Equal(definition.PhraseList.Phrases, []string{"Voxtral", "AI Talker"}) {
			t.Errorf("definição/áudio divergentes: %+v / %s / %q",
				definition, header.Filename, payload)
		}
		io.WriteString(w, `{
			"combinedPhrases":[{"text":"Olá, AI Talker."}],
			"phrases":[
				{"text":"Olá,","offsetMilliseconds":250,"durationMilliseconds":450,"speaker":0},
				{"text":"AI Talker.","offsetMilliseconds":900,"durationMilliseconds":700,"speaker":1}
			]
		}`)
	}))
	defer server.Close()
	connection := apiconfig.Connection{
		ID: strings.Repeat("a", 32), Provider: apiconfig.AzureSpeech, Endpoint: server.URL,
	}
	client, err := NewClient(connection, memoryVault{connection.CredentialTarget(), fakeKey}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	engine := &transcriber{
		client: client, model: apiconfig.AzureSpeechModel, language: "pt",
		glossary: "Voxtral, AI Talker, Voxtral",
	}
	result, err := engine.Transcribe(t.Context(), audio.Chunk{
		Duration: 5, HasSpeech: true, Format: audio.FormatWAV, Audio: []byte("synthetic wav"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "Olá, AI Talker." || len(result.Segments) != 2 ||
		result.Segments[0].Start != 0.25 || result.Segments[0].End != 0.7 ||
		result.Segments[0].Speaker != "0" || result.Segments[1].Start != 0.9 ||
		result.Segments[1].Speaker != "1" {
		t.Fatalf("texto ou metadados temporais perdidos: %+v", result)
	}
}

func TestAzureSpeechOmitsAutomaticLocaleAndRejectsUnsupportedInputBeforeSending(t *testing.T) {
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests++
		if err := request.ParseMultipartForm(1024); err != nil {
			t.Error(err)
			return
		}
		var definition map[string]json.RawMessage
		if err := json.Unmarshal([]byte(request.FormValue("definition")), &definition); err != nil {
			t.Error(err)
			return
		}
		if _, exists := definition["locales"]; exists {
			t.Error("Automático enviou uma dica de idioma")
		}
		io.WriteString(w, `{"combinedPhrases":[{"text":"Automatic speech."}]}`)
	}))
	defer server.Close()
	connection := apiconfig.Connection{Provider: apiconfig.AzureSpeech, Endpoint: server.URL}
	client, err := NewClient(connection, memoryVault{connection.CredentialTarget(), fakeKey}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	engine := &transcriber{client: client, model: apiconfig.AzureSpeechModel}
	result, err := engine.Transcribe(t.Context(), audio.Chunk{
		Duration: 2, Format: audio.FormatMP3, Audio: []byte("synthetic mp3"),
	})
	if err != nil || result.Text != "Automatic speech." || requests != 1 {
		t.Fatalf("idioma automático não foi preservado: %+v / %v / %d", result, err, requests)
	}
	engine.language = "pt,en"
	if _, err := engine.Transcribe(t.Context(), audio.Chunk{
		Duration: 2, Format: audio.FormatMP3, Audio: []byte("synthetic mp3"),
	}); err == nil || requests != 1 {
		t.Fatal("lista de idiomas incompatível chegou ao Azure Speech")
	}
	if _, err := engine.Transcribe(t.Context(), audio.Chunk{
		Duration: 2, Format: "native", Audio: []byte("synthetic pcm"),
	}); err == nil || requests != 1 {
		t.Fatal("um formato não documentado chegou ao Azure Speech")
	}
}

func TestAzureSpeechPhraseAndSpeakerMetadataAreBounded(t *testing.T) {
	phrases := azureSpeechPhrases(" AWS ; Amazon Bedrock\n AWS,  Node.js ")
	if !slices.Equal(phrases, []string{"AWS", "Amazon Bedrock", "Node.js"}) {
		t.Fatalf("glossário não foi convertido em frases únicas: %v", phrases)
	}
	for _, raw := range []string{`"speaker-2"`, `2`, `null`, `{"unexpected":true}`, `"speaker\nsecret"`} {
		got := azureSpeechSpeaker(json.RawMessage(raw))
		switch raw {
		case `"speaker-2"`:
			if got != "speaker-2" {
				t.Fatal(got)
			}
		case `2`:
			if got != "2" {
				t.Fatal(got)
			}
		default:
			if got != "" {
				t.Fatalf("ID inválido foi preservado: %q", got)
			}
		}
	}
}

func TestAzureSpeechMultipartUsesAudioPartName(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		_, params, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
		if err != nil {
			t.Error(err)
			return
		}
		reader := multipart.NewReader(request.Body, params["boundary"])
		form, err := reader.ReadForm(1024)
		if err != nil {
			t.Error(err)
			return
		}
		defer form.RemoveAll()
		if len(form.File["audio"]) != 1 || len(form.Value["definition"]) != 1 {
			t.Errorf("multipart incompleto: %+v", form)
		}
		io.WriteString(w, `{"phrases":[{"text":"Fala","offsetMilliseconds":0,"durationMilliseconds":200,"speaker":"A"}]}`)
	}))
	defer server.Close()
	connection := apiconfig.Connection{Provider: apiconfig.AzureSpeech, Endpoint: server.URL}
	client, err := NewClient(connection, memoryVault{connection.CredentialTarget(), fakeKey}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	engine := &transcriber{client: client, model: apiconfig.AzureSpeechModel}
	result, err := engine.Transcribe(t.Context(), audio.Chunk{
		Duration: 1, Format: audio.FormatMP3, Audio: []byte("synthetic"),
	})
	if err != nil || result.Text != "Fala" || len(result.Segments) != 1 ||
		result.Segments[0].Speaker != "A" {
		t.Fatalf("resposta Azure Speech incompatível: %+v / %v", result, err)
	}
}

func TestAzureSpeechPreservesWordTimingInsideOneSpeakerPhrase(t *testing.T) {
	t.Parallel()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if err := request.ParseMultipartForm(1024); err != nil {
			t.Error(err)
			return
		}
		defer request.MultipartForm.RemoveAll()
		var definition azureSpeechDefinition
		if err := json.Unmarshal([]byte(request.FormValue("definition")), &definition); err != nil {
			t.Error(err)
		}
		if definition.EnhancedMode.ModelOptions.Timestamps != "word" {
			t.Error("tempos de frase não permitem intercalar intervenções dentro da mesma voz")
		}
		io.WriteString(w, `{
			"combinedPhrases":[{"text":"Pergunta. Nova pergunta."}],
			"phrases":[{
				"text":"Pergunta. Nova pergunta.","offsetMilliseconds":100,"durationMilliseconds":3800,"speaker":0,
				"words":[
					{"text":"pergunta.","offsetMilliseconds":100,"durationMilliseconds":400},
					{"text":"Nova","offsetMilliseconds":3000,"durationMilliseconds":300},
					{"text":"pergunta.","offsetMilliseconds":3400,"durationMilliseconds":500}
				]
			}]
		}`)
	}))
	defer server.Close()
	connection := apiconfig.Connection{Provider: apiconfig.AzureSpeech, Endpoint: server.URL}
	client, err := NewClient(connection, memoryVault{connection.CredentialTarget(), fakeKey}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	engine := &transcriber{client: client, model: apiconfig.AzureSpeechModel}
	result, err := engine.Transcribe(t.Context(), audio.Chunk{
		Duration: 5, HasSpeech: true, Format: audio.FormatWAV, Audio: []byte("synthetic"),
	})
	if err != nil || result.Text != "Pergunta. Nova pergunta." || len(result.Segments) != 3 {
		t.Fatalf("uma frase inteira substituiu os turnos cronometrados: %+v / %v", result, err)
	}
	if result.Segments[0].Text != "Pergunta." || result.Segments[0].Start != 0.1 ||
		result.Segments[0].End != 0.5 || result.Segments[1].Start != 3 ||
		result.Segments[2].End != 3.9 || result.Segments[2].Speaker != "0" {
		t.Fatalf("tempos, pontuação ou fonte da frase não foram preservados: %+v", result.Segments)
	}
}

func TestAzureSpeechKeepsFullTextWhenSomePhraseTimingsAreInvalid(t *testing.T) {
	t.Parallel()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		io.WriteString(w, `{
			"combinedPhrases":[{"text":"Primeira fala. Segunda fala."}],
			"phrases":[
				{"text":"Primeira fala.","offsetMilliseconds":0,"durationMilliseconds":500,"speaker":0},
				{"text":"Segunda fala.","offsetMilliseconds":1000,"durationMilliseconds":0,"speaker":1}
			]
		}`)
	}))
	defer server.Close()
	connection := apiconfig.Connection{Provider: apiconfig.AzureSpeech, Endpoint: server.URL}
	client, err := NewClient(connection, memoryVault{connection.CredentialTarget(), fakeKey}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	engine := &transcriber{client: client, model: apiconfig.AzureSpeechModel}
	result, err := engine.Transcribe(t.Context(), audio.Chunk{
		Duration: 3, HasSpeech: true, Format: audio.FormatMP3, Audio: []byte("synthetic"),
	})
	if err != nil || result.Text != "Primeira fala. Segunda fala." || len(result.Segments) != 0 {
		t.Fatalf("os metadados incompletos descartariam parte da fala: %+v / %v", result, err)
	}
}

func TestAzureSpeechPreservesCompleteWordTimesWhenTheSummaryGroupsSpeakers(t *testing.T) {
	t.Parallel()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{
			"combinedPhrases":[{"text":"Primeira pergunta. Outra pergunta. Resposta."}],
			"phrases":[
				{"text":"Primeira pergunta. Outra pergunta.","offsetMilliseconds":0,"durationMilliseconds":5000,"speaker":0,
				 "words":[
					{"text":"Primeira","offsetMilliseconds":0,"durationMilliseconds":400},
					{"text":"pergunta.","offsetMilliseconds":400,"durationMilliseconds":600},
					{"text":"Outra","offsetMilliseconds":4000,"durationMilliseconds":400},
					{"text":"pergunta.","offsetMilliseconds":4400,"durationMilliseconds":600}
				 ]},
				{"text":"Resposta.","offsetMilliseconds":2000,"durationMilliseconds":1000,"speaker":1,
				 "words":[{"text":"Resposta.","offsetMilliseconds":2000,"durationMilliseconds":1000}]}
			]
		}`)
	}))
	defer server.Close()
	connection := apiconfig.Connection{Provider: apiconfig.AzureSpeech, Endpoint: server.URL}
	client, err := NewClient(connection, memoryVault{connection.CredentialTarget(), fakeKey}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	engine := &transcriber{client: client, model: apiconfig.AzureSpeechModel}
	result, err := engine.Transcribe(t.Context(), audio.Chunk{
		Duration: 5, HasSpeech: true, Format: audio.FormatWAV, Audio: []byte("synthetic"),
	})
	if err != nil || len(result.Segments) != 5 || result.Segments[2].Text != "Resposta." ||
		result.Segments[2].Start != 2 || result.Segments[2].Speaker != "1" {
		t.Fatalf("o resumo agrupado anulou os tempos completos de palavras: %+v / %v", result, err)
	}
}
