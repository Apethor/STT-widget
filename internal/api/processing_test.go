package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"ai-talker/internal/apiconfig"
	"ai-talker/internal/processing"
)

func TestAzureChatParametersAreIndependentOfDeploymentName(t *testing.T) {
	t.Parallel()
	for _, deployment := range []string{"producao", "gpt-5-chat-name-chosen-by-user"} {
		t.Run(deployment, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if body["model"] != deployment || body["max_completion_tokens"] != float64(8192) ||
					body["max_tokens"] != nil || body["reasoning_effort"] != "high" {
					t.Errorf("as opções explícitas dependeram do nome do deployment: %#v", body)
				}
				io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"Resposta."},"finish_reason":"stop"}]}`)
			}))
			defer server.Close()
			connection := apiconfig.Connection{Provider: apiconfig.Azure, Endpoint: server.URL + "/openai/v1"}
			client, err := NewClient(connection, memoryVault{connection.CredentialTarget(), fakeKey}, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			options := processing.Default()
			options.Provider, options.API, options.ModelID = "api", connection, deployment
			options.Mode, options.WebSearchDisabled, options.ReasoningEffort = processing.ModeFree, true, "high"
			result, err := client.process(t.Context(), processing.Request{Text: "Pedido.", Options: options}, nil)
			if err != nil || result.Text != "Resposta." {
				t.Fatalf("o pedido configurado não concluiu: %+v / %v", result, err)
			}
		})
	}
}

func TestAPIChatUsesExplicitOutputParameterAndAutomaticReasoning(t *testing.T) {
	t.Parallel()
	for _, parameter := range []string{processing.OutputMaxTokens, processing.OutputMaxCompletionTokens} {
		t.Run(parameter, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				other := processing.OutputMaxTokens
				if parameter == other {
					other = processing.OutputMaxCompletionTokens
				}
				if body[parameter] != float64(8192) || body[other] != nil || body["reasoning_effort"] != nil {
					t.Errorf("o deployment não recebeu exatamente as opções configuradas: %#v", body)
				}
				io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"Compatível."},"finish_reason":"stop"}]}`)
			}))
			defer server.Close()
			connection := apiconfig.Connection{Provider: apiconfig.Azure, Endpoint: server.URL + "/openai/v1"}
			client, err := NewClient(connection, memoryVault{connection.CredentialTarget(), fakeKey}, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			options := processing.Default()
			options.Provider, options.API, options.ModelID = "api", connection, "deployment-livre"
			options.Mode, options.WebSearchDisabled = processing.ModeFree, true
			options.OutputTokenParameter = parameter
			result, err := client.process(t.Context(), processing.Request{Text: "Pedido.", Options: options}, nil)
			if err != nil || result.Text != "Compatível." {
				t.Fatalf("a opção de limite configurada não concluiu: %+v / %v", result, err)
			}
		})
	}
}
