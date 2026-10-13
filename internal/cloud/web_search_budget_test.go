package cloud

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"ai-talker/internal/processing"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

func TestWebBudgetFinishesWithSourcesWithoutPortableToolNone(t *testing.T) {
	for _, limit := range []int{1, 4, 20} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			calls, searches := 0, 0
			client := converseFunc(func(_ context.Context, input *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
				calls++
				if input.ToolConfig != nil {
					return searchResponse(fmt.Sprint(calls), "web_search", map[string]any{"query": fmt.Sprintf("Consulta %d", calls)}), nil
				}
				for _, message := range input.Messages {
					for _, block := range message.Content {
						switch block.(type) {
						case *types.ContentBlockMemberToolUse, *types.ContentBlockMemberToolResult:
							t.Fatal("a conclusão manteve blocos de ferramenta sem ToolConfig")
						}
					}
				}
				last := input.Messages[len(input.Messages)-1]
				if len(last.Content) != 2 || last.Content[0].(*types.ContentBlockMemberText).Value != "  Pedido exato.  " ||
					!strings.Contains(last.Content[1].(*types.ContentBlockMemberText).Value, "https://example.com/fonte") {
					t.Fatal("a conclusão perdeu o prompt ou as fontes encontradas")
				}
				return completeResponse("Resposta baseada nas fontes.", types.StopReasonEndTurn), nil
			})
			options := processing.Default()
			options.Mode = processing.ModeFree
			result, err := processTextWithSearch(t.Context(), "isolated", client,
				processing.Request{Text: "  Pedido exato.  ", Options: options, WebSearchLimit: limit},
				func(context.Context, string) (webResult, error) {
					searches++
					return webResult{Text: "Referência confirmada.", Sources: []processing.Source{{URL: "https://example.com/fonte"}}}, nil
				})
			if err != nil || calls != limit+1 || searches != limit || len(result.Sources) != 1 ||
				!strings.Contains(result.Text, "Limite de buscas") || result.Usage["webSearchLimited"] != true {
				t.Fatalf("o orçamento virou erro em vez de concluir: %+v / %v / %d/%d", result, err, calls, searches)
			}
		})
	}
}

func TestDuplicateQueriesReuseSearchAndStillReachBoundedFinalAnswer(t *testing.T) {
	calls, searches := 0, 0
	client := converseFunc(func(_ context.Context, input *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
		calls++
		if input.ToolConfig == nil {
			return completeResponse("Resposta final.", types.StopReasonEndTurn), nil
		}
		query := "  TAXA   atual  "
		if calls%2 == 0 {
			query = "taxa atual"
		}
		return searchResponse(fmt.Sprint(calls), "web_search", map[string]any{"query": query}), nil
	})
	result, err := processTextWithSearch(t.Context(), "isolated", client,
		processing.Request{Text: "Pedido.", Options: processing.Default(), WebSearchLimit: 4},
		func(context.Context, string) (webResult, error) {
			searches++
			return webResult{Text: "Dado confirmado.", Sources: []processing.Source{{URL: "https://example.com"}}}, nil
		})
	if err != nil || calls != 5 || searches != 1 || !strings.Contains(result.Text, "Resposta final.") {
		t.Fatalf("a consulta duplicada gastou buscas ou deixou um loop: %+v / %v / %d/%d", result, err, calls, searches)
	}
}

func TestCancellationBeforeWebFinalizationDoesNotInvokeAnotherModelCall(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := 0
	client := converseFunc(func(context.Context, *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
		calls++
		return searchResponse(fmt.Sprint(calls), "web_search", map[string]any{"query": "Fonte atual"}), nil
	})
	request := processing.Request{Text: "Pedido.", Options: processing.Default(), OnProgress: func(message string) {
		if message == "Preparando a resposta com as fontes…" {
			cancel()
		}
	}}
	result, err := processTextWithSearch(ctx, "isolated", client, request,
		func(context.Context, string) (webResult, error) {
			return webResult{Text: "Dado confirmado.", Sources: []processing.Source{{URL: "https://example.com"}}}, nil
		})
	if !errors.Is(err, context.Canceled) || calls != 1 || result.Text != "" {
		t.Fatalf("cancelar entre etapas enviou outra chamada: %+v / %v / %d", result, err, calls)
	}
}
