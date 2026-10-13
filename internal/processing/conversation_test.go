package processing

import (
	"strings"
	"testing"
)

func TestConversationValidationRejectsInvalidAndOversizedHistory(t *testing.T) {
	t.Parallel()
	for _, history := range [][]Turn{
		{{Prompt: "", Response: "resposta"}},
		{{Prompt: "pedido", Response: string([]byte{0xff})}},
		make([]Turn, MaxConversationTurns),
		{{Prompt: strings.Repeat("a", MaxConversationRunes), Response: "resposta"}},
	} {
		request := Request{Text: "Novo pedido.", Options: Default(), History: history}
		if request.Validate() == nil {
			t.Fatal("a conversa inválida foi aceita")
		}
	}
	request := Request{
		Text: "  Novo pedido exato.  ", Options: Default(),
		History: []Turn{{Prompt: "Pedido anterior.", Response: "Resposta anterior."}},
	}
	if err := request.Validate(); err != nil || request.Text != "  Novo pedido exato.  " {
		t.Fatal("a validação alterou um pedido válido:", err)
	}
}
