package processing

import (
	"encoding/json"
	"strings"
	"testing"

	"ai-talker/internal/apiconfig"
)

func TestRequestValidationPreservesFreePromptAndBoundsInput(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		change func(*Request)
		valid  bool
	}{
		{name: "teams", valid: true, change: func(*Request) {}},
		{name: "livre sem instrução", valid: true, change: func(r *Request) {
			r.Options.Mode = ModeFree
			r.Options.TeamsPrompt = ""
		}},
		{name: "teams sem instrução", change: func(r *Request) { r.Options.TeamsPrompt = " " }},
		{name: "texto vazio", change: func(r *Request) { r.Text = " \n" }},
		{name: "texto longo", change: func(r *Request) { r.Text = strings.Repeat("ç", MaxTextRunes+1) }},
		{name: "instrução longa", change: func(r *Request) { r.Options.TeamsPrompt = strings.Repeat("a", MaxPromptRunes+1) }},
		{name: "modelo vazio", change: func(r *Request) { r.Options.ModelID = " " }},
		{name: "modelo com newline", change: func(r *Request) { r.Options.ModelID = "model\nother" }},
		{name: "modo inválido", change: func(r *Request) { r.Options.Mode = "other" }},
		{name: "mesclagem", valid: true, change: func(r *Request) { r.Options = DefaultMerge().Options() }},
		{name: "raciocínio Sol médio", valid: true, change: func(r *Request) {
			r.Options.ModelID = "global.openai.gpt-6.1-sol"
			r.Options.ReasoningEffort = "medium"
		}},
		{name: "raciocínio incompatível", change: func(r *Request) {
			r.Options.ModelID = "qwen.qwen3-next-80b-a3b"
			r.Options.ReasoningEffort = "high"
		}},
		{name: "raciocínio desconhecido", change: func(r *Request) {
			r.Options.ReasoningEffort = "unlimited"
		}},
		{name: "limite completion explícito", valid: true, change: func(r *Request) {
			r.Options.OutputTokenParameter = OutputMaxCompletionTokens
		}},
		{name: "limite tokens explícito", valid: true, change: func(r *Request) {
			r.Options.OutputTokenParameter = OutputMaxTokens
		}},
		{name: "parâmetro de saída inválido", change: func(r *Request) {
			r.Options.OutputTokenParameter = "max_output_tokens"
		}},
		{name: "parâmetro de mesclar inválido", change: func(r *Request) {
			r.Options.MergeOutputTokenParameter = "unknown"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := Request{Text: "  Texto com português.\nSegunda linha. ", Options: Default()}
			tc.change(&request)
			original := request.Text
			err := request.Validate()
			if (err == nil) != tc.valid {
				t.Fatalf("validação = %v, validade esperada = %v", err, tc.valid)
			}
			if request.Text != original {
				t.Fatal("validar alterou o texto que será enviado ao modelo")
			}
		})
	}
}

func TestOptionsKeepAPIOutputParametersIndependentThroughPersistenceAndMerge(t *testing.T) {
	t.Parallel()
	options := Default().WithLegacyMerge(DefaultMerge())
	options.Provider = "api"
	options.API = apiconfig.Connection{Provider: apiconfig.Azure, Endpoint: "https://recurso.openai.azure.com/openai/v1"}
	options.ModelID, options.MergeModelID = "chat-producao", "merge-producao"
	options.MergeNeedsReview = false
	options.OutputTokenParameter, options.MergeOutputTokenParameter = OutputMaxTokens, OutputMaxCompletionTokens
	options.ReasoningEffort, options.MergeReasoning = "", "high"
	encoded, err := json.Marshal(options)
	if err != nil {
		t.Fatal(err)
	}
	var restored Options
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	merged, err := restored.ForMerge(DefaultMerge())
	if err != nil {
		t.Fatal(err)
	}
	if restored.OutputTokenParameter != OutputMaxTokens || merged.OutputTokenParameter != OutputMaxCompletionTokens ||
		restored.ReasoningEffort != "" || merged.ReasoningEffort != "high" ||
		merged.ModelID != "merge-producao" || restored.ModelID != "chat-producao" {
		t.Fatalf("Processar/Mesclar perderam a seleção independente: %+v / %+v", restored, merged)
	}
}
