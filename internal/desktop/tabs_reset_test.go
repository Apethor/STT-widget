package desktop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"ai-talker/internal/config"
	"ai-talker/internal/processing"
	"ai-talker/internal/recorder"
	"ai-talker/internal/store"
	"ai-talker/internal/transcript"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func TestCloseAllTabsShowsConfirmationAndCanBeCancelled(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.d.editor.SetText("Texto que não pode sumir ao cancelar.")
	second := h.d.addDocument()
	second.processEditor.SetText("Resultado da segunda aba.")
	h.d.requestCloseTabs()
	overlays := h.d.window.Canvas().Overlays().List()
	if len(overlays) != 1 || !h.d.tabsDialog || !h.d.groupMenu().Items[5].Disabled {
		t.Fatal("fechar todas não abriu a confirmação")
	}
	var cancel, confirm *widget.Button
	for _, object := range test.LaidOutObjects(overlays[0]) {
		if button, ok := object.(*widget.Button); ok {
			switch button.Text {
			case "Cancelar":
				cancel = button
			case "Fechar e limpar":
				confirm = button
			}
		}
	}
	if cancel == nil || confirm == nil {
		t.Fatal("a confirmação não oferece cancelar e fechar/limpar")
	}
	test.Tap(cancel)
	if h.d.tabsDialog || h.d.groupMenu().Items[5].Disabled || len(h.d.documents) != 2 ||
		h.d.documents[0].editor.Text != "Texto que não pode sumir ao cancelar." ||
		second.processEditor.Text != "Resultado da segunda aba." {
		t.Fatal("cancelar a confirmação alterou as abas")
	}
}

func TestCloseAllTabsKeepsFirstEmptyArchivesTextsAndPersists(t *testing.T) {
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.confirmTabs = func(_ fyne.Window, complete func(bool)) { complete(true) }
	})
	first := h.d.document
	first.editor.SetText("Transcrição da primeira.")
	first.processEditor.SetText("Resultado da primeira.")
	second := h.d.addDocument()
	second.editor.SetText("Transcrição da segunda.")
	second.processEditor.SetText("Resultado da segunda.")
	second.undo = &clearUndo{Text: "Última limpeza."}
	third := h.d.addDocument()
	third.editor.SetText("Transcrição da terceira.")
	source := filepath.Join(h.d.directory, "gravacao-preservada.wav")
	export := filepath.Join(h.d.directory, "exportacao-preservada.txt")
	for path, content := range map[string]string{source: "áudio", export: "exportação"} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	h.d.requestCloseTabs()
	if len(h.d.documents) != 1 || h.d.document != first || first.editor.Text != "" ||
		first.processEditor.Text != "" || first.lastAudio != "" || first.lastResult != nil ||
		first.undo != nil || !second.closed || !third.closed ||
		h.d.undoCloseButton.Visible() || h.d.closedTab != nil {
		t.Fatal("fechar todas não deixou a primeira aba vazia")
	}
	if !first.copyButton.Disabled() || !first.processCopyButton.Disabled() ||
		first.item.Text != "Aba 1" || h.d.recordState.Text != "Pronto para começar" {
		t.Fatal("os controles não acompanharam a aba vazia")
	}
	requireFile(t, source, "áudio")
	requireFile(t, export, "exportação")
	var saved workspaceSnapshot
	if err := store.ReadJSON(filepath.Join(h.d.directory, "workspace-go-v1.json"), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Selected != first.id || len(saved.Tabs) != 1 ||
		saved.Tabs[0].Text != "" || saved.Tabs[0].ProcessedText != "" {
		t.Fatal("a limpeza não foi persistida")
	}
	files, err := filepath.Glob(filepath.Join(h.d.directory, "rascunhos", "*.txt"))
	if err != nil || len(files) != 5 {
		t.Fatalf("os cinco textos não foram preservados antes da limpeza: %v, %v", files, err)
	}
}

func TestCloseAllTabsWriteFailureKeepsEveryEditor(t *testing.T) {
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.confirmTabs = func(_ fyne.Window, complete func(bool)) { complete(true) }
	})
	first := h.d.document
	first.editor.SetText("Primeira transcrição.")
	second := h.d.addDocument()
	second.editor.SetText("Segunda transcrição.")
	second.processEditor.SetText("Resultado que deve continuar aqui.")
	if err := os.WriteFile(filepath.Join(h.d.directory, "rascunhos"), []byte("pasta indisponível"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.d.requestCloseTabs()
	if len(h.d.documents) != 2 || first.closed || second.closed ||
		first.editor.Text != "Primeira transcrição." || second.editor.Text != "Segunda transcrição." ||
		second.processEditor.Text != "Resultado que deve continuar aqui." {
		t.Fatal("falhar ao preservar os textos limpou ou fechou alguma aba")
	}
	if !h.d.errorLabel.Visible() {
		t.Fatal("a falha não foi apresentada na aba selecionada")
	}
}

func TestCloseAllTabsDoesNotInterruptRecordingOrStartItInsideConfirmation(t *testing.T) {
	var complete func(bool)
	captured := &fakeRecording{state: recorder.Snapshot{Path: "audio.wav"}}
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.newRecorder = func(int, string) recording { return captured }
		deps.confirmTabs = func(_ fyne.Window, callback func(bool)) { complete = callback }
	})
	h.d.requestCloseTabs()
	test.Tap(h.d.recordButton)
	if starts, _ := captured.counts(); starts != 0 || complete == nil {
		t.Fatal("a confirmação permitiu iniciar uma gravação")
	}
	complete(false)
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	complete = nil
	h.d.requestCloseTabs()
	if complete != nil || !h.d.groupMenu().Items[5].Disabled || !captured.Snapshot().Active {
		t.Fatal("fechar todas interferiu numa gravação ativa")
	}
}

func TestCloseAllTabsSessionWriteFailureKeepsOriginalTabs(t *testing.T) {
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.confirmTabs = func(_ fyne.Window, complete func(bool)) { complete(true) }
	})
	first := h.d.document
	first.editor.SetText("Primeira transcrição.")
	second := h.d.addDocument()
	second.editor.SetText("Segunda transcrição.")
	path := filepath.Join(h.d.directory, "workspace-go-v1.json")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	h.d.requestCloseTabs()
	if len(h.d.documents) != 2 || h.d.document != second || first.closed || second.closed ||
		first.editor.Text != "Primeira transcrição." || second.editor.Text != "Segunda transcrição." ||
		!h.d.errorLabel.Visible() {
		t.Fatal("a falha ao salvar a sessão limpou ou fechou as abas originais")
	}
}

func TestCloseAllTabsRejectsLateTranscriptionAndProcessing(t *testing.T) {
	release := make(chan struct{})
	releaseJobs := sync.OnceFunc(func() { close(release) })
	audioStarted, textStarted := make(chan context.Context, 1), make(chan context.Context, 1)
	h := newHarness(t, runnerFunc(func(ctx context.Context, source string, _ config.Settings, _ transcript.Request) (
		transcript.Result, error,
	) {
		audioStarted <- ctx
		<-release
		return resultFor(source, "Transcrição antiga."), nil
	}), func(deps *dependencies) {
		deps.confirmTabs = func(_ fyne.Window, complete func(bool)) { complete(true) }
		deps.process = func(ctx context.Context, _ config.Settings, _ processing.Request) (processing.Result, error) {
			textStarted <- ctx
			<-release
			return processing.Result{Text: "Resultado antigo."}, nil
		}
	})
	t.Cleanup(releaseJobs)
	h.d.startJob("audio.wav")
	audio := waitValue(t, audioStarted)
	h.d.addDocument()
	h.d.editor.SetText("Texto que está sendo processado.")
	test.Tap(h.d.processButton)
	text := waitValue(t, textStarted)
	h.d.requestCloseTabs()
	releaseJobs()
	h.settle()
	if audio.Err() == nil || text.Err() == nil || len(h.d.documents) != 1 ||
		h.d.editor.Text != "" || h.d.processEditor.Text != "" || h.d.hasJobs() {
		t.Fatal("uma tarefa antiga recolocou conteúdo após limpar as abas")
	}
	if _, err := os.Stat(h.d.latestPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("o resultado antigo reapareceu no snapshot selecionado")
	}
}
