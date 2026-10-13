//go:build awsintegration

package cloud

import (
	"os"
	"strings"
	"testing"

	"ai-talker/internal/config"
	"ai-talker/internal/processing"
)

// This opt-in integration sends two synthetic texts only. Routine tests never
// retrieve user credentials or invoke a real model.
func TestTextProcessingBedrockIntegration(t *testing.T) {
	if os.Getenv("AI_TALKER_TEST_PROCESSING_AWS") != "1" {
		t.Skip("ative explicitamente AI_TALKER_TEST_PROCESSING_AWS=1 para duas chamadas sintéticas")
	}
	settings := config.Default()
	settings.Profile = config.DefaultProfile()
	if selected := os.Getenv("AI_TALKER_TEST_PROFILE"); selected != "" {
		settings.Profile = selected
	}
	if selected := os.Getenv("AI_TALKER_TEST_REGION"); selected != "" {
		settings.Region = selected
	}
	for _, tc := range []struct {
		name string
		mode string
		text string
	}{
		{
			name: "Teams", mode: processing.ModeTeams,
			text: "Quero mandar uma mensagem para o Mendes num tom cordial dizendo que a revisão da proposta terminou e que consigo enviar hoje até as 15h.",
		},
		{
			name: "Livre", mode: processing.ModeFree,
			text: "Responda apenas com as três palavras a seguir, sem pontuação: TESTE LIVRE OK",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			options := processing.Default()
			options.Mode = tc.mode
			result, err := Process(t.Context(), settings, processing.Request{Text: tc.text, Options: options})
			if err != nil {
				t.Fatal(err)
			}
			if tc.mode == processing.ModeFree {
				if result.Text != "TESTE LIVRE OK" {
					t.Fatalf("o modo Livre não seguiu o pedido sintético: %q", result.Text)
				}
			} else {
				lower := strings.ToLower(result.Text)
				if !strings.Contains(lower, "mendes") || !strings.Contains(lower, "revis") ||
					!strings.Contains(lower, "15") || len(result.Text) > 1500 {
					t.Fatalf("a mensagem perdeu destinatário, conteúdo ou horário: %q", result.Text)
				}
			}
			t.Logf("modelo=%s modo=%s uso=%v texto=%q", result.ModelID, result.Mode, result.Usage, result.Text)
		})
	}
}
