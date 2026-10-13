package desktop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"ai-talker/internal/config"
	"ai-talker/internal/recorder"
	"ai-talker/internal/transcript"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

func TestDiscardRecordingRequiresConfirmationAndCancellationKeepsCapture(t *testing.T) {
	r := &fakeRecording{state: recorder.Snapshot{Path: "nova.wav"}}
	var confirm func(bool)
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.newRecorder = func(int, string) recording { return r }
		deps.confirmDiscard = func(_ fyne.Window, callback func(bool)) { confirm = callback }
	})
	if h.d.discardButton.Visible() {
		t.Fatal("descartar apareceu sem uma gravação")
	}
	h.d.editor.SetText("Meu texto anterior.")
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	test.Tap(h.d.pauseButton)
	test.Tap(h.d.discardButton)
	if confirm == nil || !h.d.discardDialog || !r.Snapshot().Active || !r.Snapshot().Paused {
		t.Fatal("o descarte dispensou a confirmação ou mudou a captura antes da resposta")
	}
	h.d.newAudio()
	h.d.requestClose()
	h.d.recordAction()
	if h.d.editor.Text != "Meu texto anterior." || h.d.closing || !r.Snapshot().Active {
		t.Fatal("outro controle passou pela confirmação de descarte pendente")
	}
	confirm(false)
	if h.d.discardDialog || h.d.pauseButton.Disabled() || !h.d.discardButton.Visible() ||
		!r.Snapshot().Active || !r.Snapshot().Paused {
		t.Fatal("cancelar o descarte não preservou a gravação pausada")
	}
}

func TestDiscardRecordingPreservesEditorsFilesAndPreviousAudioLink(t *testing.T) {
	var calls atomic.Int32
	r := &fakeRecording{}
	h := newHarness(t, runnerFunc(func(context.Context, string, config.Settings, transcript.Request) (transcript.Result, error) {
		calls.Add(1)
		return transcript.Result{}, errors.New("descartar não deve transcrever")
	}), func(deps *dependencies) {
		deps.newRecorder = func(int, string) recording { return r }
		deps.confirmDiscard = func(_ fyne.Window, callback func(bool)) { callback(true) }
	})
	recordingPath := filepath.Join(h.d.directory, "nova.wav")
	if err := os.WriteFile(recordingPath, []byte("áudio preservado"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.update(func(s *recorder.Snapshot) { s.Path = recordingPath })
	doc := h.d.document
	previous := resultFor("anterior.wav", "Texto anterior.")
	doc.lastAudio, doc.lastResult = previous.Source, &previous
	doc.recordedAppend = &recordedAppend{Source: previous.Source, Prefix: "Primeira fala.", Combined: previous.Text}
	doc.sourceLabel.SetText("Áudio anterior")
	doc.editor.SetText(previous.Text)
	doc.processEditor.SetText("Mensagem pronta.")
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	doc.editor.SetText("Texto anterior com uma edição durante a captura.")
	second := h.d.addDocument()
	second.editor.SetText("Texto da outra aba.")
	test.Tap(h.d.discardButton)
	h.await(func() bool { return h.d.capture == captureIdle })
	if calls.Load() != 0 || r.Snapshot().Active || h.d.discardButton.Visible() ||
		doc.editor.Text != "Texto anterior com uma edição durante a captura." || doc.processEditor.Text != "Mensagem pronta." ||
		doc.lastAudio != previous.Source || doc.lastResult != &previous || doc.recordedAppend.Source != previous.Source ||
		second.editor.Text != "Texto da outra aba." || doc.retryButton.Visible() {
		t.Fatal("descartar transcreveu, perdeu texto ou alterou o vínculo anterior da aba")
	}
	requireFile(t, recordingPath, "áudio preservado")
	if err := h.d.saveWorkspace(); err != nil {
		t.Fatal(err)
	}
}

func TestDiscardRecordingDoesNotUndoManualClear(t *testing.T) {
	r := &fakeRecording{state: recorder.Snapshot{Path: "nova.wav"}}
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.newRecorder = func(int, string) recording { return r }
		deps.confirmDiscard = func(_ fyne.Window, callback func(bool)) { callback(true) }
	})
	h.d.editor.SetText("Texto que eu vou limpar.")
	h.d.lastAudio = "anterior.wav"
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	test.Tap(h.d.clearButton)
	test.Tap(h.d.discardButton)
	h.await(func() bool { return h.d.capture == captureIdle })
	if h.d.editor.Text != "" || h.d.lastAudio != "" || h.d.lastResult != nil || h.d.recordedAppend != nil {
		t.Fatal("descartar restaurou metadados ou texto que foram limpos manualmente")
	}
	test.Tap(h.d.undoButton)
	if h.d.editor.Text != "Texto que eu vou limpar." {
		t.Fatal("descartar perdeu o desfazer da limpeza manual")
	}
}

func TestDiscardRecordingShowsRealConfirmation(t *testing.T) {
	r := &fakeRecording{state: recorder.Snapshot{Path: "nova.wav"}}
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.newRecorder = func(int, string) recording { return r }
	})
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	test.Tap(h.d.discardButton)
	if !h.d.discardDialog || len(h.d.window.Canvas().Overlays().List()) == 0 || !r.Snapshot().Active {
		t.Fatal("o botão não apresentou o diálogo de confirmação")
	}
}
