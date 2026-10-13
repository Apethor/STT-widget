package desktop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"ai-talker/internal/config"
	"ai-talker/internal/processing"
	"ai-talker/internal/recorder"
	"ai-talker/internal/transcript"

	"fyne.io/fyne/v2/test"
)

func TestDocumentsKeepEditorsResultsAndUndoIsolated(t *testing.T) {
	h := newHarness(t, nil, nil)
	first := h.d.document
	first.editor.SetText("  Primeira mensagem.\nCom quebra de linha.  ")
	first.processEditor.SetText("Resultado da primeira aba.")
	test.Tap(first.clearButton)
	if first.undoButton.Disabled() || first.editor.Text != "" {
		t.Fatal("a limpeza não ofereceu desfazer")
	}
	second := h.d.addDocument()
	second.editor.SetText("Texto da segunda aba.")
	second.processEditor.SetText("Resultado da segunda aba.")
	if !second.undoButton.Disabled() {
		t.Fatal("a segunda aba recebeu a limpeza da primeira")
	}
	h.d.tabs.Select(first.item)
	test.Tap(first.undoButton)
	if first.editor.Text != "  Primeira mensagem.\nCom quebra de linha.  " ||
		first.processEditor.Text != "Resultado da primeira aba." || !first.undoButton.Disabled() {
		t.Fatal("desfazer não recuperou exatamente o texto da primeira aba")
	}
	h.d.tabs.Select(second.item)
	if h.d.editor.Text != "Texto da segunda aba." || h.d.processEditor.Text != "Resultado da segunda aba." {
		t.Fatal("desfazer alterou os editores da outra aba")
	}
}

func TestUndoClearRestoresLastNonemptyClearAndPreservesNewDraft(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.d.editor.SetText("Primeiro texto.")
	test.Tap(h.d.clearButton)
	test.Tap(h.d.clearButton)
	test.Tap(h.d.undoButton)
	if h.d.editor.Text != "Primeiro texto." {
		t.Fatal("limpar novamente um editor vazio apagou a última limpeza")
	}
	h.d.editor.SetText("Segundo texto.")
	test.Tap(h.d.clearButton)
	h.d.editor.SetText("Edição nova antes de desfazer.")
	test.Tap(h.d.undoButton)
	if h.d.editor.Text != "Segundo texto." {
		t.Fatal("desfazer não restaurou a limpeza mais recente")
	}
	drafts, err := filepath.Glob(filepath.Join(h.d.directory, "rascunhos", "*.txt"))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, path := range drafts {
		payload, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		found = found || strings.Contains(string(payload), "Edição nova antes de desfazer.")
	}
	if !found {
		t.Fatal("desfazer descartou a edição feita depois da limpeza")
	}
}

func TestDocumentTranscriptionFinishesInItsOriginTab(t *testing.T) {
	calls := make(chan transcript.Request, 1)
	release := make(chan struct{})
	releaseJob := sync.OnceFunc(func() { close(release) })
	h := newHarness(t, runnerFunc(func(_ context.Context, source string, _ config.Settings, request transcript.Request) (
		transcript.Result, error,
	) {
		calls <- request
		<-release
		return resultFor(source, "Resultado da aba original."), nil
	}), nil)
	t.Cleanup(releaseJob)
	first := h.d.document
	h.d.startJob("primeira.wav")
	request := waitValue(t, calls)
	second := h.d.addDocument()
	second.editor.SetText("Estou editando outra aba.")
	before := h.d.status.Text
	request.OnProgress(transcript.Progress{Message: "Progresso da primeira aba", Fraction: 0.7})
	releaseJob()
	h.await(func() bool { return !first.busy })
	if second.editor.Text != "Estou editando outra aba." || h.d.status.Text != before || second.errorLabel.Visible() {
		t.Fatal("uma tarefa de outra aba alterou a aba selecionada")
	}
	requireAbsent(t, h.d.latestPath())
	if first.editor.Text != "Resultado da aba original." {
		t.Fatal("a tarefa não terminou na aba em que começou")
	}
	h.d.tabs.Select(first.item)
	if h.d.editor.Text != first.editor.Text ||
		readResult(t, h.d.latestPath()).Text != "Resultado da aba original." {
		t.Fatal("selecionar a primeira aba não recuperou seu resultado")
	}
}

func TestDocumentErrorsStayInTheirOriginTab(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	releaseJob := sync.OnceFunc(func() { close(release) })
	h := newHarness(t, runnerFunc(func(context.Context, string, config.Settings, transcript.Request) (
		transcript.Result, error,
	) {
		started <- struct{}{}
		<-release
		return transcript.Result{}, &transcript.Error{
			Cause: errors.New("erro da primeira aba"), PartialText: "Texto parcial da primeira.",
		}
	}), nil)
	t.Cleanup(releaseJob)
	first := h.d.document
	h.d.startJob("primeira.wav")
	waitValue(t, started)
	second := h.d.addDocument()
	second.editor.SetText("Texto atual da segunda.")
	releaseJob()
	h.await(func() bool { return !first.busy })
	if first.editor.Text != "Texto parcial da primeira." || !first.errorLabel.Visible() ||
		second.editor.Text != "Texto atual da segunda." || second.errorLabel.Visible() {
		t.Fatal("o erro parcial atravessou as abas")
	}
}

func TestClosingDocumentCancelsAndRejectsItsLateResult(t *testing.T) {
	started := make(chan context.Context, 1)
	release := make(chan struct{})
	releaseJob := sync.OnceFunc(func() { close(release) })
	h := newHarness(t, runnerFunc(func(ctx context.Context, source string, _ config.Settings, _ transcript.Request) (
		transcript.Result, error,
	) {
		started <- ctx
		<-release
		return resultFor(source, "Texto de uma aba fechada."), nil
	}), nil)
	t.Cleanup(releaseJob)
	first := h.d.document
	h.d.startJob("primeira.wav")
	job := waitValue(t, started)
	second := h.d.addDocument()
	second.editor.SetText("Aba que continua aberta.")
	h.d.closeDocument(first.item)
	releaseJob()
	h.settle()
	if job.Err() == nil || !first.closed || len(h.d.documents) != 1 ||
		h.d.editor.Text != "Aba que continua aberta." {
		t.Fatal("a aba fechada não cancelou sua tarefa ou reapareceu")
	}
	requireAbsent(t, h.d.documentLatestPath(first))
}

func TestRecordingRemainsAttachedToItsOriginWhileSwitchingTabs(t *testing.T) {
	r := &fakeRecording{state: recorder.Snapshot{Path: "primeira-gravacao.wav"}}
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.newRecorder = func(int, string) recording { return r }
	})
	first := h.d.document
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	second := h.d.addDocument()
	second.editor.SetText("Segunda aba independente.")
	test.Tap(h.d.clearButton)
	test.Tap(h.d.undoButton)
	if !r.Snapshot().Active || h.d.recordButton.Text != "■  Parar e transcrever" {
		t.Fatal("mudar de aba ou desfazer a limpeza interrompeu a gravação")
	}
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureIdle && !first.busy && first.lastResult != nil })
	if first.lastAudio != "primeira-gravacao.wav" || first.editor.Text != "Texto desta gravação." ||
		second.editor.Text != "Segunda aba independente." || second.lastAudio != "" {
		t.Fatal("a gravação foi atribuída à aba selecionada depois do início")
	}
}

func TestWorkspaceRestoresTabsSelectionProcessingAndUndo(t *testing.T) {
	h := newHarness(t, nil, nil)
	first := h.d.document
	first.editor.SetText("Texto para restaurar.")
	test.Tap(first.clearButton)
	first.processEditor.SetText("Mensagem anterior.")
	second := h.d.addDocument()
	second.editor.SetText("Prompt livre da segunda aba.")
	second.processOptions.Mode = processing.ModeFree
	second.processOptions.ModelID = processing.Models()[0].ID
	second.processEditor.SetText("Resposta da segunda aba.")
	directory := h.d.directory
	h.d.finish()
	h.settle()
	restored := newHarness(t, nil, nil)
	restored.d.directory = directory
	if !restored.d.loadWorkspace() || len(restored.d.documents) != 2 {
		t.Fatal("as abas não foram restauradas")
	}
	if restored.d.recordState.Text != "Pronto para começar" {
		t.Fatalf("o gravador não abriu pronto para gravar: %q", restored.d.recordState.Text)
	}
	if restored.d.id != second.id || restored.d.editor.Text != "Prompt livre da segunda aba." ||
		restored.d.processEditor.Text != "Resposta da segunda aba." ||
		restored.d.processOptions.Mode != processing.ModeFree ||
		restored.d.processModel.Selected != processing.Models()[0].Name {
		t.Fatal("a seleção, os resultados ou as opções da aba foram perdidos")
	}
	restored.d.tabs.Select(restored.d.documents[0].item)
	test.Tap(restored.d.undoButton)
	if restored.d.editor.Text != "Texto para restaurar." ||
		restored.d.processEditor.Text != "Mensagem anterior." {
		t.Fatal("a última limpeza não pôde ser desfeita após restaurar as abas")
	}
}

func TestRestoredGeneratedOutputIsArchivedBeforeReplacement(t *testing.T) {
	for _, action := range []string{"novo áudio", "fechar aba", "processar novamente"} {
		t.Run(action, func(t *testing.T) {
			const previous = "Mensagem gerada que ainda não foi editada ou exportada."
			h := newHarness(t, nil, nil)
			h.d.editor.SetText("Texto original da mensagem.")
			h.d.processResult = &processing.Result{Text: previous}
			h.d.processEditor.SetText(previous)
			directory := h.d.directory
			h.d.finish()
			h.settle()
			drafts, err := filepath.Glob(filepath.Join(directory, "rascunhos", "Processado-*.txt"))
			if err != nil || len(drafts) != 0 {
				t.Fatalf("o resultado deveria existir somente na sessão salva: %v, %v", drafts, err)
			}
			restored := newHarness(t, nil, func(deps *dependencies) {
				deps.process = func(context.Context, config.Settings, processing.Request) (processing.Result, error) {
					return processing.Result{Text: "Nova resposta."}, nil
				}
			})
			restored.d.directory = directory
			if !restored.d.loadWorkspace() {
				t.Fatal("a sessão não foi restaurada")
			}
			switch action {
			case "novo áudio":
				test.Tap(restored.d.newButton)
			case "fechar aba":
				restored.d.closeDocument(restored.d.item)
			case "processar novamente":
				test.Tap(restored.d.processButton)
				restored.await(func() bool { return !restored.d.processBusy })
			}
			if err := restored.d.saveWorkspace(); err != nil {
				t.Fatal(err)
			}
			drafts, err = filepath.Glob(filepath.Join(directory, "rascunhos", "Processado-*.txt"))
			if err != nil {
				t.Fatal(err)
			}
			preserved := false
			for _, path := range drafts {
				payload, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				preserved = preserved || string(payload) == previous+"\n"
			}
			if !preserved {
				t.Fatal("substituir o resultado restaurado descartou sua única cópia")
			}
		})
	}
}

func TestInvalidWorkspaceIsPreservedAndAppStaysUsable(t *testing.T) {
	h := newHarness(t, nil, nil)
	path := filepath.Join(h.d.directory, "workspace-go-v1.json")
	payload := `{"version":1,"next_id":1,"tabs":[{"id":1},{"id":1}]}`
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	if !h.d.loadWorkspace() || !h.d.errorLabel.Visible() || h.d.recordButton.Disabled() {
		t.Fatal("a sessão inválida não manteve uma aba recuperável")
	}
	requireFile(t, path, payload)
	h.d.editor.SetText("Ainda posso editar.")
	if h.d.copyButton.Disabled() || h.d.processButton.Disabled() {
		t.Fatal("a sessão inválida bloqueou os editores")
	}
}
