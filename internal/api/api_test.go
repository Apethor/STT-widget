package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ai-talker/internal/apiconfig"
	"ai-talker/internal/audio"
	"ai-talker/internal/config"
	"ai-talker/internal/processing"
	"ai-talker/internal/secrets"
)

const fakeKey = "fictitious-secret-for-api-tests"

type memoryVault struct {
	target string
	key    string
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestOpenAITranscriptionLanguagesAndOpenRouterAudioCatalogUseProviderContracts(t *testing.T) {
	for _, model := range []string{"gpt-transcribe", "gpt-4o-mini-transcribe"} {
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
					languageField, absentField := "language", "languages[]"
					if model == "gpt-transcribe" {
						languageField, absentField = absentField, languageField
					}
					if request.URL.String() != "https://api.openai.com/v1/audio/transcriptions" ||
						len(form.Value[languageField]) != 1 || form.Value[languageField][0] != "pt" ||
						len(form.Value[absentField]) != 0 {
						t.Fatal("o modelo recebeu o campo de idioma de outra API", form.Value)
					}
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"text":"Texto reconhecido."}`)), Header: make(http.Header)}, nil
				}),
			})
			if err != nil {
				t.Fatal(err)
			}
			transcriber := &transcriber{client: client, model: model, language: "pt"}
			result, err := transcriber.Transcribe(t.Context(), audio.Chunk{Audio: []byte("synthetic-mp3"), Format: audio.FormatMP3})
			if err != nil || result.Text != "Texto reconhecido." {
				t.Fatal(result, err)
			}
		})
	}
	connection := apiconfig.Connection{Provider: apiconfig.OpenRouter}
	client, err := NewClient(connection, memoryVault{connection.CredentialTarget(), fakeKey}, &http.Client{
		Transport: transportFunc(func(request *http.Request) (*http.Response, error) {
			if request.URL.Query().Get("output_modalities") != "transcription" ||
				request.URL.Path != "/api/v1/models" {
				t.Fatal("o catálogo de áudio trouxe apenas modelos de texto", request.URL)
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"audio-model"}]}`)), Header: make(http.Header)}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	models, err := client.AudioModels(t.Context())
	if err != nil || len(models) != 1 || models[0].ID != "audio-model" {
		t.Fatal(models, err)
	}
}

func (v memoryVault) Read(target string) (string, error) {
	if target != v.target {
		return "", secrets.ErrNotFound
	}
	return v.key, nil
}
func (memoryVault) Write(string, string) error { return errors.New("read-only test vault") }
func (memoryVault) Delete(string) error        { return errors.New("read-only test vault") }

func TestNamedAccountDoesNotBorrowLegacyEndpointCredential(t *testing.T) {
	connection := apiconfig.Connection{
		ID: strings.Repeat("a", 32), Provider: apiconfig.Generic, Endpoint: "https://accounts.example/v1",
	}
	var requests int
	client, err := NewClient(connection, memoryVault{connection.LegacyCredentialTarget(), fakeKey}, &http.Client{
		Transport: transportFunc(func(*http.Request) (*http.Response, error) {
			requests++
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"data":[{"id":"manual-model"}]}`)),
				Header:     make(http.Header),
			}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Models(t.Context()); !errors.Is(err, secrets.ErrNotFound) || requests != 0 {
		t.Fatalf("uma conta nova usou a credencial antiga do mesmo endpoint: chamadas=%d erro=%v", requests, err)
	}
}

func TestMigratedLegacyAccountCanStillUseItsCredentialWhenVaultWriteFails(t *testing.T) {
	connection := apiconfig.Connection{Provider: apiconfig.Generic, Endpoint: "https://legacy.example/v1"}
	connection.ID = apiconfig.LegacyConnectionID(connection)
	client, err := NewClient(connection, memoryVault{connection.LegacyCredentialTarget(), fakeKey}, &http.Client{
		Transport: transportFunc(func(request *http.Request) (*http.Response, error) {
			if request.Header.Get("Authorization") != "Bearer "+fakeKey {
				t.Fatal("a conexão migrada perdeu sua credencial")
			}
			return &http.Response{
				StatusCode: http.StatusOK, Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"manual-model"}]}`)),
			}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Models(t.Context()); err != nil {
		t.Fatalf("a falha de migração da credencial impediu a conexão existente: %v", err)
	}
}

type writableTestVault map[string]string

func (v writableTestVault) Read(target string) (string, error) {
	if key, exists := v[target]; exists {
		return key, nil
	}
	return "", secrets.ErrNotFound
}

func (v writableTestVault) Write(target, key string) error {
	v[target] = key
	return nil
}

func (v writableTestVault) Delete(target string) error {
	delete(v, target)
	return nil
}

func TestLegacyPresetsSharingAnEndpointCanMigrateInEitherOrder(t *testing.T) {
	for _, genericFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("generic-first=%t", genericFirst), func(t *testing.T) {
			preset := apiconfig.Connection{Provider: apiconfig.OpenAI}
			generic := apiconfig.Connection{Provider: apiconfig.Generic, Endpoint: preset.BaseURL()}
			preset.ID, generic.ID = apiconfig.LegacyConnectionID(preset), apiconfig.LegacyConnectionID(generic)
			connections := []apiconfig.Connection{preset, generic}
			if genericFirst {
				connections[0], connections[1] = connections[1], connections[0]
			}
			vault := writableTestVault{preset.LegacyCredentialTarget(): fakeKey}
			for _, connection := range connections {
				client, err := NewClient(connection, vault, &http.Client{
					Transport: transportFunc(func(request *http.Request) (*http.Response, error) {
						if request.Header.Get("Authorization") != "Bearer "+fakeKey {
							t.Fatal("um preset legado perdeu a credencial compartilhada")
						}
						return &http.Response{
							StatusCode: http.StatusOK, Header: make(http.Header),
							Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"manual-model"}]}`)),
						}, nil
					}),
				})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := client.Models(t.Context()); err != nil {
					t.Fatal("migrar um preset apagou a credencial do outro preset legado no mesmo endpoint", err)
				}
				if _, err := vault.Read(connection.CredentialTarget()); err != nil {
					t.Fatal("a migração não criou a credencial individual da conexão")
				}
			}
		})
	}
}

func apiServer(t *testing.T, handler http.HandlerFunc) (*Client, apiconfig.Connection) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	connection := apiconfig.Connection{Provider: apiconfig.Generic, Endpoint: server.URL + "/v1"}
	client, err := NewClient(connection, memoryVault{connection.CredentialTarget(), fakeKey}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	return client, connection
}

func TestFreeAPIRequestSendsExactCurrentTextAndCompletedConversation(t *testing.T) {
	requestText := "  Uma mensagem com espaços.\nE outra linha.  "
	client, connection := apiServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer "+fakeKey {
			t.Error("rota ou autenticação incorreta")
		}
		var body struct {
			Model    string    `json:"model"`
			Messages []message `json:"messages"`
			Tools    []any     `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Model != "editable-model" || len(body.Tools) != 0 || len(body.Messages) != 3 ||
			body.Messages[0].Role != "user" || *body.Messages[1].Content != "Resposta anterior." ||
			*body.Messages[2].Content != requestText {
			t.Errorf("o modo Livre envolveu/alterou o prompt ou a conversa: %+v", body)
		}
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"Resposta exata."},"finish_reason":"stop"}],"usage":{"prompt_tokens":7}}`)
	})
	options := processing.Default()
	options.Provider, options.API, options.ModelID = "api", connection, "editable-model"
	options.Mode, options.WebSearchDisabled = processing.ModeFree, true
	result, err := client.process(t.Context(), processing.Request{
		Text: requestText, Options: options, History: []processing.Turn{{Prompt: "Anterior.", Response: "Resposta anterior."}},
	}, nil)
	if err != nil || result.Text != "Resposta exata." || result.ModelID != options.ModelID {
		t.Fatal(result, err)
	}
}

func TestAPIWebBudgetDeduplicatesQueriesAndFinalizesWithSourcesWithoutAWS(t *testing.T) {
	var requests, searches atomic.Int32
	client, connection := apiServer(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if requests.Add(1) == 1 {
			calls := []map[string]any{}
			for index, query := range []string{"  taxa atual ", "TAXA   atual", "outro dado", "mais uma"} {
				arguments, _ := json.Marshal(map[string]string{"query": query})
				calls = append(calls, map[string]any{"id": string(rune('a' + index)), "type": "function",
					"function": map[string]string{"name": "web_search", "arguments": string(arguments)}})
			}
			json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{
				"message": map[string]any{"role": "assistant", "content": nil, "tool_calls": calls}, "finish_reason": "tool_calls",
			}}})
			return
		}
		if _, hasTools := body["tools"]; hasTools || body["tool_choice"] != "none" {
			t.Error("o limite não encerrou a autorização de buscas")
		}
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"Resposta com os dados encontrados."},"finish_reason":"stop"}]}`)
	})
	options := processing.Default()
	options.Provider, options.API, options.ModelID = "api", connection, "tools-model"
	result, err := client.process(t.Context(), processing.Request{Text: "Pesquise.", Options: options, WebSearchLimit: 2},
		func(ctx context.Context, query string) (string, []processing.Source, error) {
			searches.Add(1)
			return "Um dado confirmado.", []processing.Source{{Title: "Fonte", URL: "https://example.com/source"}}, ctx.Err()
		})
	if err != nil || searches.Load() != 2 || requests.Load() != 2 || len(result.Sources) != 1 ||
		!strings.Contains(result.Text, "Limite de buscas") || !strings.Contains(result.Text, "https://example.com/source") {
		t.Fatalf("o orçamento virou erro/perdeu fontes/duplicou busca: %+v / %v / %d", result, err, searches.Load())
	}
}

func TestAPIAudioUsesIndependentMultipartModelLanguageAndBytes(t *testing.T) {
	var called atomic.Bool
	_, connection := apiServer(t, func(w http.ResponseWriter, r *http.Request) {
		called.Store(true)
		if r.URL.Path != "/v1/audio/transcriptions" || r.Header.Get("Authorization") != "Bearer "+fakeKey {
			t.Error("a transcrição usou a rota ou chave errada")
		}
		if err := r.ParseMultipartForm(1024); err != nil {
			t.Error(err)
			return
		}
		defer r.MultipartForm.RemoveAll()
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Error(err)
			return
		}
		defer file.Close()
		payload, _ := io.ReadAll(file)
		if header.Filename != "audio.mp3" || string(payload) != "synthetic-mp3" ||
			r.FormValue("model") != "audio-only-model" || r.FormValue("language") != "pt" ||
			r.FormValue("prompt") != "Termos técnicos." {
			t.Error("opções de texto substituíram as de áudio")
		}
		io.WriteString(w, `{"text":"Transcrição por API."}`)
	})
	settings := config.Default()
	settings.Provider = config.ProviderAPI
	settings.AudioAPI = apiconfig.Audio{Connection: connection, Model: "audio-only-model", Language: "pt"}
	settings.Processing.ModelID, settings.Glossary = "different-text-model", "Termos técnicos."
	transcriber, err := Factory(memoryVault{connection.CredentialTarget(), fakeKey})(t.Context(), settings)
	if err != nil {
		t.Fatal(err)
	}
	result, err := transcriber.Transcribe(t.Context(), audio.Chunk{
		Duration: 5, HasSpeech: true, Format: audio.FormatMP3, Audio: []byte("synthetic-mp3"),
	})
	if err != nil || !called.Load() || result.Text != "Transcrição por API." {
		t.Fatal(result, err)
	}
}

func TestAPIKeyNeverFollowsRedirectOrAppearsInErrors(t *testing.T) {
	var leaked atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Add(1) }))
	defer target.Close()
	client, _ := apiServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/?key="+fakeKey, http.StatusTemporaryRedirect)
	})
	_, err := client.Models(t.Context())
	if err == nil || strings.Contains(err.Error(), fakeKey) || leaked.Load() != 0 {
		t.Fatalf("o cliente seguiu o redirecionamento ou expôs a chave: %v / %d", err, leaked.Load())
	}
}

func TestAPIRejectsServerEchoedSecretsAndCancelsHungResponse(t *testing.T) {
	client, _ := apiServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, fakeKey)
	})
	_, err := client.Models(t.Context())
	if err == nil || strings.Contains(err.Error(), fakeKey) {
		t.Fatal("um diagnóstico do servidor expôs a chave", err)
	}
	started := make(chan struct{}, 1)
	blocked, _ := apiServer(t, func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { _, err := blocked.Models(ctx); done <- err }()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("o cancelamento foi ocultado por um erro genérico", err)
		}
	case <-time.After(time.Second):
		t.Fatal("o cancelamento deixou a API bloqueada")
	}
}

func TestAPIRejectsUnsourcedSearchAndUnsafeLinks(t *testing.T) {
	client, connection := apiServer(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"x","type":"function","function":{"name":"web_search","arguments":"{\"query\":\"dado atual\"}"}}]},"finish_reason":"tool_calls"}]}`)
	})
	options := processing.Default()
	options.Provider, options.API = "api", connection
	_, err := client.process(t.Context(), processing.Request{Text: "Pesquise.", Options: options},
		func(context.Context, string) (string, []processing.Source, error) { return "Sem fontes.", nil, nil })
	if err == nil {
		t.Fatal("a API entregou uma resposta pesquisada sem fontes")
	}
	links := appendSources(nil, []processing.Source{
		{URL: "https://user:password@example.com"}, {URL: "javascript:alert(1)"}, {URL: "https://example.com\nsecret"},
		{URL: "https://example.com"}, {URL: "https://example.com"},
	})
	if len(links) != 1 || links[0].URL != "https://example.com" {
		t.Fatal("links inseguros ou repetidos entraram no resultado", links)
	}
}

func TestAzureV1UsesDeploymentKeyAndExplicitChatParameters(t *testing.T) {
	for _, tc := range []struct {
		deployment string
		completion bool
		reasoning  bool
	}{
		{"gpt-5-mini-prod", true, true},
		{"deployment-chat", true, true},
	} {
		t.Run(tc.deployment, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if request.URL.Path != "/openai/v1/chat/completions" ||
					request.Header.Get("api-key") != fakeKey ||
					request.Header.Get("Authorization") != "" {
					t.Errorf("Azure v1 usou endpoint ou autenticação incorretos: %s %s", request.Method, request.URL)
				}
				var body map[string]any
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body["model"] != tc.deployment {
					t.Errorf("o deployment configurado não foi enviado: %#v", body["model"])
				}
				_, hasCompletion := body["max_completion_tokens"]
				_, hasMax := body["max_tokens"]
				_, hasReasoning := body["reasoning_effort"]
				if tc.completion && (!hasCompletion || hasMax) ||
					!tc.completion && (!hasMax || hasCompletion) ||
					hasReasoning != tc.reasoning {
					t.Errorf("parâmetros incompatíveis com o deployment: %#v", body)
				}
				io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"Azure respondeu."},"finish_reason":"stop"}]}`)
			}))
			defer server.Close()
			connection := apiconfig.Connection{
				Provider: apiconfig.Azure, Endpoint: server.URL + "/openai/v1",
			}
			client, err := NewClient(connection, memoryVault{connection.CredentialTarget(), fakeKey}, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			options := processing.Default()
			options.Provider, options.API = "api", connection
			options.ModelID, options.Mode = tc.deployment, processing.ModeFree
			options.WebSearchDisabled, options.ReasoningEffort = true, "high"
			result, err := client.process(t.Context(), processing.Request{
				Text: "Responda.", Options: options,
			}, nil)
			if err != nil || result.Text != "Azure respondeu." {
				t.Fatalf("Azure Chat Completions não concluiu: %+v / %v", result, err)
			}
		})
	}
}

func TestAPICancellationBetweenToolResultsDoesNotStartAnotherSearch(t *testing.T) {
	var modelCalls, searches atomic.Int32
	client, connection := apiServer(t, func(w http.ResponseWriter, r *http.Request) {
		modelCalls.Add(1)
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"a","type":"function","function":{"name":"web_search","arguments":"{\"query\":\"primeira\"}"}},{"id":"b","type":"function","function":{"name":"web_search","arguments":"{\"query\":\"segunda\"}"}}]},"finish_reason":"tool_calls"}]}`)
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	options := processing.Default()
	options.Provider, options.API = "api", connection
	result, err := client.process(ctx, processing.Request{Text: "Pesquise.", Options: options},
		func(context.Context, string) (string, []processing.Source, error) {
			searches.Add(1)
			cancel()
			return "Dado encontrado.", []processing.Source{{URL: "https://example.com"}}, nil
		})
	if !errors.Is(err, context.Canceled) || searches.Load() != 1 || modelCalls.Load() != 1 || result.Text != "" {
		t.Fatal("a busca continuou enviando depois de cancelar", result, err, searches.Load(), modelCalls.Load())
	}
}
