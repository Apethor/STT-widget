package desktop

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"ai-talker/internal/config"
	"ai-talker/internal/processing"

	"fyne.io/fyne/v2/test"
)

func TestNewAudioKeepsCompletedContextAndEditedAnswerAcrossSeveralTurns(t *testing.T) {
	requests := make(chan processing.Request, 1)
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.process = func(_ context.Context, _ config.Settings, request processing.Request) (processing.Result, error) {
			requests <- request
			return processing.Result{Text: "Resposta: " + request.Text}, nil
		}
	})
	doc := h.d.document
	history := []processing.Turn{}
	for index := 1; index <= 4; index++ {
		prompt := fmt.Sprintf("Pedido %d", index)
		if index > 1 {
			h.d.newAudio()
		}
		doc.editor.SetText(prompt)
		if index == 1 {
			test.Tap(doc.processButton)
		} else {
			test.Tap(doc.processFollowButton)
		}
		request := waitValue(t, requests)
		h.await(func() bool { return !doc.processBusy })
		if request.Text != prompt || !slices.Equal(request.History, history) {
			t.Fatalf("Novo áudio perdeu a conversa ou a resposta editada: %+v; esperado %+v", request, history)
		}
		response := fmt.Sprintf("Resposta %d editada.", index)
		doc.processEditor.SetText(response)
		history = append(history, processing.Turn{Prompt: prompt, Response: response})
	}
	if err := h.d.saveWorkspace(); err != nil {
		t.Fatal(err)
	}
	if !h.d.loadWorkspace() {
		t.Fatal("a conversa não reabriu")
	}
	if !slices.Equal(h.d.document.processHistory, history) {
		t.Fatal("a persistência perdeu as respostas editadas")
	}
}

func TestConversationContinuesWithNewPromptEditedAnswerAndOwnTab(t *testing.T) {
	requests := make(chan processing.Request, 4)
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.process = func(_ context.Context, _ config.Settings, request processing.Request) (processing.Result, error) {
			requests <- request
			return processing.Result{Text: "Resposta: " + request.Text}, nil
		}
	})
	first := h.d.document
	first.editor.SetText("Primeiro pedido.")
	test.Tap(first.processButton)
	initial := waitValue(t, requests)
	h.await(func() bool { return !first.processBusy })
	if len(initial.History) != 0 || len(first.processHistory) != 1 || first.processFollowButton.Disabled() {
		t.Fatal("Processar não iniciou uma conversa nova")
	}
	first.processEditor.SetText("Resposta editada pelo usuário.")
	test.Tap(first.clearButton)
	first.editor.SetText("Novo pedido para continuar.")
	second := h.d.addDocument()
	second.editor.SetText("Pedido de outra aba.")
	h.d.continueProcessing(first)
	follow := waitValue(t, requests)
	h.await(func() bool { return !first.processBusy })
	if follow.Text != "Novo pedido para continuar." ||
		!slices.Equal(follow.History, []processing.Turn{{Prompt: "Primeiro pedido.", Response: "Resposta editada pelo usuário."}}) ||
		len(second.processHistory) != 0 || second.processEditor.Text != "" ||
		len(first.processHistory) != 2 {
		t.Fatal("reprocessar perdeu contexto, a edição ou o isolamento da aba")
	}
	if err := h.d.saveWorkspace(); err != nil {
		t.Fatal(err)
	}
	h.d.loadWorkspace()
	restored := h.d.documents[0]
	if len(restored.processHistory) != 2 || restored.processHistory[1].Prompt != follow.Text {
		t.Fatal("a sessão perdeu a conversa")
	}
	restored.editor.SetText("Começar outro assunto.")
	h.d.processDocument(restored)
	newRequest := waitValue(t, requests)
	h.await(func() bool { return !restored.processBusy })
	if len(newRequest.History) != 0 || len(restored.processHistory) != 1 {
		t.Fatal("Processar não reiniciou a conversa após sucesso")
	}
}

func TestConversationFailedContinuationKeepsCompletedHistory(t *testing.T) {
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.process = func(context.Context, config.Settings, processing.Request) (processing.Result, error) {
			return processing.Result{}, errors.New("falha simulada")
		}
	})
	doc := h.d.document
	doc.processHistory = []processing.Turn{{Prompt: "Pedido anterior.", Response: "Resposta anterior."}}
	doc.processEditor.SetText("Resposta anterior.")
	doc.editor.SetText("Novo pedido.")
	test.Tap(doc.processFollowButton)
	h.await(func() bool { return !doc.processBusy })
	if len(doc.processHistory) != 1 || doc.processEditor.Text != "Resposta anterior." {
		t.Fatal("um pedido com erro apagou ou acrescentou um turno")
	}
	h.d.clearAllDocuments()
	if len(doc.processHistory) != 0 || !doc.processFollowButton.Disabled() {
		t.Fatal("fechar todas deixou o contexto anterior na aba vazia")
	}
}
