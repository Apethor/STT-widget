package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"ai-talker/internal/processing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

func TestProcessTextSendsSelectedModeAndExactEditor(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{processing.ModeTeams, processing.ModeFree, processing.ModeMerge} {
		t.Run(mode, func(t *testing.T) {
			request := processing.Request{
				Text: "  Quero falar para o Mendes num tom cordial.\nAvise que a revisão ficou pronta.  ",
				Options: processing.Options{
					ModelID: "my-selected-model", Mode: mode, TeamsPrompt: "Minha instrução editada.",
					WebSearchDisabled: true,
				},
			}
			client := converseFunc(func(ctx context.Context, input *bedrockruntime.ConverseInput) (
				*bedrockruntime.ConverseOutput, error,
			) {
				if aws.ToString(input.ModelId) != request.Options.ModelID || len(input.Messages) != 1 {
					t.Fatal("a seleção do modelo ou o isolamento da conversa foi perdido")
				}
				message := input.Messages[0]
				if message.Role != types.ConversationRoleUser || len(message.Content) != 1 {
					t.Fatal("o texto deveria ser uma única mensagem do usuário")
				}
				content, ok := message.Content[0].(*types.ContentBlockMemberText)
				if !ok || content.Value != request.Text {
					t.Fatal("o texto do editor foi alterado ou recebeu um wrapper")
				}
				if input.ToolConfig != nil {
					t.Fatal("o processamento não deve oferecer ferramentas ao modelo")
				}
				if mode == processing.ModeFree {
					if len(input.System) != 0 {
						t.Fatal("o modo Livre recebeu uma instrução adicional")
					}
				} else if len(input.System) != 1 ||
					input.System[0].(*types.SystemContentBlockMemberText).Value != request.Options.TeamsPrompt {
					t.Fatal("o modo Teams perdeu a instrução editável")
				}
				deadline, bounded := ctx.Deadline()
				if !bounded || time.Until(deadline) > callTimeout {
					t.Fatal("a chamada de texto não tem prazo")
				}
				return completeResponse("Olá, Mendes! A revisão ficou pronta.", types.StopReasonEndTurn), nil
			})
			result, err := processText(t.Context(), "test", client, request)
			if err != nil || result.Text != "Olá, Mendes! A revisão ficou pronta." ||
				result.ModelID != request.Options.ModelID || result.Mode != mode {
				t.Fatalf("resultado = %+v, erro = %v", result, err)
			}
		})
	}
}

func TestProcessTextSendsProviderSpecificReasoningFields(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		model  string
		effort string
	}{
		{name: "automático", model: processing.DefaultModelID},
		{name: "Sol alto", model: "global.openai.gpt-6.1-sol", effort: "high"},
		{name: "OSS baixo", model: "openai.gpt-oss-120b-1:0", effort: "low"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			options := processing.Default()
			options.ModelID, options.ReasoningEffort = tc.model, tc.effort
			client := converseFunc(func(_ context.Context, input *bedrockruntime.ConverseInput) (
				*bedrockruntime.ConverseOutput, error,
			) {
				if tc.effort == "" {
					if input.AdditionalModelRequestFields != nil {
						t.Fatal("Automático enviou parâmetros extras")
					}
				} else {
					var fields map[string]any
					payload, err := input.AdditionalModelRequestFields.MarshalSmithyDocument()
					if err != nil {
						t.Fatal(err)
					}
					if err := json.Unmarshal(payload, &fields); err != nil {
						t.Fatal(err)
					}
					if strings.Contains(tc.model, "gpt-oss-") {
						if fields["reasoning_effort"] != tc.effort {
							t.Fatal("o modelo OSS perdeu reasoning_effort")
						}
					} else if fields["reasoning"].(map[string]any)["effort"] != tc.effort {
						t.Fatal("o modelo Sol perdeu reasoning.effort")
					}
				}
				return completeResponse("Resposta completa.", types.StopReasonEndTurn), nil
			})
			if _, err := processText(t.Context(), "isolated", client, processing.Request{Text: "Olá.", Options: options}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestProcessTextRejectsEmptyInvalidAndCancelledRequestsBeforeCalling(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		text string
		mode string
	}{
		{name: "texto vazio", text: " \n", mode: processing.ModeTeams},
		{name: "modo desconhecido", text: "Olá.", mode: "unknown"},
		{name: "texto longo", text: strings.Repeat("á", processing.MaxTextRunes+1), mode: processing.ModeFree},
	} {
		t.Run(tc.name, func(t *testing.T) {
			options := processing.Default()
			options.Mode = tc.mode
			client := converseFunc(func(context.Context, *bedrockruntime.ConverseInput) (
				*bedrockruntime.ConverseOutput, error,
			) {
				t.Fatal("um pedido inválido acionou o modelo")
				return nil, nil
			})
			if _, err := processText(t.Context(), "test", client, processing.Request{Text: tc.text, Options: options}); err == nil {
				t.Fatal("um pedido inválido foi aceito")
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := processText(ctx, "test", nil, processing.Request{
		Text: "Olá.", Options: processing.Default(),
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("o cancelamento não foi preservado: %v", err)
	}
}

func TestProcessTextRejectsTruncatedEmptyAndToolResponses(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		response *bedrockruntime.ConverseOutput
	}{
		{name: "sem resposta"},
		{name: "texto vazio", response: completeResponse(" ", types.StopReasonEndTurn)},
		{name: "truncada", response: completeResponse("Resposta cortada.", types.StopReasonMaxTokens)},
		{name: "ferramenta", response: &bedrockruntime.ConverseOutput{
			StopReason: types.StopReasonEndTurn,
			Output: &types.ConverseOutputMemberMessage{Value: types.Message{
				Content: []types.ContentBlock{&types.ContentBlockMemberToolUse{}},
			}},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := converseFunc(func(context.Context, *bedrockruntime.ConverseInput) (
				*bedrockruntime.ConverseOutput, error,
			) {
				return tc.response, nil
			})
			result, err := processText(t.Context(), "test", client, processing.Request{
				Text: "Olá.", Options: processing.Default(),
			})
			if !errors.Is(err, ErrIncompleteResponse) || result.Text != "" {
				t.Fatalf("uma resposta incompleta foi aceita: %+v, %v", result, err)
			}
		})
	}
}
