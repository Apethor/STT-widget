//go:build awsintegration

package cloud

import (
	"errors"
	"os"
	"strings"
	"testing"

	"ai-talker/internal/config"
	"ai-talker/internal/processing"
	"github.com/aws/smithy-go"
)

// Opt-in smoke checks use synthetic writing tasks only. These single requests
// verify access and parameter compatibility; they are not a latency benchmark.
func TestFastTextModelsBedrockIntegration(t *testing.T) {
	if os.Getenv("AI_TALKER_TEST_TEXT_MODELS_AWS") != "1" {
		t.Skip("ative AI_TALKER_TEST_TEXT_MODELS_AWS=1 para quatro pedidos sintéticos")
	}
	settings := config.Default()
	settings.Profile = config.DefaultProfile()
	if selected := os.Getenv("AI_TALKER_TEST_PROFILE"); selected != "" {
		settings.Profile = selected
	}
	for _, tc := range []struct {
		name   string
		model  string
		effort string
		merge  bool
	}{
		{name: "Nova 2 Lite", model: "global.amazon.nova-2-lite-v1:0"},
		{name: "Qwen3 Next", model: "qwen.qwen3-next-80b-a3b"},
		{name: "gpt-oss-120b low", model: "openai.gpt-oss-120b-1:0", effort: "low"},
		{name: "Sol 6.1 medium merge", model: "global.openai.gpt-6.1-sol", effort: "medium", merge: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			options := processing.Default()
			options.ModelID, options.ReasoningEffort = tc.model, tc.effort
			text := "Quero mandar uma mensagem para o Mendes num tom cordial dizendo que a revisão da proposta terminou e que consigo enviar hoje até as 15h."
			if tc.merge {
				options = processing.DefaultMerge().Options()
				options.ReasoningEffort = tc.effort
				text = `[{"aba":"Aba 1","texto":"Oi Mendes, a revisão da proposta terminou."},` +
					`{"aba":"Aba 2","texto":"Consigo enviar hoje até as 15h. Use um tom cordial."}]`
			}
			result, err := Process(t.Context(), settings, processing.Request{Text: text, Options: options})
			if err != nil {
				var apiError smithy.APIError
				if errors.As(err, &apiError) && apiError.ErrorCode() == "ValidationException" {
					t.Logf("compatibilidade dos parâmetros: %s", apiError.ErrorMessage())
				}
				t.Fatal(err)
			}
			lower := strings.ToLower(result.Text)
			if !strings.Contains(lower, "mendes") || !strings.Contains(lower, "revis") ||
				!strings.Contains(lower, "15") || len(result.Text) > 1500 {
				t.Fatalf("a resposta perdeu conteúdo ou não ficou pronta para enviar: %q", result.Text)
			}
			t.Logf("modelo=%s effort=%q duração=%.3fs uso=%v texto=%q",
				result.ModelID, tc.effort, result.ElapsedSeconds, result.Usage, result.Text)
		})
	}
}
