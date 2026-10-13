package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"ai-talker/internal/processing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/aws/smithy-go"
)

func searchResponse(id, name string, input any) *bedrockruntime.ConverseOutput {
	response := completeResponse("", types.StopReasonToolUse)
	message := response.Output.(*types.ConverseOutputMemberMessage)
	message.Value.Content = []types.ContentBlock{&types.ContentBlockMemberToolUse{Value: types.ToolUseBlock{
		Name: aws.String(name), ToolUseId: aws.String(id), Input: document.NewLazyDocument(input),
	}}}
	return response
}

func citationResponse() *bedrockruntime.ConverseOutput {
	response := completeResponse("Informação atual. ", types.StopReasonEndTurn)
	message := response.Output.(*types.ConverseOutputMemberMessage)
	message.Value.Content = append(message.Value.Content, &types.ContentBlockMemberCitationsContent{
		Value: types.CitationsContentBlock{
			Content: []types.CitationGeneratedContent{&types.CitationGeneratedContentMemberText{Value: "Dado citado."}},
			Citations: []types.Citation{
				{Title: aws.String("Fonte oficial"), Location: &types.CitationLocationMemberWeb{Value: types.WebLocation{Url: aws.String("https://www.bcb.gov.br/")}}},
				{Title: aws.String("Fonte repetida"), Location: &types.CitationLocationMemberWeb{Value: types.WebLocation{Url: aws.String("https://www.bcb.gov.br/")}}},
				{Location: &types.CitationLocationMemberWeb{Value: types.WebLocation{Url: aws.String("file:///private")}}},
			},
		},
	})
	return response
}

func TestProcessingWebSearchReturnsToolResultToSelectedModelAndKeepsSources(t *testing.T) {
	t.Parallel()
	options := processing.Default()
	options.Mode = processing.ModeFree
	request := processing.Request{Text: "  Faça web search sobre o dólar.\nInforme a data.  ", Options: options}
	calls := 0
	client := converseFunc(func(ctx context.Context, input *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
		calls++
		if aws.ToString(input.ModelId) != options.ModelID || len(input.System) != 0 || input.ToolConfig == nil {
			t.Fatal("a busca trocou o modelo, mudou o modo Livre ou não ofereceu a ferramenta")
		}
		tool := input.ToolConfig.Tools[0].(*types.ToolMemberToolSpec)
		if aws.ToString(tool.Value.Name) != "web_search" {
			t.Fatal("a ferramenta de busca não foi oferecida")
		}
		if input.Messages[0].Content[0].(*types.ContentBlockMemberText).Value != request.Text {
			t.Fatal("o prompt da transcrição foi alterado")
		}
		response := completeResponse("Resposta com cotação e data.", types.StopReasonEndTurn)
		response.Usage = &types.TokenUsage{InputTokens: aws.Int32(10), OutputTokens: aws.Int32(5)}
		if calls == 1 {
			response.Output = searchResponse("query-1", "web_search", map[string]any{"query": "cotação dólar comercial hoje fonte oficial"}).Output
			response.StopReason = types.StopReasonToolUse
		} else if calls == 2 {
			if len(input.Messages) != 3 || input.Messages[1].Role != types.ConversationRoleAssistant {
				t.Fatal("o modelo perdeu o turno que solicitou a busca")
			}
			result := input.Messages[2].Content[0].(*types.ContentBlockMemberToolResult)
			if aws.ToString(result.Value.ToolUseId) != "query-1" || result.Value.Status != "" {
				t.Fatal("o resultado perdeu seu ID ou enviou um status incompatível com OpenAI")
			}
			payload, err := result.Value.Content[0].(*types.ToolResultContentBlockMemberJson).Value.MarshalSmithyDocument()
			if err != nil || !strings.Contains(string(payload), "https://www.bcb.gov.br/") || !strings.Contains(string(payload), "Dado pesquisado") {
				t.Fatalf("o resultado da busca não chegou ao modelo: %s, %v", payload, err)
			}
		} else {
			t.Fatal("o modelo foi chamado além da resposta final")
		}
		return response, nil
	})
	searchCalls := 0
	result, err := processTextWithSearch(t.Context(), "isolated", client, request, func(ctx context.Context, query string) (webResult, error) {
		searchCalls++
		if query != "cotação dólar comercial hoje fonte oficial" {
			t.Fatal("a consulta do modelo não chegou à busca")
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("a ferramenta não herdou o prazo da operação")
		}
		return webResult{Text: "Dado pesquisado, com data e tipo de cotação.", Sources: []processing.Source{
			{Title: "Banco Central", URL: "https://www.bcb.gov.br/"},
		}, Usage: map[string]any{"inputTokens": int32(30)}}, nil
	})
	if err != nil || calls != 2 || searchCalls != 1 || len(result.Sources) != 1 ||
		!strings.Contains(result.Text, "Fontes:\nBanco Central · https://www.bcb.gov.br/") ||
		result.Usage["inputTokens"] != int64(20) || result.Usage["webSearch"] == nil {
		t.Fatalf("resultado=%+v, erro=%v, chamadas=%d/%d", result, err, calls, searchCalls)
	}
}

func TestGroundedSearchUsesNovaUSSystemToolAndExtractsCitations(t *testing.T) {
	t.Parallel()
	client := converseFunc(func(_ context.Context, input *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
		if aws.ToString(input.ModelId) != groundingModelID || input.ToolConfig == nil ||
			aws.ToString(input.ToolConfig.Tools[0].(*types.ToolMemberSystemTool).Value.Name) != "nova_grounding" ||
			!strings.HasSuffix(input.Messages[0].Content[0].(*types.ContentBlockMemberText).Value, "\n\nCotação com data") {
			t.Fatal("a busca não usou o perfil US e a ferramenta nativa de grounding")
		}
		return citationResponse(), nil
	})
	result, err := groundedSearch(t.Context(), "isolated", client, "Cotação com data")
	if err != nil || result.Text != "Informação atual. Dado citado." || len(result.Sources) != 1 {
		t.Fatalf("o conteúdo citado foi perdido ou duplicado: %+v, %v", result, err)
	}
}

func TestWebSearchRejectsUnexpectedToolsAndInvalidArgumentsBeforeExecuting(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		tool string
		args any
	}{
		{"ferramenta desconhecida", "run_command", map[string]any{"query": "ignored"}},
		{"consulta vazia", "web_search", map[string]any{"query": "  "}},
		{"consulta longa", "web_search", map[string]any{"query": strings.Repeat("á", maxQueryRunes+1)}},
		{"campo desconhecido", "web_search", map[string]any{"query": "fonte oficial", "execute": "ignored"}},
		{"tipo incorreto", "web_search", map[string]any{"query": []string{"x"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := converseFunc(func(context.Context, *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
				return searchResponse("1", tc.tool, tc.args), nil
			})
			_, err := processTextWithSearch(t.Context(), "isolated", client, processing.Request{Text: "Pedido", Options: processing.Default()},
				func(context.Context, string) (webResult, error) {
					t.Fatal("uma ferramenta ou consulta inválida foi executada")
					return webResult{}, nil
				})
			if !errors.Is(err, ErrIncompleteResponse) {
				t.Fatalf("a chamada inválida foi aceita: %v", err)
			}
		})
	}
}

func TestWebSearchBoundsRepeatedToolRequests(t *testing.T) {
	t.Parallel()
	for _, duplicate := range []bool{false, true} {
		t.Run(fmt.Sprint(duplicate), func(t *testing.T) {
			calls, searches := 0, 0
			client := converseFunc(func(context.Context, *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
				calls++
				id := fmt.Sprint(calls)
				if duplicate {
					id = "duplicate"
				}
				return searchResponse(id, "web_search", map[string]any{"query": "Consulta"}), nil
			})
			result, err := processTextWithSearch(t.Context(), "isolated", client, processing.Request{Text: "Pedido", Options: processing.Default()},
				func(context.Context, string) (webResult, error) {
					searches++
					return webResult{Text: "Dado", Sources: []processing.Source{{URL: "https://example.com"}}}, nil
				})
			expected, expectedCalls := 1, maxWebSearches+1
			if duplicate {
				expectedCalls = 2
			}
			if !errors.Is(err, ErrIncompleteResponse) || searches != expected || calls != expectedCalls || result.Text != "" {
				t.Fatalf("o loop não foi limitado: chamadas=%d buscas=%d resultado=%+v erro=%v", calls, searches, result, err)
			}
		})
	}
}

func TestWebSearchDisabledDoesNotDispatchAndMergeOffersNoTools(t *testing.T) {
	t.Parallel()
	for _, merge := range []bool{false, true} {
		options := processing.Default()
		options.WebSearchDisabled = true
		if merge {
			options = processing.DefaultMerge().Options()
		}
		client := converseFunc(func(context.Context, *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
			return searchResponse("1", "web_search", map[string]any{"query": "Consulta"}), nil
		})
		_, err := processTextWithSearch(t.Context(), "isolated", client, processing.Request{Text: "Pedido", Options: options},
			func(context.Context, string) (webResult, error) {
				t.Fatal("a busca desativada foi executada")
				return webResult{}, nil
			})
		if !errors.Is(err, ErrIncompleteResponse) {
			t.Fatalf("a ferramenta desativada foi aceita: %v", err)
		}
	}
}

func TestWebSearchFailureAndCancellationKeepIncompleteAnswerOut(t *testing.T) {
	t.Parallel()
	for _, cancelRequest := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		searchFailure := errors.New("falha da ferramenta")
		client := converseFunc(func(context.Context, *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
			return searchResponse("1", "web_search", map[string]any{"query": "Consulta"}), nil
		})
		result, err := processTextWithSearch(ctx, "isolated", client, processing.Request{Text: "Pedido", Options: processing.Default()},
			func(context.Context, string) (webResult, error) {
				if cancelRequest {
					cancel()
					return webResult{Text: "Resposta tardia", Sources: []processing.Source{{URL: "https://example.com"}}}, nil
				}
				return webResult{}, searchFailure
			})
		cancel()
		expected := searchFailure
		if cancelRequest {
			expected = context.Canceled
		}
		if !errors.Is(err, expected) || result.Text != "" {
			t.Fatalf("a falha perdeu a causa ou publicou texto: %+v, %v", result, err)
		}
	}
}

func TestGroundingPermissionErrorIsReadableAndKeepsCause(t *testing.T) {
	t.Parallel()
	cause := &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "private diagnostic"}
	client := converseFunc(func(context.Context, *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
		return nil, cause
	})
	_, err := groundedSearch(t.Context(), "isolated", client, "Consulta")
	if !errors.Is(err, cause) || !strings.Contains(err.Error(), "bedrock:InvokeTool") ||
		strings.Contains(err.Error(), "private diagnostic") {
		t.Fatalf("a falha não indica a permissão necessária ou vazou o diagnóstico: %v", err)
	}
}

func TestSourcesRemainInPlainTextAndJSON(t *testing.T) {
	t.Parallel()
	result := processing.Result{
		Text:    "Já citada https://example.com/a",
		Sources: []processing.Source{{Title: "Primeira", URL: "https://example.com/a"}, {Title: "Segunda", URL: "https://example.com/b"}},
	}
	result.Text = withSources(result.Text, result.Sources)
	if strings.Count(result.Text, "https://example.com/a") != 1 || !strings.Contains(result.Text, "https://example.com/b") {
		t.Fatal("as fontes se perderam ou foram duplicadas no texto copiável")
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var restored processing.Result
	if err := json.Unmarshal(data, &restored); err != nil || len(restored.Sources) != 2 || restored.Text != result.Text {
		t.Fatal("as fontes não sobreviveram à persistência")
	}
}
