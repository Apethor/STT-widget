package cloud

import (
	"context"
	"testing"

	"ai-talker/internal/processing"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

func TestProcessingContinuationSendsAlternatingCompletedTurns(t *testing.T) {
	t.Parallel()
	options := processing.Default()
	options.Mode = processing.ModeFree
	options.WebSearchDisabled = true
	request := processing.Request{
		Text: "  Novo prompt exato.\nMais detalhes.  ", Options: options,
		History: []processing.Turn{
			{Prompt: "Primeiro pedido.", Response: "Primeira resposta."},
			{Prompt: "Segundo pedido.", Response: "Segunda resposta editada."},
		},
	}
	want := []string{"Primeiro pedido.", "Primeira resposta.", "Segundo pedido.", "Segunda resposta editada.", request.Text}
	client := converseFunc(func(_ context.Context, input *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
		if len(input.Messages) != len(want) || len(input.System) != 0 {
			t.Fatal("o contexto não preservou o modo Livre")
		}
		for index, message := range input.Messages {
			role := types.ConversationRoleUser
			if index%2 == 1 {
				role = types.ConversationRoleAssistant
			}
			if message.Role != role || len(message.Content) != 1 ||
				message.Content[0].(*types.ContentBlockMemberText).Value != want[index] {
				t.Fatal("o contexto ou o prompt novo foi modificado")
			}
		}
		return completeResponse("Resposta nova.", types.StopReasonEndTurn), nil
	})
	if result, err := processText(t.Context(), "test", client, request); err != nil || result.Text != "Resposta nova." {
		t.Fatal("a continuação falhou:", err)
	}
}
