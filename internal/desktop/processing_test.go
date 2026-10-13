package desktop

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"ai-talker/internal/config"
	"ai-talker/internal/processing"
	"ai-talker/internal/store"

	"fyne.io/fyne/v2/test"
)

func TestProcessingWebSearchChoiceAndSourcesArePreservedPerTab(t *testing.T) {
	requests := make(chan processing.Request, 1)
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.process = func(_ context.Context, _ config.Settings, request processing.Request) (processing.Result, error) {
			requests <- request
			return processing.Result{
				Text:    "Resposta pesquisada.\n\nFontes:\nhttps://example.com",
				Sources: []processing.Source{{Title: "Fonte", URL: "https://example.com"}},
			}, nil
		}
	})
	first := h.d.document
	second := h.d.addDocument()
	h.d.tabs.Select(first.item)
	test.Tap(first.processSearch)
	if !first.processOptions.WebSearchDisabled || second.processOptions.WebSearchDisabled {
		t.Fatal("a escolha de busca não ficou isolada por aba")
	}
	second.editor.SetText("Consulte um dado atual.")
	test.Tap(second.processButton)
	request := waitValue(t, requests)
	if request.Options.WebSearchDisabled {
		t.Fatal("a busca habilitada não chegou ao serviço")
	}
	h.await(func() bool { return !second.processBusy })
	if err := h.d.saveWorkspace(); err != nil {
		t.Fatal(err)
	}
	var snapshot workspaceSnapshot
	if err := store.ReadJSON(filepath.Join(h.d.directory, "workspace-go-v1.json"), &snapshot); err != nil {
		t.Fatal(err)
	}
	if !snapshot.Tabs[0].Options.WebSearchDisabled || snapshot.Tabs[1].Options.WebSearchDisabled ||
		snapshot.Tabs[1].Processed == nil || len(snapshot.Tabs[1].Processed.Sources) != 1 ||
		!strings.Contains(snapshot.Tabs[1].ProcessedText, "https://example.com") {
		t.Fatal("a sessão perdeu a escolha de busca ou as fontes da resposta")
	}
}

func TestProcessingIsOptionalAndUsesSelectedTabEditor(t *testing.T) {
	calls := make(chan processing.Request, 1)
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.process = func(_ context.Context, _ config.Settings, request processing.Request) (processing.Result, error) {
			calls <- request
			return processing.Result{Text: "Olá, Mendes. A revisão ficou pronta."}, nil
		}
	})
	first := h.d.document
	first.editor.SetText("Texto que não deve ir ao modelo.")
	second := h.d.addDocument()
	h.d.startJob("segunda.wav")
	h.await(func() bool { return !h.d.busy })
	select {
	case <-calls:
		t.Fatal("a transcrição iniciou o processamento automaticamente")
	default:
	}
	second.editor.SetText("Quero falar pro Mendes, num tom cordial, que a revisão ficou pronta.")
	test.Tap(second.processButton)
	request := waitValue(t, calls)
	if request.Text != second.editor.Text || request.Options.Mode != processing.ModeTeams ||
		request.Options.ModelID != processing.DefaultModelID {
		t.Fatal("Processar não usou o texto atual e as escolhas da aba selecionada")
	}
	h.await(func() bool { return !second.processBusy })
	if second.processEditor.Text != "Olá, Mendes. A revisão ficou pronta." ||
		first.processEditor.Text != "" || first.editor.Text != "Texto que não deve ir ao modelo." {
		t.Fatal("o resultado alterou a transcrição ou outra aba")
	}
}

func TestProcessingCompletesInOriginTabAfterSelectionChanges(t *testing.T) {
	started := make(chan processing.Request, 1)
	release := make(chan struct{})
	releaseProcessing := sync.OnceFunc(func() { close(release) })
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.process = func(_ context.Context, _ config.Settings, request processing.Request) (processing.Result, error) {
			started <- request
			<-release
			return processing.Result{Text: "Resposta para a primeira aba."}, nil
		}
	})
	t.Cleanup(releaseProcessing)
	first := h.d.document
	first.editor.SetText("Pedido da primeira aba.")
	test.Tap(first.processButton)
	waitValue(t, started)
	second := h.d.addDocument()
	second.editor.SetText("Texto da segunda aba.")
	second.processEditor.SetText("Resposta da segunda aba.")
	releaseProcessing()
	h.await(func() bool { return !first.processBusy })
	if first.processEditor.Text != "Resposta para a primeira aba." ||
		second.editor.Text != "Texto da segunda aba." || second.processEditor.Text != "Resposta da segunda aba." {
		t.Fatal("a mudança de aba redirecionou a resposta")
	}
}

func TestProcessingCancelEditClearAndClosedTabRejectLateResponses(t *testing.T) {
	for _, action := range []string{"cancelar", "editar", "editar resultado", "limpar e desfazer", "fechar aba"} {
		t.Run(action, func(t *testing.T) {
			started := make(chan context.Context, 1)
			release := make(chan struct{})
			releaseProcessing := sync.OnceFunc(func() { close(release) })
			h := newHarness(t, nil, func(deps *dependencies) {
				deps.process = func(ctx context.Context, _ config.Settings, _ processing.Request) (processing.Result, error) {
					started <- ctx
					<-release
					return processing.Result{Text: "Resposta atrasada que não deve voltar."}, nil
				}
			})
			t.Cleanup(releaseProcessing)
			doc := h.d.document
			doc.editor.SetText("Texto do pedido.")
			doc.processEditor.SetText("Resposta que já existe.")
			test.Tap(doc.processButton)
			job := waitValue(t, started)
			expected := "Resposta que já existe."
			switch action {
			case "cancelar":
				test.Tap(doc.processButton)
			case "editar":
				doc.editor.SetText("Texto editado.")
			case "editar resultado":
				expected = "Edição manual que deve ficar."
				doc.processEditor.SetText(expected)
			case "limpar e desfazer":
				test.Tap(doc.clearButton)
				test.Tap(doc.undoButton)
			case "fechar aba":
				h.d.addDocument()
				h.d.closeDocument(doc.item)
			}
			if job.Err() == nil || doc.processBusy || h.d.ctx.Err() != nil {
				t.Fatal("a ação não cancelou somente o processamento antigo")
			}
			releaseProcessing()
			h.settle()
			if doc.processEditor.Text != expected || doc.processError.Visible() {
				t.Fatal("uma resposta antiga modificou o resultado ou mostrou um erro")
			}
		})
	}
}

func TestProcessingFreeModeAndModelSelectionArePerTab(t *testing.T) {
	calls := make(chan processing.Request, 2)
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.process = func(_ context.Context, _ config.Settings, request processing.Request) (processing.Result, error) {
			calls <- request
			return processing.Result{Text: "Resposta livre."}, nil
		}
	})
	first := h.d.document
	second := h.d.addDocument()
	second.editor.SetText("Explique este trecho em três tópicos.")
	second.processMode.SetSelected(freeModeChoice)
	second.processModel.SetSelected(processing.Models()[0].Name)
	test.Tap(second.processButton)
	request := waitValue(t, calls)
	h.await(func() bool { return !second.processBusy })
	if request.Options.Mode != processing.ModeFree || request.Options.ModelID != processing.Models()[0].ID {
		t.Fatal("a seleção do modo Livre ou do modelo não foi aplicada")
	}
	h.d.tabs.Select(first.item)
	if first.processMode.Selected != teamsModeChoice || first.processOptions.ModelID != processing.DefaultModelID {
		t.Fatal("a escolha da segunda aba alterou a primeira")
	}
	h.settle()
	settings, err := config.Load(h.d.directory)
	if err != nil || settings.Processing.Mode != processing.ModeFree ||
		settings.Processing.ModelID != processing.Models()[0].ID {
		t.Fatalf("a seleção inicial para novas abas não foi lembrada: %+v, %v", settings.Processing, err)
	}
}

func TestProcessingEmptyErrorAndPanicKeepOriginalAndOutputEditable(t *testing.T) {
	for _, scenario := range []string{"vazio", "erro", "panic"} {
		t.Run(scenario, func(t *testing.T) {
			h := newHarness(t, nil, func(deps *dependencies) {
				deps.process = func(context.Context, config.Settings, processing.Request) (processing.Result, error) {
					switch scenario {
					case "erro":
						return processing.Result{}, errors.New("modelo sem acesso")
					case "panic":
						panic("falha simulada")
					default:
						return processing.Result{}, nil
					}
				}
			})
			h.d.editor.SetText("Texto original da transcrição.")
			h.d.processEditor.SetText("Resposta anterior que posso copiar.")
			test.Tap(h.d.processButton)
			h.await(func() bool { return !h.d.processBusy })
			if !h.d.processError.Visible() || h.d.errorLabel.Visible() ||
				h.d.editor.Text != "Texto original da transcrição." ||
				h.d.processEditor.Text != "Resposta anterior que posso copiar." {
				t.Fatal("a falha do processamento descartou texto ou afetou a transcrição")
			}
			if h.d.processButton.Disabled() || h.d.processCopyButton.Disabled() || h.d.clearButton.Disabled() {
				t.Fatal("a falha do processamento bloqueou os controles")
			}
			if len(h.d.window.Canvas().Overlays().List()) != 0 {
				t.Fatal("o erro do modelo abriu um modal")
			}
		})
	}
}

func TestProcessingCopyAndSaveUseActualOutputEditor(t *testing.T) {
	copied := make(chan string, 1)
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.copy = func(text string) error { copied <- text; return nil }
	})
	h.d.editor.SetText("Transcrição original.")
	h.d.processEditor.SetText("Resposta editada para enviar.")
	test.Tap(h.d.processCopyButton)
	if text := waitValue(t, copied); text != "Resposta editada para enviar." {
		t.Fatalf("foi copiada a transcrição em vez do resultado: %q", text)
	}
	h.settle()
	writer := &selectionWriter{}
	h.d.saveDocumentSelection(h.d.document, true, writer)
	h.await(func() bool { return h.d.status.Text == "Texto salvo." })
	if writer.buffer.String() != "Resposta editada para enviar.\n" || writer.closed.Load() != 1 {
		t.Fatal("salvar usou conteúdo diferente do editor de resultado")
	}
	h.d.processEditor.SetText("")
	if !h.d.processCopyButton.Disabled() || !h.d.processSaveButton.Disabled() {
		t.Fatal("copiar e salvar resultado não acompanham o editor vazio")
	}
}

func TestProcessingInstructionCanBeEditedAndReset(t *testing.T) {
	h := newHarness(t, nil, nil)
	test.Tap(h.d.processPrompt)
	h.await(func() bool { return !h.d.settingsForm.loading })
	form := h.d.processingForm
	if form == nil || !strings.Contains(form.prompt.Text, "instruções ditadas") {
		t.Fatal("a instrução inicial não considera as orientações faladas pelo usuário")
	}
	form.prompt.SetText("Minha instrução de redação, com outro tom.")
	form.model.SetText("my-custom-converse-model")
	test.Tap(form.save)
	h.settle()
	if h.d.processingForm != nil || h.d.processOptions.TeamsPrompt != "Minha instrução de redação, com outro tom." ||
		h.d.processOptions.ModelID != "my-custom-converse-model" {
		t.Fatal("a instrução ou o ID do modelo não foram aplicados")
	}
	settings, err := config.Load(h.d.directory)
	if err != nil || settings.Processing != h.d.processOptions {
		t.Fatal("a instrução editada não foi persistida")
	}
}

func TestProcessingConcurrencyIsBoundedAndWaitingRequestCanCancel(t *testing.T) {
	started := make(chan string, 3)
	release := make(chan struct{})
	releaseCalls := sync.OnceFunc(func() { close(release) })
	var active, maximum atomic.Int32
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.process = func(ctx context.Context, _ config.Settings, request processing.Request) (processing.Result, error) {
			count := active.Add(1)
			defer active.Add(-1)
			for old := maximum.Load(); count > old && !maximum.CompareAndSwap(old, count); old = maximum.Load() {
			}
			started <- request.Text
			select {
			case <-release:
				return processing.Result{Text: "Resposta."}, nil
			case <-ctx.Done():
				return processing.Result{}, ctx.Err()
			}
		}
	})
	t.Cleanup(releaseCalls)
	first := h.d.document
	first.editor.SetText("Primeira.")
	test.Tap(first.processButton)
	waitValue(t, started)
	second := h.d.addDocument()
	second.editor.SetText("Segunda.")
	test.Tap(second.processButton)
	waitValue(t, started)
	third := h.d.addDocument()
	third.editor.SetText("Terceira.")
	test.Tap(third.processButton)
	test.Tap(third.processButton)
	releaseCalls()
	h.settle()
	if maximum.Load() > 2 || third.processBusy || third.processEditor.Text != "" {
		t.Fatal("as chamadas não foram limitadas ou o pedido cancelado da fila foi executado")
	}
	select {
	case <-started:
		t.Fatal("o pedido cancelado enquanto aguardava iniciou uma chamada ao modelo")
	default:
	}
}
