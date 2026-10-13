package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"ai-talker/internal/apiconfig"
	"ai-talker/internal/config"
	"ai-talker/internal/processing"
	"ai-talker/internal/store"
	"ai-talker/internal/transcript"

	"fyne.io/fyne/v2/test"
)

func TestJoinPreservesExactOrderProcessedEditorsTabsAndUndo(t *testing.T) {
	now := time.Now()
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.now = func() time.Time { return now }
		deps.process = func(context.Context, config.Settings, processing.Request) (processing.Result, error) {
			t.Fatal("Juntar não deve chamar uma LLM")
			return processing.Result{}, nil
		}
	})
	first := h.d.document
	first.editor.SetText("  Primeiro texto.\nLinha dois.  ")
	first.processEditor.SetText("Resposta processada 1.")
	second := h.d.addDocument()
	second.editor.SetText("Segundo texto.")
	second.processEditor.SetText("Resposta processada 2.")
	third := h.d.addDocument()
	third.editor.SetText("Terceiro texto.")
	test.Tap(h.d.joinButton)
	want := "  Primeiro texto.\nLinha dois.  \n\nSegundo texto.\n\nTerceiro texto."
	if first.editor.Text != want ||
		len(h.d.documents) != 1 || h.d.document != first || !second.closed || !third.closed {
		t.Fatal("Juntar não reuniu os textos na primeira aba e fechou as outras")
	}
	if first.processEditor.Text != "Resposta processada 1." || second.processEditor.Text != "Resposta processada 2." {
		t.Fatal("Juntar alterou os resultados processados")
	}
	var saved workspaceSnapshot
	if err := store.ReadJSON(filepath.Join(h.d.directory, "workspace-go-v1.json"), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Selected != first.id || len(saved.Tabs) != 1 || saved.Tabs[0].Text != want {
		t.Fatal("o texto combinado e a seleção não foram persistidos")
	}
	test.Tap(h.d.undoCloseButton)
	if third.editor.Text != "Terceiro texto." || second.editor.Text != "Segundo texto." ||
		first.editor.Text != "  Primeiro texto.\nLinha dois.  " ||
		len(h.d.documents) != 3 || second.closed || third.closed || h.d.undoCloseButton.Visible() {
		t.Fatal("Desfazer Juntar não restaurou a operação inteira")
	}
}

func TestJoinWriteFailureKeepsAllOriginalsVisible(t *testing.T) {
	h := newHarness(t, nil, nil)
	first := h.d.document
	first.editor.SetText("Primeiro original.")
	second := h.d.addDocument()
	second.editor.SetText("Segundo original.")
	path := filepath.Join(h.d.directory, "workspace-go-v1.json")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	test.Tap(h.d.joinButton)
	if first.editor.Text != "Primeiro original." || second.editor.Text != "Segundo original." ||
		!h.d.errorLabel.Visible() {
		t.Fatal("uma falha ao preservar as abas apagou os textos")
	}
}

func TestJoinFailedFinalSaveRestoresOriginalTextsSelectionAndRetry(t *testing.T) {
	blocked := false
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.writeWorkspace = func(path string, snapshot workspaceSnapshot) error {
			if len(snapshot.Tabs) == 1 && strings.Contains(snapshot.Tabs[0].Text, "Segundo original.") {
				blocked = true
				return errors.New("falha simulada no salvamento final")
			}
			return store.WriteJSON(path, snapshot)
		}
	})
	first := h.d.document
	first.editor.SetText("Primeiro original.")
	first.lastAudio = "audio-original.wav"
	first.lastResult = &transcript.Result{Text: first.editor.Text, NeedsReview: true}
	first.transcriptionRun = strings.Repeat("A", 26)
	first.retryButton.Show()
	second := h.d.addDocument()
	second.editor.SetText("Segundo original.")
	test.Tap(h.d.joinButton)
	if !blocked || first.editor.Text != "Primeiro original." || second.editor.Text != "Segundo original." ||
		h.d.document != second || !first.retryButton.Visible() || first.lastAudio != "audio-original.wav" ||
		first.lastResult == nil || first.transcriptionRun != strings.Repeat("A", 26) || !h.d.errorLabel.Visible() {
		t.Fatal("a falha do salvamento final deixou uma origem, seleção ou controle alterado")
	}
}

func TestMergeUsesConfiguredPromptAndChangesEditorsOnlyOnSuccess(t *testing.T) {
	requests := make(chan processing.Request, 1)
	release := make(chan struct{})
	releaseCall := sync.OnceFunc(func() { close(release) })
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.process = func(_ context.Context, _ config.Settings, request processing.Request) (processing.Result, error) {
			requests <- request
			<-release
			return processing.Result{Text: "Mensagem unificada."}, nil
		}
	})
	t.Cleanup(releaseCall)
	first := h.d.document
	first.editor.SetText("Primeira parte.")
	second := h.d.addDocument()
	second.editor.SetText("Segunda parte.")
	second.processOptions.MergePrompt = "Mescle as duas partes mantendo os fatos."
	second.processOptions.MergeReasoning = "medium"
	test.Tap(h.d.mergeButton)
	request := waitValue(t, requests)
	if request.Options.Mode != processing.ModeMerge ||
		request.Options.ModelID != "global.openai.gpt-6.1-sol" ||
		request.Options.TeamsPrompt != second.processOptions.MergePrompt ||
		request.Options.ReasoningEffort != "medium" {
		t.Fatal("a mesclagem perdeu as opções próprias")
	}
	var inputs []struct {
		Name string `json:"aba"`
		Text string `json:"texto"`
	}
	if err := json.Unmarshal([]byte(request.Text), &inputs); err != nil ||
		len(inputs) != 2 || inputs[0].Text != "Primeira parte." || inputs[1].Text != "Segunda parte." {
		t.Fatal("as origens não foram enviadas em ordem")
	}
	if first.editor.Text != "Primeira parte." || second.editor.Text != "Segunda parte." || !h.d.joinButton.Disabled() {
		t.Fatal("os textos foram alterados antes da resposta")
	}
	releaseCall()
	h.await(func() bool { return !h.d.mergeBusy })
	if first.editor.Text != "Mensagem unificada." || h.d.document != first ||
		len(h.d.documents) != 1 || !second.closed {
		t.Fatal("a resposta válida não foi colocada na primeira aba")
	}
}

func TestMergeCancellationAndChangedSourcesRejectLateAnswers(t *testing.T) {
	for _, action := range []string{"cancelar", "editar", "abrir aba", "fechar aba", "renomear", "opções de mesclagem"} {
		t.Run(action, func(t *testing.T) {
			contexts := make(chan context.Context, 1)
			release := make(chan struct{})
			releaseCall := sync.OnceFunc(func() { close(release) })
			h := newHarness(t, nil, func(deps *dependencies) {
				deps.process = func(ctx context.Context, _ config.Settings, _ processing.Request) (processing.Result, error) {
					contexts <- ctx
					<-release
					return processing.Result{Text: "Resposta atrasada que deve ser rejeitada."}, nil
				}
			})
			t.Cleanup(releaseCall)
			first := h.d.document
			first.editor.SetText("Primeira parte.")
			second := h.d.addDocument()
			second.editor.SetText("Segunda parte.")
			test.Tap(h.d.mergeButton)
			ctx := waitValue(t, contexts)
			switch action {
			case "cancelar":
				test.Tap(h.d.mergeButton)
			case "editar":
				second.editor.SetText("Segunda parte editada.")
			case "abrir aba":
				h.d.addDocument()
			case "fechar aba":
				h.d.closeDocument(second.item)
			case "renomear":
				if err := h.d.renameDocument(second, "Projeto"); err != nil {
					t.Fatal(err)
				}
			case "opções de mesclagem":
				updated := cloneSettings(h.d.settings)
				updated.Merge.Prompt = "Uma nova instrução."
				h.d.persistPreferences(updated, func(error) {})
			}
			if h.d.mergeBusy || ctx.Err() == nil {
				t.Fatal("a mesclagem antiga não foi cancelada prontamente")
			}
			releaseCall()
			h.settle()
			if first.editor.Text != "Primeira parte." ||
				strings.Contains(second.editor.Text, "atrasada") || h.d.ctx.Err() != nil {
				t.Fatal("a resposta atrasada alterou os textos ou o contexto da interface")
			}
		})
	}
}

func TestMergeEmptyOrErrorKeepsOriginalsAndAllowsRetry(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		text string
	}{
		{name: "erro", err: errors.New("conexão indisponível")},
		{name: "vazio", text: " \n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, nil, func(deps *dependencies) {
				deps.process = func(context.Context, config.Settings, processing.Request) (processing.Result, error) {
					return processing.Result{Text: tc.text}, tc.err
				}
			})
			first := h.d.document
			first.editor.SetText("Primeiro original.")
			second := h.d.addDocument()
			second.editor.SetText("Segundo original.")
			test.Tap(h.d.mergeButton)
			h.await(func() bool { return !h.d.mergeBusy })
			if first.editor.Text != "Primeiro original." || second.editor.Text != "Segundo original." ||
				h.d.mergeButton.Disabled() || !h.d.errorLabel.Visible() {
				t.Fatal("a falha da mesclagem perdeu as origens ou bloqueou nova tentativa")
			}
		})
	}
}

func TestUndoCombinationRestoresCompleteSourcesAndArchivesLaterWork(t *testing.T) {
	for _, operation := range []string{"Juntar", "Mesclar"} {
		t.Run(operation, func(t *testing.T) {
			requests := make(chan processing.Request, 1)
			now := time.Now()
			h := newHarness(t, nil, func(deps *dependencies) {
				deps.now = func() time.Time { return now }
				deps.process = func(_ context.Context, _ config.Settings, request processing.Request) (processing.Result, error) {
					requests <- request
					return processing.Result{Text: "Transcrições mescladas."}, nil
				}
			})
			first, _, _ := seedScreenshotTimeline(t, h)
			first.processEditor.SetText("Resposta original da primeira aba.")
			first.processOptions.Mode = processing.ModeFree
			first.transcriptCollapsed = true
			first.panelSizes.EditorOffset = 0.64
			h.d.arrangeDocumentEditors(first)
			if err := h.d.renameDocument(first, "Origem A"); err != nil {
				t.Fatal(err)
			}
			second := h.d.addDocument()
			second.editor.SetText("Segunda transcrição.")
			second.processEditor.SetText("Resposta original da segunda aba.")
			second.processOptions.Provider = "api"
			second.processOptions.API = apiconfig.Connection{Provider: apiconfig.OpenAI}
			second.processOptions.ModelID, second.processOptions.MergeModelID = "modelo-principal", "modelo-mesclar"
			second.processOptions.ReasoningEffort, second.processOptions.MergeReasoning = "low", "high"
			second.processOptions.MergeNeedsReview = false
			other := h.d.addGroup()
			other.editor.SetText("Outro grupo intacto.")
			h.d.tabs.Select(second.item)
			original, err := detachedWorkspace(h.d.snapshotWorkspace())
			if err != nil {
				t.Fatal(err)
			}
			if operation == "Juntar" {
				test.Tap(h.d.joinButton)
			} else {
				test.Tap(h.d.mergeButton)
				request := waitValue(t, requests)
				if request.Options.Provider != "api" || request.Options.API.Provider != apiconfig.OpenAI ||
					request.Options.ModelID != "modelo-mesclar" || request.Options.ReasoningEffort != "high" ||
					!request.Options.WebSearchDisabled {
					t.Fatal("Mesclar não usou provider comum e modelo/raciocínio próprios", request.Options)
				}
				h.await(func() bool { return !h.d.mergeBusy })
			}
			first.editor.SetText("Combinação editada depois da operação.")
			first.processEditor.SetText("Resposta editada depois da operação.")
			later := h.d.addDocument()
			later.editor.SetText("Trabalho novo na mesma linha.")
			other.editor.SetText("Trabalho posterior no outro grupo.")
			h.d.undoCloseDocument()
			if h.d.closedTab != nil || len(h.d.documents) != 4 || h.d.documents[0] != first ||
				h.d.documents[1] != second || later.closed || later.editor.Text != "Trabalho novo na mesma linha." ||
				other.editor.Text != "Trabalho posterior no outro grupo." {
				t.Fatal("Desfazer alterou trabalho posterior ou não restaurou as origens")
			}
			for _, doc := range []*document{first, second} {
				index := slices.IndexFunc(original.Tabs, func(tab documentSnapshot) bool { return tab.ID == doc.id })
				actual := h.d.snapshotDocument(doc)
				if !reflect.DeepEqual(actual, original.Tabs[index]) {
					want, got := reflect.ValueOf(original.Tabs[index]), reflect.ValueOf(actual)
					for field := range want.NumField() {
						if !reflect.DeepEqual(want.Field(field).Interface(), got.Field(field).Interface()) {
							t.Errorf("estado original da aba %d não restaurado: %s", doc.id, want.Type().Field(field).Name)
						}
					}
					t.FailNow()
				}
			}
			files, err := filepath.Glob(filepath.Join(h.d.directory, "rascunhos", "antes-de-desfazer-*.json"))
			if err != nil || len(files) != 1 {
				t.Fatal("a versão posterior não foi arquivada", err)
			}
			var recovery workspaceSnapshot
			if err := store.ReadJSON(files[0], &recovery); err != nil ||
				recovery.Tabs[0].Text != "Combinação editada depois da operação." ||
				recovery.Tabs[0].ProcessedText != "Resposta editada depois da operação." {
				t.Fatal("o arquivo de recuperação perdeu edições posteriores", err)
			}
		})
	}
}

func TestUndoCombinationKeepsEntryAndCurrentWorkspaceOnPersistenceFailure(t *testing.T) {
	for _, failure := range []string{"arquivo de recuperação", "workspace"} {
		t.Run(failure, func(t *testing.T) {
			failWrite := false
			now := time.Now()
			h := newHarness(t, nil, func(deps *dependencies) {
				deps.now = func() time.Time { return now }
				deps.writeWorkspace = func(path string, snapshot workspaceSnapshot) error {
					if failWrite {
						return errors.New("falha de gravação simulada")
					}
					return store.WriteJSON(path, snapshot)
				}
			})
			first := h.d.document
			first.editor.SetText("Primeiro original.")
			second := h.d.addDocument()
			second.editor.SetText("Segundo original.")
			test.Tap(h.d.joinButton)
			first.editor.SetText("Combinação com trabalho posterior.")
			if err := h.d.saveWorkspace(); err != nil {
				t.Fatal(err)
			}
			workspace := filepath.Join(h.d.directory, "workspace-go-v1.json")
			before, err := os.ReadFile(workspace)
			if err != nil {
				t.Fatal(err)
			}
			entry := h.d.closedTab
			drafts := filepath.Join(h.d.directory, "rascunhos")
			if failure == "workspace" {
				failWrite = true
			} else {
				if err := os.Rename(drafts, drafts+".backup"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(drafts, []byte("obstrução sintética"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			h.d.undoCloseDocument()
			after, err := os.ReadFile(workspace)
			if err != nil || string(before) != string(after) || h.d.closedTab != entry ||
				len(h.d.documents) != 1 || !second.closed || first.editor.Text != "Combinação com trabalho posterior." {
				t.Fatal("uma falha consumiu o desfazer ou alterou os editores/workspace", err)
			}
			failWrite = false
			if failure == "arquivo de recuperação" {
				if err := os.Remove(drafts); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(drafts+".backup", drafts); err != nil {
					t.Fatal(err)
				}
			}
			h.d.undoCloseDocument()
			if len(h.d.documents) != 2 || first.editor.Text != "Primeiro original." || second.closed {
				t.Fatal("a falha não permitiu recuperar a operação após corrigir o armazenamento")
			}
		})
	}
}
