package desktop

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"ai-talker/internal/apiconfig"
	"ai-talker/internal/config"
	"ai-talker/internal/processing"

	"fyne.io/fyne/v2/test"
)

func completedPromptConversation(t *testing.T, h *testHarness, requests <-chan processing.Request) *document {
	t.Helper()
	doc := h.d.document
	for index := 1; index <= 3; index++ {
		doc.editor.SetText(fmt.Sprintf("Pedido %d", index))
		if index == 1 {
			test.Tap(doc.processButton)
		} else {
			test.Tap(doc.processFollowButton)
		}
		waitValue(t, requests)
		h.await(func() bool { return !doc.processBusy })
	}
	doc.editor.SetText("Rascunho mais recente")
	if err := h.d.saveWorkspace(); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestHistoricalPromptReprocessUsesCurrentOptionsOnlyPriorContextAndKeepsLaterWork(t *testing.T) {
	requests := make(chan processing.Request, 1)
	release := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.process = func(ctx context.Context, _ config.Settings, request processing.Request) (processing.Result, error) {
			requests <- request
			if strings.Contains(request.Text, "editado") {
				select {
				case <-release:
				case <-ctx.Done():
					return processing.Result{}, ctx.Err()
				}
			}
			return processing.Result{Text: "Resposta " + request.Text}, nil
		}
	})
	t.Cleanup(unblock)
	doc := completedPromptConversation(t, h, requests)
	laterHistory := slices.Clone(doc.processHistory)
	options := doc.processOptions
	options.Provider, options.API, options.ModelID = "api", apiconfig.Connection{Provider: apiconfig.OpenAI}, "current-model"
	options.ReasoningEffort = "high"
	h.d.changeProcessingOptions(doc, options)
	h.settle()
	h.d.navigatePrompt(doc, -1)
	h.d.navigatePrompt(doc, -1)
	if doc.editor.Text != "Pedido 2" || !doc.editor.Disabled() || !doc.promptEdit.Visible() {
		t.Fatal("o par antigo deixou de ser consulta explícita")
	}
	test.Tap(doc.promptEdit)
	doc.editor.SetText("Pedido 2 editado")
	test.Tap(doc.processFollowButton)
	request := waitValue(t, requests)
	if request.Text != "Pedido 2 editado" || request.Options.ModelID != "current-model" ||
		request.Options.Provider != "api" || request.Options.ReasoningEffort != "high" ||
		!slices.Equal(request.History, laterHistory[:1]) {
		t.Fatal("o envio usou configuração antiga ou contexto posterior", request)
	}
	generation := doc.processGeneration
	h.d.navigatePrompt(doc, 1)
	if doc.processGeneration != generation || !doc.processBusy {
		t.Fatal("navegar cancelou o reprocessamento")
	}
	unblock()
	h.await(func() bool { return !doc.processBusy })
	if doc.editor.Text != "Pedido 3" || doc.processEditor.Text != "Resposta Pedido 3" ||
		doc.promptSteps[1].Prompt != "Pedido 2 editado" || doc.promptSteps[1].Response != "Resposta Pedido 2 editado" ||
		doc.processHistory[2] != laterHistory[2] || len(doc.processHistory) != 3 ||
		doc.promptSteps[1].Options.ModelID != "current-model" || len(doc.promptSteps[1].PreviousOptions) != 1 {
		t.Fatal("a resposta alterou a página consultada ou os turnos posteriores", doc.promptSteps, doc.processHistory)
	}
	h.d.navigatePrompt(doc, 1)
	if doc.editor.Text != "Rascunho mais recente" || doc.processEditor.Text != "Resposta Pedido 3" {
		t.Fatal("o rascunho atual foi sobrescrito")
	}
	if !h.d.loadWorkspace() || h.d.document.processHistory[1].Prompt != "Pedido 2 editado" ||
		h.d.document.editor.Text != "Rascunho mais recente" {
		t.Fatal("reabrir perdeu a atualização do turno ou o rascunho")
	}
}

func TestHistoricalPromptFailureCancelAndSaveFailureKeepEditedDraftAndPriorResponse(t *testing.T) {
	for _, failure := range []string{"provider", "cancel", "save"} {
		t.Run(failure, func(t *testing.T) {
			requests := make(chan processing.Request, 1)
			release := make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			h := newHarness(t, nil, func(deps *dependencies) {
				deps.process = func(ctx context.Context, _ config.Settings, request processing.Request) (processing.Result, error) {
					requests <- request
					if strings.Contains(request.Text, "editado") {
						select {
						case <-release:
						case <-ctx.Done():
							return processing.Result{}, ctx.Err()
						}
						if failure == "provider" {
							return processing.Result{}, errors.New("falha simulada")
						}
					}
					return processing.Result{Text: "Resposta " + request.Text}, nil
				}
			})
			t.Cleanup(unblock)
			doc := completedPromptConversation(t, h, requests)
			h.d.navigatePrompt(doc, -1)
			h.d.navigatePrompt(doc, -1)
			test.Tap(doc.promptEdit)
			doc.editor.SetText("Pedido 2 editado")
			test.Tap(doc.processFollowButton)
			waitValue(t, requests)
			if failure == "save" {
				h.d.deps.writeWorkspace = func(string, workspaceSnapshot) error { return errors.New("disco cheio") }
			}
			if failure == "cancel" {
				test.Tap(doc.processButton)
			}
			unblock()
			h.settle()
			if doc.promptSteps[1].Prompt != "Pedido 2" || doc.promptSteps[1].Response != "Resposta Pedido 2" ||
				doc.promptSteps[1].Draft == nil || *doc.promptSteps[1].Draft != "Pedido 2 editado" ||
				doc.processHistory[1].Prompt != "Pedido 2" || doc.currentPrompt != "Rascunho mais recente" {
				t.Fatal("a falha perdeu a edição ou alterou a resposta anterior")
			}
			h.d.deps.writeWorkspace = nil
			if err := h.d.saveWorkspace(); err != nil {
				t.Fatal(err)
			}
			if !h.d.loadWorkspace() || h.d.document.promptSteps[1].Draft == nil ||
				*h.d.document.promptSteps[1].Draft != "Pedido 2 editado" {
				t.Fatal("a edição recuperável não persistiu")
			}
		})
	}
}

func TestReplacingLastCompletedTurnDoesNotReuseItsOldPresentedAnswerAfterReopen(t *testing.T) {
	for _, reopen := range []bool{false, true} {
		t.Run(fmt.Sprintf("reopen=%t", reopen), func(t *testing.T) {
			requests := make(chan processing.Request, 1)
			h := newHarness(t, nil, func(deps *dependencies) {
				deps.process = func(_ context.Context, _ config.Settings, request processing.Request) (processing.Result, error) {
					requests <- request
					return processing.Result{Text: "Resposta " + request.Text}, nil
				}
			})
			doc := completedPromptConversation(t, h, requests)
			h.d.navigatePrompt(doc, -1)
			test.Tap(doc.promptEdit)
			doc.editor.SetText("Pedido 3 editado")
			test.Tap(doc.processFollowButton)
			waitValue(t, requests)
			h.await(func() bool { return !doc.processBusy })
			h.d.navigatePrompt(doc, 1)
			if doc.editor.Text != "Rascunho mais recente" || doc.processEditor.Text != "Resposta Pedido 3" {
				t.Fatal("a atualização perdeu a apresentação do rascunho atual")
			}
			if reopen {
				if !h.d.loadWorkspace() {
					t.Fatal("a sessão não reabriu")
				}
				doc = h.d.document
			}
			test.Tap(doc.processFollowButton)
			request := waitValue(t, requests)
			h.await(func() bool { return !doc.processBusy })
			if len(request.History) != 3 || request.History[2].Prompt != "Pedido 3 editado" ||
				request.History[2].Response != "Resposta Pedido 3 editado" {
				t.Fatal("a resposta antiga da apresentação sobrescreveu o último turno atualizado", request.History)
			}
		})
	}
}
