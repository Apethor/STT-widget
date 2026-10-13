package desktop

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"ai-talker/internal/config"
	"ai-talker/internal/processing"

	"fyne.io/fyne/v2/test"
)

func TestPromptNavigationKeepsPendingResponseAndMoreThanThreeCompletedSteps(t *testing.T) {
	requests := make(chan processing.Request, 8)
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.process = func(_ context.Context, _ config.Settings, request processing.Request) (processing.Result, error) {
			requests <- request
			return processing.Result{Text: "Resposta " + request.Text}, nil
		}
	})
	doc := h.d.document
	for index := 1; index <= 5; index++ {
		prompt := fmt.Sprintf("Prompt %d", index)
		h.d.displayDocumentResult(doc, resultFor(fmt.Sprintf("audio-%d.wav", index), prompt), false, prompt, false)
		if index > 1 {
			h.d.navigatePrompt(doc, -1)
			if doc.editor.Text != fmt.Sprintf("Prompt %d", index-1) ||
				doc.processEditor.Text != fmt.Sprintf("Resposta Prompt %d", index-1) ||
				!doc.editor.Disabled() || !doc.processEditor.Disabled() || doc.processFollowButton.Visible() {
				t.Fatal("o histórico não apresentou o par antigo somente para consulta")
			}
			h.d.navigatePrompt(doc, 1)
			if doc.editor.Text != prompt || doc.processEditor.Text != fmt.Sprintf("Resposta Prompt %d", index-1) ||
				doc.editor.Disabled() || !doc.processFollowButton.Visible() {
				t.Fatal("o rascunho pendente perdeu o prompt ou a última resposta")
			}
			test.Tap(doc.processFollowButton)
		} else {
			test.Tap(doc.processButton)
		}
		request := waitValue(t, requests)
		h.await(func() bool { return !doc.processBusy })
		if request.Text != prompt || len(request.History) != index-1 {
			t.Fatal("a continuação usou outra etapa ou perdeu o contexto", request)
		}
	}
	for index := 4; index >= 1; index-- {
		h.d.navigatePrompt(doc, -1)
		if doc.editor.Text != fmt.Sprintf("Prompt %d", index) || doc.processEditor.Text != fmt.Sprintf("Resposta Prompt %d", index) {
			t.Fatal("Voltar não manteve prompt e resposta associados")
		}
	}
	if err := h.d.saveWorkspace(); err != nil {
		t.Fatal(err)
	}
	h.d.loadWorkspace()
	doc = h.d.document
	if len(doc.promptSteps) != 5 || doc.editor.Text != "Prompt 5" || doc.processEditor.Text != "Resposta Prompt 5" ||
		len(doc.processHistory) != 5 || doc.editor.Disabled() {
		t.Fatal("salvar enquanto consultava uma antiga substituiu o rascunho atual")
	}
	for range 4 {
		h.d.navigatePrompt(doc, -1)
	}
	if doc.editor.Text != "Prompt 1" || doc.processEditor.Text != "Resposta Prompt 1" {
		t.Fatal("reabrir perdeu as etapas antigas")
	}
}

func TestBrowsingDuringProcessingDoesNotCancelOrReplaceTheConsultedStep(t *testing.T) {
	started := make(chan processing.Request, 2)
	release := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	t.Cleanup(unblock)
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.process = func(ctx context.Context, _ config.Settings, request processing.Request) (processing.Result, error) {
			started <- request
			if request.Text == "Segundo" {
				select {
				case <-release:
				case <-ctx.Done():
					return processing.Result{}, ctx.Err()
				}
			}
			return processing.Result{Text: "Resposta " + request.Text}, nil
		}
	})
	doc := h.d.document
	doc.editor.SetText("Primeiro")
	test.Tap(doc.processButton)
	waitValue(t, started)
	h.await(func() bool { return !doc.processBusy })
	h.d.displayDocumentResult(doc, resultFor("segundo.wav", "Segundo"), false, "Segundo", false)
	test.Tap(doc.processFollowButton)
	waitValue(t, started)
	generation := doc.processGeneration
	h.d.navigatePrompt(doc, -1)
	if !doc.processBusy || generation != doc.processGeneration {
		t.Fatal("consultar o histórico cancelou a tarefa")
	}
	unblock()
	h.await(func() bool { return !doc.processBusy })
	if doc.editor.Text != "Primeiro" || doc.processEditor.Text != "Resposta Primeiro" || !doc.browsingPrompts() {
		t.Fatal("a resposta em andamento substituiu o par consultado")
	}
	h.d.navigatePrompt(doc, 1)
	if doc.editor.Text != "Segundo" || doc.processEditor.Text != "Resposta Segundo" ||
		!doc.promptSteps[1].Processed || len(doc.processHistory) != 2 {
		t.Fatal("a resposta não chegou à etapa de origem")
	}
}

func TestFailedNewConversationKeepsOldConsultationButCannotContinueItsContext(t *testing.T) {
	requests := make(chan processing.Request, 3)
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.process = func(_ context.Context, _ config.Settings, request processing.Request) (processing.Result, error) {
			requests <- request
			if request.Text == "Novo assunto" {
				return processing.Result{}, errors.New("falha da nova conversa")
			}
			return processing.Result{Text: "Resposta antiga"}, nil
		}
	})
	doc := h.d.document
	doc.editor.SetText("Assunto anterior")
	test.Tap(doc.processButton)
	waitValue(t, requests)
	h.await(func() bool { return !doc.processBusy })
	doc.editor.SetText("Novo assunto")
	test.Tap(doc.processButton)
	request := waitValue(t, requests)
	h.await(func() bool { return !doc.processBusy })
	if len(request.History) != 0 || len(doc.processHistory) != 0 || !doc.processFollowButton.Disabled() {
		t.Fatal("a conversa nova com erro reabriu o contexto antigo")
	}
	h.d.navigatePrompt(doc, -1)
	if doc.editor.Text != "Assunto anterior" || doc.processEditor.Text != "Resposta antiga" {
		t.Fatal("a falha apagou a consulta da conversa anterior")
	}
	h.d.navigatePrompt(doc, 1)
	if doc.editor.Text != "Novo assunto" || doc.promptSteps[len(doc.promptSteps)-1].Processed {
		t.Fatal("a falha criou um turno concluído ou perdeu o rascunho")
	}
}

func TestConversationWindowAllowsTwentyFiveTurnsWhileKeepingFullHistory(t *testing.T) {
	requests := make(chan processing.Request, 1)
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.process = func(_ context.Context, _ config.Settings, request processing.Request) (processing.Result, error) {
			requests <- request
			return processing.Result{Text: "Resposta " + request.Text}, nil
		}
	})
	doc := h.d.document
	for index := range 25 {
		doc.editor.SetText(fmt.Sprintf("Pedido %d", index))
		if index == 0 {
			test.Tap(doc.processButton)
		} else {
			test.Tap(doc.processFollowButton)
		}
		request := waitValue(t, requests)
		h.await(func() bool { return !doc.processBusy })
		if len(request.History) >= processing.MaxConversationTurns {
			t.Fatal("o pedido excedeu o limite do contexto")
		}
	}
	if len(doc.processHistory) != 25 || len(doc.promptSteps) != 25 ||
		!strings.Contains(doc.processStatus.Text, "contexto") {
		t.Fatal("a janela descartou histórico consultável ou deixou de avisar sobre a redução")
	}
}
