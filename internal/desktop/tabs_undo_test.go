package desktop

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"ai-talker/internal/config"
	"ai-talker/internal/processing"
	"ai-talker/internal/store"
	"ai-talker/internal/transcript"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

func TestUndoClosedTabRestoresPositionNameEditorsOptionsAndClearUndo(t *testing.T) {
	now := time.Date(2026, 10, 4, 20, 0, 0, 0, time.UTC)
	h := newHarness(t, nil, func(deps *dependencies) { deps.now = func() time.Time { return now } })
	first := h.d.document
	middle := h.d.addDocument()
	middle.editor.SetText("Texto antes da limpeza.")
	test.Tap(middle.clearButton)
	middle.editor.SetText("Texto que precisa ser preservado.")
	middle.processEditor.SetText("Mensagem pronta que também precisa voltar.")
	middle.lastAudio = "audio-preservado.wav"
	result := resultFor(middle.lastAudio, middle.editor.Text)
	middle.lastResult = &result
	middle.processOptions.Mode = processing.ModeFree
	middle.processOptions.ModelID = processing.Models()[2].ID
	if err := h.d.renameDocument(middle, "Mendes"); err != nil {
		t.Fatal(err)
	}
	last := h.d.addDocument()
	last.editor.SetText("A outra aba continua aqui.")
	h.d.closeDocument(middle.item)
	if !h.d.undoCloseButton.Visible() || len(h.d.documents) != 2 {
		t.Fatal("fechar não ofereceu a restauração temporária")
	}
	now = now.Add(29*time.Second + 900*time.Millisecond)
	h.d.tick()
	button := h.d.undoCloseButton
	position := h.app.Driver().AbsolutePositionForObject(button).Add(fyne.NewPos(12, 12))
	test.MoveMouse(h.d.window.Canvas(), position)
	test.TapCanvas(h.d.window.Canvas(), position)
	if len(h.d.documents) != 3 || h.d.documents[0] != first || h.d.documents[1] != middle ||
		h.d.documents[2] != last || h.d.document != middle || middle.closed || button.Visible() {
		t.Fatal("desfazer não restaurou e selecionou a aba na posição original")
	}
	if middle.item.Text != "Mendes" || middle.editor.Text != "Texto que precisa ser preservado." ||
		middle.processEditor.Text != "Mensagem pronta que também precisa voltar." ||
		middle.lastAudio != "audio-preservado.wav" || middle.lastResult != &result ||
		middle.processOptions.Mode != processing.ModeFree || middle.processOptions.ModelID != processing.Models()[2].ID ||
		last.item.Text != "Aba 3" || last.editor.Text != "A outra aba continua aqui." {
		t.Fatal("restaurar perdeu conteúdo, opções ou nomes das abas")
	}
	if err := h.d.saveWorkspace(); err != nil {
		t.Fatal(err)
	}
	restored := newHarness(t, nil, nil)
	restored.d.directory = h.d.directory
	if !restored.d.loadWorkspace() || len(restored.d.documents) != 3 ||
		restored.d.document.item.Text != "Mendes" || restored.d.processOptions.Mode != processing.ModeFree {
		t.Fatal("a restauração não foi preservada ao reabrir")
	}
	test.Tap(restored.d.undoButton)
	if restored.d.editor.Text != "Texto antes da limpeza." ||
		restored.d.processEditor.Text != "Mensagem pronta que também precisa voltar." {
		t.Fatal("restaurar a aba descartou o desfazer da limpeza")
	}
}

func TestUndoClosedTabUsesCurrentLayoutAndQuality(t *testing.T) {
	for _, layouts := range []struct {
		name   string
		before string
		after  string
	}{
		{"coluna", config.LayoutInline, config.LayoutColumn},
		{"lado a lado", config.LayoutColumn, config.LayoutInline},
	} {
		t.Run(layouts.name, func(t *testing.T) {
			now := time.Date(2026, 10, 4, 20, 0, 0, 0, time.UTC)
			h := newHarness(t, nil, func(deps *dependencies) { deps.now = func() time.Time { return now } })
			previous := h.d.settings.Appearance
			h.d.settings.Appearance.Layout = layouts.before
			h.d.applyAppearance(previous, h.d.settings.Appearance)
			doc := h.d.document
			doc.editor.SetText("Transcrição preservada ao restaurar.")
			doc.processEditor.SetText("Resultado preservado ao restaurar.")
			sourceEditor, resultEditor := doc.editor, doc.processEditor
			h.d.addDocument()
			h.d.closeDocument(doc.item)
			updated := cloneSettings(h.d.settings)
			updated.Appearance.Layout = layouts.after
			updated.Quality = "fast"
			h.d.persistPreferences(updated, func(err error) {
				if err != nil {
					t.Fatal(err)
				}
			})
			h.settle()
			test.Tap(h.d.undoCloseButton)
			h.d.window.Resize(h.d.initialWindowSize())
			if h.d.document != doc || doc.closed || doc.editor != sourceEditor || doc.processEditor != resultEditor ||
				doc.editor.Text != "Transcrição preservada ao restaurar." ||
				doc.processEditor.Text != "Resultado preservado ao restaurar." || doc.qualityBars.levels != 2 {
				t.Fatal("restaurar perdeu os editores ou o modo de qualidade atual")
			}
			transcription := h.app.Driver().AbsolutePositionForObject(doc.editors.Leading)
			processing := h.app.Driver().AbsolutePositionForObject(doc.editors.Trailing)
			if layouts.after == config.LayoutColumn {
				if transcription.X != processing.X || processing.Y < transcription.Y+doc.editors.Leading.Size().Height {
					t.Fatal("a aba restaurada não colocou processamento abaixo da transcrição")
				}
			} else if transcription.Y != processing.Y || processing.X < transcription.X+doc.editors.Leading.Size().Width {
				t.Fatal("a aba restaurada não colocou os editores lado a lado")
			}
		})
	}
}

func TestUndoClosedTabExpiresAtThirtySeconds(t *testing.T) {
	now := time.Date(2026, 10, 4, 20, 0, 0, 0, time.UTC)
	h := newHarness(t, nil, func(deps *dependencies) { deps.now = func() time.Time { return now } })
	closed := h.d.document
	closed.editor.SetText("Conteúdo arquivado ao fechar.")
	h.d.addDocument()
	h.d.closeDocument(closed.item)
	now = now.Add(30 * time.Second)
	h.d.tick()
	test.Tap(h.d.undoCloseButton)
	if h.d.undoCloseButton.Visible() || h.d.closedTab != nil || len(h.d.documents) != 1 || !closed.closed {
		t.Fatal("o desfazer continuou disponível depois de trinta segundos")
	}
	drafts, err := filepath.Glob(filepath.Join(h.d.directory, "rascunhos", "*.txt"))
	if err != nil || len(drafts) == 0 {
		t.Fatal("expirar a restauração descartou o arquivo de rascunho")
	}
}

func TestUndoClosingLastTabKeepsWorkInItsReplacement(t *testing.T) {
	for _, edited := range []bool{false, true} {
		name := "substituta vazia"
		if edited {
			name = "substituta com nova edição"
		}
		t.Run(name, func(t *testing.T) {
			now := time.Date(2026, 10, 4, 20, 0, 0, 0, time.UTC)
			h := newHarness(t, nil, func(deps *dependencies) { deps.now = func() time.Time { return now } })
			closed := h.d.document
			closed.editor.SetText("Texto da única aba.")
			h.d.closeDocument(closed.item)
			replacement := h.d.document
			if edited {
				replacement.editor.SetText("Novo texto depois do fechamento.")
			}
			test.Tap(h.d.undoCloseButton)
			want := 1
			if edited {
				want = 2
			}
			if len(h.d.documents) != want || h.d.document != closed || closed.editor.Text != "Texto da única aba." {
				t.Fatal("desfazer o fechamento da última aba perdeu o texto ou manteve uma substituta vazia")
			}
			if edited && (replacement.closed || replacement.editor.Text != "Novo texto depois do fechamento.") {
				t.Fatal("restaurar apagou o trabalho novo feito na aba substituta")
			}
		})
	}
}

func TestUndoClosedTabUsesLatestClosureAndNewDeadline(t *testing.T) {
	now := time.Date(2026, 10, 4, 20, 0, 0, 0, time.UTC)
	h := newHarness(t, nil, func(deps *dependencies) { deps.now = func() time.Time { return now } })
	first := h.d.document
	second := h.d.addDocument()
	h.d.addDocument()
	h.d.closeDocument(first.item)
	now = now.Add(29 * time.Second)
	h.d.closeDocument(second.item)
	now = now.Add(2 * time.Second)
	h.d.tick()
	test.Tap(h.d.undoCloseButton)
	if len(h.d.documents) != 2 || h.d.document != second || !first.closed || second.closed {
		t.Fatal("o fechamento anterior expirou ou substituiu o desfazer mais recente")
	}
	test.Tap(h.d.undoCloseButton)
	if len(h.d.documents) != 3 || h.d.document != first || first.closed || h.d.undoCloseButton.Visible() {
		t.Fatal("o segundo clique não recuperou o fechamento anterior da sequência")
	}
}

func TestUndoThreeClosuresRestoresInReverseOrderAndKeepsEntryOnSaveFailure(t *testing.T) {
	now := time.Date(2026, 10, 4, 20, 0, 0, 0, time.UTC)
	h := newHarness(t, nil, func(deps *dependencies) { deps.now = func() time.Time { return now } })
	first := h.d.document
	second := h.d.addDocument()
	third := h.d.addDocument()
	h.d.addDocument()
	for _, doc := range []*document{first, second, third} {
		doc.editor.SetText(doc.item.Text + " preservada")
		h.d.closeDocument(doc.item)
	}
	path := filepath.Join(h.d.directory, "workspace-go-v1.json")
	if err := os.Rename(path, path+".backup"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	h.d.undoCloseDocument()
	if !third.closed || len(h.d.documents) != 1 || !h.d.undoCloseButton.Visible() {
		t.Fatal("uma falha consumiu a entrada ou alterou as abas")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	for _, want := range []*document{third, second, first} {
		h.d.undoCloseDocument()
		if want.closed || h.d.document != want {
			t.Fatal("a pilha não recuperou as abas em ordem inversa")
		}
	}
	var saved workspaceSnapshot
	if err := store.ReadJSON(path, &saved); err != nil || len(saved.Tabs) != 4 {
		t.Fatal("a restauração não foi persistida imediatamente", err)
	}
}

func TestUndoClosedTabRejectsLateTranscriptionAndProcessing(t *testing.T) {
	jobs := make(chan context.Context, 1)
	processingJobs := make(chan context.Context, 1)
	release := make(chan struct{})
	releaseJobs := sync.OnceFunc(func() { close(release) })
	h := newHarness(t, runnerFunc(func(ctx context.Context, source string, _ config.Settings, request transcript.Request) (
		transcript.Result, error,
	) {
		jobs <- ctx
		<-release
		request.OnProgress(transcript.Progress{Message: "Progresso antigo", Fraction: 0.9})
		return resultFor(source, "Resposta antiga que não pode substituir o texto."), nil
	}), func(deps *dependencies) {
		now := time.Date(2026, 10, 4, 20, 0, 0, 0, time.UTC)
		deps.now = func() time.Time { return now }
		deps.process = func(ctx context.Context, _ config.Settings, _ processing.Request) (processing.Result, error) {
			processingJobs <- ctx
			<-release
			return processing.Result{Text: "Processamento antigo."}, nil
		}
	})
	t.Cleanup(releaseJobs)
	closed := h.d.document
	h.d.startJob("audio-antigo.wav")
	job := waitValue(t, jobs)
	closed.editor.SetText("Edição durante a espera.")
	h.d.processDocument(closed)
	processJob := waitValue(t, processingJobs)
	h.d.addDocument()
	h.d.closeDocument(closed.item)
	test.Tap(h.d.undoCloseButton)
	closed.editor.SetText("Edição após restaurar.")
	closed.processEditor.SetText("Resultado preservado após restaurar.")
	releaseJobs()
	h.settle()
	if job.Err() == nil || processJob.Err() == nil || h.d.document != closed ||
		closed.editor.Text != "Edição após restaurar." ||
		closed.processEditor.Text != "Resultado preservado após restaurar." ||
		closed.busy || closed.processBusy || closed.errorLabel.Visible() || closed.processError.Visible() {
		t.Fatal("restaurar reativou tarefas canceladas ou aceitou uma resposta atrasada")
	}
	requireAbsent(t, h.d.documentLatestPath(closed))
}

func TestFailedOrRecordingTabClosureDoesNotOfferUndo(t *testing.T) {
	t.Run("erro ao arquivar", func(t *testing.T) {
		h := newHarness(t, nil, nil)
		doc := h.d.document
		doc.editor.SetText("Texto que não pode ser descartado.")
		if err := os.WriteFile(filepath.Join(h.d.directory, "rascunhos"), []byte("obstrução"), 0o600); err != nil {
			t.Fatal(err)
		}
		h.d.closeDocument(doc.item)
		if doc.closed || h.d.undoCloseButton.Visible() || h.d.closedTab != nil || !doc.errorLabel.Visible() {
			t.Fatal("uma falha de salvamento foi tratada como fechamento concluído")
		}
	})
	t.Run("gravação em andamento", func(t *testing.T) {
		h := newHarness(t, nil, nil)
		doc := h.d.document
		test.Tap(h.d.recordButton)
		h.await(func() bool { return h.d.capture == captureRunning })
		h.d.closeDocument(doc.item)
		if doc.closed || h.d.undoCloseButton.Visible() || h.d.closedTab != nil || h.d.capture != captureRunning {
			t.Fatal("fechar a aba de gravação interrompeu a captura ou ofereceu um desfazer incorreto")
		}
	})
}
