package desktop

import (
	"context"
	"errors"
	"testing"

	"ai-talker/internal/config"
	"ai-talker/internal/processing"
	"fyne.io/fyne/v2/test"
)

func TestStaleProfileSaveCannotReplaceLaterTabOptions(t *testing.T) {
	h := newHarness(t, nil, nil)
	doc := h.d.document
	h.d.openSettingsSection("LLM")
	main := h.d.settingsForm
	h.await(func() bool { return !main.loading })
	main.llm.profile.SetText("Perfil anterior à seleção")
	h.d.saveLLMProfile(main)
	latest := doc.processOptions
	latest.Mode = processing.ModeFree
	h.d.changeProcessingOptions(doc, latest)
	h.settle()
	if doc.processOptions.Mode != processing.ModeFree {
		t.Fatalf("callback de perfil reverteu a seleção posterior: aba=%q preferências=%q", doc.processOptions.Mode, h.d.settings.Processing.Mode)
	}
}

func TestHistoricalMergeInstructionKeepsExplicitCancelAvailable(t *testing.T) {
	started := make(chan struct{}, 1)
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.process = func(ctx context.Context, _ config.Settings, _ processing.Request) (processing.Result, error) {
			started <- struct{}{}
			<-ctx.Done()
			return processing.Result{}, ctx.Err()
		}
	})
	doc := seedThreePrompts(t, h)
	doc.processMode.SetSelected(mergeModeChoice)
	h.settle()
	h.d.navigatePrompt(doc, -1)
	test.Tap(doc.promptEdit)
	doc.editor.SetText("Prompt 2 reprocessado com instrução Mesclar")
	test.Tap(doc.processFollowButton)
	waitValue(t, started)
	if !doc.processBusy || !doc.processButton.Visible() || doc.processButton.Disabled() {
		t.Fatalf("Cancelar indisponível na chamada histórica: busy=%v visible=%v disabled=%v", doc.processBusy, doc.processButton.Visible(), doc.processButton.Disabled())
	}
	test.Tap(doc.processButton)
	h.settle()
	if doc.processBusy {
		t.Fatal("a ação explícita não cancelou a chamada histórica")
	}
}
func TestNewProcessSeparatesFailedConversationBeforeHistoricalRecovery(t *testing.T) {
	requests := make(chan processing.Request, 1)
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.process = func(_ context.Context, _ config.Settings, request processing.Request) (processing.Result, error) {
			requests <- request
			if request.Text == "Pedido que falhou" {
				return processing.Result{}, errors.New("falha simulada")
			}
			return processing.Result{Text: "Resposta " + request.Text}, nil
		}
	})
	doc := h.d.document
	doc.editor.SetText("Pedido que falhou")
	test.Tap(doc.processButton)
	waitValue(t, requests)
	h.await(func() bool { return !doc.processBusy })
	doc.editor.SetText("Nova conversa independente")
	test.Tap(doc.processButton)
	waitValue(t, requests)
	h.await(func() bool { return !doc.processBusy })
	doc.editor.SetText("Rascunho posterior")
	if err := h.d.saveWorkspace(); err != nil {
		t.Fatal(err)
	}
	firstConversation, activeConversation := doc.promptSteps[0].Conversation, doc.currentConversation
	h.d.navigatePrompt(doc, -1)
	h.d.navigatePrompt(doc, -1)
	test.Tap(doc.promptEdit)
	doc.editor.SetText("Pedido antigo recuperado")
	test.Tap(doc.processFollowButton)
	waitValue(t, requests)
	h.await(func() bool { return !doc.processBusy })
	if firstConversation == activeConversation || len(doc.processHistory) != 1 || doc.processHistory[0].Prompt != "Nova conversa independente" {
		t.Fatalf("Processar reutilizou conversa de tentativa fracassada; recuperar a antiga contaminou o contexto ativo: anterior=%d atual=%d history=%+v", firstConversation, activeConversation, doc.processHistory)
	}
}
