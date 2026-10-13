package desktop

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"ai-talker/internal/config"
	"ai-talker/internal/platform"
	"ai-talker/internal/recorder"
	"ai-talker/internal/transcript"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func TestCloseDuringTranscriptionHidesAndPersistsBeforeExit(t *testing.T) {
	started := make(chan context.Context, 1)
	release := make(chan struct{})
	releaseJob := sync.OnceFunc(func() { close(release) })
	h := newHarness(t, runnerFunc(func(ctx context.Context, source string, _ config.Settings, _ transcript.Request) (
		transcript.Result, error,
	) {
		started <- ctx
		<-release
		return resultFor(source, "Texto concluído em segundo plano."), nil
	}), nil)
	t.Cleanup(releaseJob)
	h.d.startJob("audio.wav")
	ctx := waitValue(t, started)
	h.d.requestClose()
	if !h.d.hidden || !h.d.closeAfterJob || h.d.closing || ctx.Err() != nil || h.d.ctx.Err() != nil {
		t.Fatal("fechar deve apenas ocultar enquanto a transcrição continua")
	}
	releaseJob()
	h.await(func() bool { return h.d.closing })
	saved := readResult(t, h.d.latestPath())
	if saved.Text != "Texto concluído em segundo plano." || saved.Source != "audio.wav" {
		t.Fatal("o fechamento ocorreu antes de salvar o resultado atual")
	}
}

func TestReopenDuringTranscriptionKeepsWindowAfterCompletion(t *testing.T) {
	started := make(chan context.Context, 1)
	release := make(chan struct{})
	releaseJob := sync.OnceFunc(func() { close(release) })
	var activations atomic.Int32
	h := newHarness(t, runnerFunc(func(ctx context.Context, source string, _ config.Settings, _ transcript.Request) (
		transcript.Result, error,
	) {
		started <- ctx
		<-release
		return resultFor(source, "A janela continua aberta."), nil
	}), func(deps *dependencies) {
		deps.activate = func() error { activations.Add(1); return nil }
	})
	t.Cleanup(releaseJob)
	h.d.startJob("audio.wav")
	ctx := waitValue(t, started)
	h.d.requestClose()
	h.d.activateWindow()
	if h.d.hidden || h.d.closeAfterJob || !h.d.busy || ctx.Err() != nil {
		t.Fatal("reabrir cancelou a tarefa ou manteve o fechamento automático")
	}
	releaseJob()
	h.await(func() bool { return !h.d.busy })
	h.settle()
	if h.d.closing || h.d.editor.Text != "A janela continua aberta." || activations.Load() != 1 {
		t.Fatal("a janela reaberta foi fechada ou perdeu o resultado")
	}
}

func TestHiddenWindowReopensIfLatestCannotBePersisted(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	releaseJob := sync.OnceFunc(func() { close(release) })
	h := newHarness(t, runnerFunc(func(_ context.Context, source string, _ config.Settings, _ transcript.Request) (
		transcript.Result, error,
	) {
		started <- struct{}{}
		<-release
		return resultFor(source, "Texto que precisa ser preservado."), nil
	}), nil)
	t.Cleanup(releaseJob)
	h.d.startJob("audio.wav")
	waitValue(t, started)
	if err := os.MkdirAll(filepath.Join(h.d.latestPath(), "bloqueio"), 0o700); err != nil {
		t.Fatal(err)
	}
	h.d.requestClose()
	releaseJob()
	h.await(func() bool { return !h.d.busy })
	if h.d.hidden || h.d.closing || h.d.closeAfterJob || !h.d.errorLabel.Visible() {
		t.Fatal("a falha de persistência fechou a janela com texto ainda recuperável")
	}
	if h.d.editor.Text != "Texto que precisa ser preservado." || h.d.copyButton.Disabled() {
		t.Fatal("a falha de persistência descartou ou bloqueou o texto")
	}
}

func TestCloseDuringRecordingCanBeCancelled(t *testing.T) {
	var confirm func(bool)
	r := &fakeRecording{state: recorder.Snapshot{Path: "gravacao.wav"}}
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.newRecorder = func(int, string) recording { return r }
		deps.confirmClose = func(_ fyne.Window, callback func(bool)) { confirm = callback }
	})
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	h.d.requestClose()
	if confirm == nil || h.d.closing || !r.Snapshot().Active {
		t.Fatal("fechar deveria aguardar a confirmação durante a gravação")
	}
	confirm(false)
	_, stops := r.counts()
	if h.d.closeDialog || h.d.closing || stops != 0 || !r.Snapshot().Active {
		t.Fatal("cancelar o fechamento interrompeu a gravação")
	}
}

func TestCloseDuringRecordingSavesBeforeExit(t *testing.T) {
	source := filepath.Join(t.TempDir(), "gravacao.wav")
	if err := os.WriteFile(source, []byte("áudio preservado"), 0o600); err != nil {
		t.Fatal(err)
	}
	stopping := make(chan struct{}, 1)
	release := make(chan struct{})
	releaseStop := sync.OnceFunc(func() { close(release) })
	r := &fakeRecording{
		state: recorder.Snapshot{Path: source},
		stop: func() (string, error) {
			stopping <- struct{}{}
			<-release
			return source, nil
		},
	}
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.newRecorder = func(int, string) recording { return r }
	})
	t.Cleanup(releaseStop)
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	h.d.requestClose()
	waitValue(t, stopping)
	if h.d.closing || h.d.capture != captureStopping || h.d.clearButton.Disabled() {
		t.Fatal("o fechamento não aguardou a finalização assíncrona do áudio")
	}
	releaseStop()
	h.await(func() bool { return h.d.closing })
	requireFile(t, source, "áudio preservado")
	requireAbsent(t, h.d.latestPath())
}

func TestCloseRecordingFailureLeavesWindowRecoverable(t *testing.T) {
	r := &fakeRecording{
		state: recorder.Snapshot{Path: "preservado.wav"},
		stop:  func() (string, error) { return "preservado.wav", errors.New("falha ao finalizar WAV") },
	}
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.newRecorder = func(int, string) recording { return r }
	})
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	h.d.requestClose()
	h.await(func() bool { return h.d.capture == captureIdle })
	if h.d.closing || h.d.hidden || !h.d.errorLabel.Visible() {
		t.Fatal("a falha de fechamento deveria manter o erro visível")
	}
	if h.d.clearButton.Disabled() || h.d.recordButton.Disabled() || h.d.lastAudio != "preservado.wav" {
		t.Fatal("a falha de fechamento bloqueou os controles ou perdeu o arquivo")
	}
}

func TestCopyRetriesBusyClipboardWithClickTimeEditor(t *testing.T) {
	attempted := make(chan string, 3)
	var attempts atomic.Int32
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.copy = func(text string) error {
			attempted <- text
			if attempts.Add(1) < 3 {
				return platform.ErrClipboardBusy
			}
			return nil
		}
	})
	h.d.editor.SetText("Texto atual ao copiar.")
	test.Tap(h.d.copyButton)
	waitValue(t, attempted)
	h.d.editor.SetText("Posso continuar editando durante a tentativa.")
	if h.d.clearButton.Disabled() || h.d.saveButton.Disabled() {
		t.Fatal("uma tentativa de clipboard bloqueou os controles")
	}
	h.await(func() bool { return h.d.status.Text == "Texto copiado." })
	for range 2 {
		if text := waitValue(t, attempted); text != "Texto atual ao copiar." {
			t.Fatalf("a tentativa copiou outro conteúdo: %q", text)
		}
	}
	if attempts.Load() != 3 {
		t.Fatal("não houve as três tentativas limitadas")
	}
}

func TestCopyBusyClipboardExhaustionIsNonmodal(t *testing.T) {
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.copy = func(string) error { return platform.ErrClipboardBusy }
	})
	h.d.editor.SetText("Texto atual.")
	test.Tap(h.d.copyButton)
	h.await(func() bool { return strings.Contains(h.d.status.Text, "ocupada") })
	if h.d.clearButton.Disabled() || h.d.copyButton.Disabled() || h.d.errorLabel.Visible() {
		t.Fatal("a área de transferência ocupada bloqueou o fluxo")
	}
	if len(h.d.window.Canvas().Overlays().List()) != 0 {
		t.Fatal("a área de transferência ocupada abriu um modal")
	}
}

type selectionWriter struct {
	buffer   bytes.Buffer
	write    func([]byte) (int, error)
	closed   atomic.Int32
	closeErr error
}

func (w *selectionWriter) Write(payload []byte) (int, error) {
	if w.write != nil {
		return w.write(payload)
	}
	return w.buffer.Write(payload)
}

func (w *selectionWriter) Close() error {
	w.closed.Add(1)
	return w.closeErr
}

func (w *selectionWriter) URI() fyne.URI { return storage.NewFileURI("transcricao.txt") }

func TestSaveSelectionWritesCurrentEditorWhileServiceWaits(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	releaseJob := sync.OnceFunc(func() { close(release) })
	h := newHarness(t, runnerFunc(func(_ context.Context, source string, _ config.Settings, _ transcript.Request) (
		transcript.Result, error,
	) {
		started <- struct{}{}
		<-release
		return resultFor(source, "Transcrição concluída."), nil
	}), nil)
	t.Cleanup(releaseJob)
	h.d.startJob("audio.wav")
	waitValue(t, started)
	h.d.editor.SetText("Edição atual com português e TypeScript.")
	writer := &selectionWriter{}
	h.d.saveSelection(writer)
	h.await(func() bool { return h.d.status.Text == "Texto salvo." })
	if writer.buffer.String() != "Edição atual com português e TypeScript.\n" || writer.closed.Load() != 1 {
		t.Fatal("salvar usou o resultado anterior ou não fechou o arquivo")
	}
	if !h.d.busy || h.d.clearButton.Disabled() || h.d.saveButton.Disabled() {
		t.Fatal("salvar o editor interrompeu a transcrição ou bloqueou os controles")
	}
	releaseJob()
	h.await(func() bool { return !h.d.busy })
}

func TestSaveSelectionFailureClosesWriterAndKeepsEditor(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.d.editor.SetText("Texto recuperável.")
	writer := &selectionWriter{
		write: func([]byte) (int, error) { return 0, errors.New("disco cheio") },
	}
	h.d.saveSelection(writer)
	h.await(func() bool { return h.d.errorLabel.Visible() })
	if writer.closed.Load() != 1 || h.d.editor.Text != "Texto recuperável." || h.d.copyButton.Disabled() {
		t.Fatal("a falha de exportação deixou o arquivo aberto ou descartou o texto")
	}
	if len(h.d.window.Canvas().Overlays().List()) != 0 {
		t.Fatal("a falha de exportação deve ser não modal")
	}
}

func TestSaveSelectionFinishesWhenGUIExits(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.d.editor.SetText("Texto confirmado antes de fechar.")
	writer := &selectionWriter{}
	h.d.saveSelection(writer)
	h.d.finish()
	h.settle()
	if writer.buffer.String() != "Texto confirmado antes de fechar.\n" || writer.closed.Load() != 1 {
		t.Fatal("encerrar descartou uma exportação já confirmada")
	}
}

func TestOpenFolderMenuChoosesRecordingOrScreenshotDirectory(t *testing.T) {
	opened := make(chan string, 2)
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.openFolder = func(path string) error { opened <- path; return nil }
	})
	folder := recordingFolder(h.d.directory)
	result := resultFor("audio.wav", "Texto.")
	result.Exports = map[string]string{"txt": filepath.Join(h.d.directory, "exportacoes", "audio.txt")}
	h.d.showResult(result, false)
	test.Tap(h.d.folderButton)
	menu := h.d.window.Canvas().Focused().(*widget.PopUpMenu)
	test.Tap(menu.Items[0].(fyne.Tappable))
	if path := waitValue(t, opened); path != folder {
		t.Fatalf("abrir pasta escolheu %q", path)
	}
	h.settle()
	test.Tap(h.d.clearButton)
	test.Tap(h.d.folderButton)
	menu = h.d.window.Canvas().Focused().(*widget.PopUpMenu)
	test.Tap(menu.Items[1].(fyne.Tappable))
	if path := waitValue(t, opened); path != filepath.Join(h.d.directory, "screenshots") {
		t.Fatalf("abrir screenshots após limpar escolheu %q", path)
	}
}

func TestFinishCancelsAppOperationsAndStopsRecording(t *testing.T) {
	r := &fakeRecording{state: recorder.Snapshot{Path: "gravacao.wav"}}
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.newRecorder = func(int, string) recording { return r }
	})
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	h.d.finish()
	h.settle()
	_, stops := r.counts()
	if !h.d.closing || h.d.ctx.Err() == nil || stops != 1 || r.Snapshot().Active {
		t.Fatal("encerrar não liberou o gravador e suas operações")
	}
}
