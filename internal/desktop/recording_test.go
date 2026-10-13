package desktop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"ai-talker/internal/recorder"

	"fyne.io/fyne/v2/test"
)

type fakeRecording struct {
	mu     sync.Mutex
	state  recorder.Snapshot
	ctx    context.Context
	start  func(context.Context) (string, error)
	stop   func() (string, error)
	starts int
	stops  int
}

func (r *fakeRecording) Start(ctx context.Context) (string, error) {
	r.mu.Lock()
	r.ctx = ctx
	r.starts++
	path := r.state.Path
	r.mu.Unlock()
	var err error
	if r.start != nil {
		path, err = r.start(ctx)
	}
	r.mu.Lock()
	r.state.Path = path
	r.state.Active = err == nil
	r.mu.Unlock()
	return path, err
}

func (r *fakeRecording) Stop() (string, error) {
	r.mu.Lock()
	r.stops++
	path := r.state.Path
	r.mu.Unlock()
	var err error
	if r.stop != nil {
		path, err = r.stop()
	}
	r.mu.Lock()
	r.state.Path = path
	r.state.Active = false
	r.state.Paused = false
	r.mu.Unlock()
	return path, err
}

func (r *fakeRecording) Pause() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.state.Paused = true
	r.state.Level = 0
}

func (r *fakeRecording) Resume() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.state.Paused = false
}

func (r *fakeRecording) Snapshot() recorder.Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state
}

func (r *fakeRecording) counts() (starts, stops int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.starts, r.stops
}

func (r *fakeRecording) context() context.Context {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ctx
}

func (r *fakeRecording) update(update func(*recorder.Snapshot)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	update(&r.state)
}

func TestRecordingClearKeepsCaptureAndPauseControls(t *testing.T) {
	r := &fakeRecording{state: recorder.Snapshot{Path: "gravacao.wav", Duration: 5, Level: 0.3}}
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.newRecorder = func(int, string) recording { return r }
	})
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	h.d.editor.SetText("Edição durante a gravação.")
	test.Tap(h.d.clearButton)
	_, stops := r.counts()
	if !r.Snapshot().Active || stops != 0 || r.context().Err() != nil {
		t.Fatal("limpar interrompeu a captura")
	}
	if h.d.recordButton.Text != "■  Parar e transcrever" || h.d.pauseButton.Disabled() {
		t.Fatal("a gravação deixou de poder ser parada ou pausada")
	}
	test.Tap(h.d.pauseButton)
	if !r.Snapshot().Paused || h.d.pauseButton.hint.text != "Retomar" {
		t.Fatal("pausar não atualizou a captura e os controles")
	}
	h.d.tick()
	if h.d.timer.Text != "00:05" {
		t.Fatal("o intervalo pausado mudou a duração apresentada")
	}
	test.Tap(h.d.pauseButton)
	if r.Snapshot().Paused || h.d.pauseButton.hint.text != "Pausar" {
		t.Fatal("retomar não recuperou a gravação")
	}
}

func TestRecordingNewAudioFinalizesFileWithoutBlockingEditor(t *testing.T) {
	source := filepath.Join(t.TempDir(), "gravacao.wav")
	if err := os.WriteFile(source, []byte("áudio original"), 0o600); err != nil {
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
	h.d.editor.SetText("Minha edição.")
	test.Tap(h.d.newButton)
	waitValue(t, stopping)
	if h.d.editor.Text != "" || h.d.capture != captureStopping {
		t.Fatal("novo áudio deveria limpar o editor e finalizar em segundo plano")
	}
	if h.d.clearButton.Disabled() || h.d.editor.Disabled() {
		t.Fatal("salvar o WAV bloqueou o editor")
	}
	h.d.editor.SetText("Ainda posso editar.")
	test.Tap(h.d.clearButton)
	releaseStop()
	h.await(func() bool { return h.d.capture == captureIdle })
	requireFile(t, source, "áudio original")
	if h.d.lastAudio != "" || h.d.timer.Text != "00:00" || r.Snapshot().Active {
		t.Fatal("a interface não terminou a preparação de novo áudio")
	}
}

func TestRecordingStartIsAsyncAndClearDoesNotCancelIt(t *testing.T) {
	started := make(chan context.Context, 1)
	release := make(chan struct{})
	releaseStart := sync.OnceFunc(func() { close(release) })
	r := &fakeRecording{
		start: func(ctx context.Context) (string, error) {
			started <- ctx
			select {
			case <-release:
				return "gravacao.wav", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		},
	}
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.newRecorder = func(int, string) recording { return r }
	})
	t.Cleanup(releaseStart)
	test.Tap(h.d.recordButton)
	capture := waitValue(t, started)
	h.d.editor.SetText("Posso escrever enquanto o microfone abre.")
	test.Tap(h.d.clearButton)
	if capture.Err() != nil || h.d.editor.Text != "" || h.d.capture != captureStarting {
		t.Fatal("limpar cancelou a abertura do microfone")
	}
	releaseStart()
	h.await(func() bool { return h.d.capture == captureRunning })
	if h.d.recordButton.Text != "■  Parar e transcrever" || h.d.pauseButton.Disabled() {
		t.Fatal("a atualização do gravador foi descartada junto com o texto")
	}
}

func TestRecordingNewAudioCancelsPendingStartAndRejectsItsCallback(t *testing.T) {
	started := make(chan context.Context, 1)
	r := &fakeRecording{
		start: func(ctx context.Context) (string, error) {
			started <- ctx
			<-ctx.Done()
			return "preservado.wav", ctx.Err()
		},
		stop: func() (string, error) { return "", recorder.ErrNotRecording },
	}
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.newRecorder = func(int, string) recording { return r }
	})
	test.Tap(h.d.recordButton)
	capture := waitValue(t, started)
	test.Tap(h.d.newButton)
	h.await(func() bool { return h.d.capture == captureIdle })
	h.settle()
	if capture.Err() == nil || h.d.recorder != nil || h.d.lastAudio != "" || h.d.errorLabel.Visible() {
		t.Fatal("a abertura antiga reativou a gravação após novo áudio")
	}
	if h.d.recordButton.Disabled() || h.d.settingsButton.Disabled() {
		t.Fatal("a abertura cancelada deixou a interface travada")
	}
}

func TestRecordingStartFailureRecoversControls(t *testing.T) {
	r := &fakeRecording{
		start: func(context.Context) (string, error) { return "", errors.New("microfone indisponível") },
	}
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.newRecorder = func(int, string) recording { return r }
	})
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureIdle })
	if !h.d.errorLabel.Visible() || h.d.recordButton.Disabled() || h.d.settingsButton.Disabled() {
		t.Fatal("a falha de abertura bloqueou a recuperação")
	}
	if len(h.d.window.Canvas().Overlays().List()) != 0 {
		t.Fatal("a falha de gravação deve aparecer sem diálogo modal")
	}
}

func TestRecordingCaptureFailureFinalizesAndAllowsRetry(t *testing.T) {
	r := &fakeRecording{state: recorder.Snapshot{Path: "preservado.wav", Duration: 12}}
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.newRecorder = func(int, string) recording { return r }
	})
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	r.update(func(snapshot *recorder.Snapshot) { snapshot.Error = "a captura falhou" })
	h.d.tick()
	h.await(func() bool { return h.d.capture == captureIdle })
	if r.Snapshot().Active || h.d.lastAudio != "preservado.wav" || !h.d.errorLabel.Visible() {
		t.Fatal("a falha não finalizou e preservou a captura")
	}
	if !h.d.retryButton.Visible() || h.d.retryButton.Disabled() || h.d.clearButton.Disabled() {
		t.Fatal("a falha não permite transcrever o áudio já salvo")
	}
}

func TestRecordingCallbackFailureDoesNotStopTicks(t *testing.T) {
	r := &fakeRecording{state: recorder.Snapshot{Path: "gravacao.wav", Duration: 2}}
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.newRecorder = func(int, string) recording { return r }
		deps.tickInterval = 5 * time.Millisecond
	})
	h.d.start("")
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	h.d.guard(func() { panic("drawing failed") })()
	if !h.d.errorLabel.Visible() || h.d.clearButton.Disabled() {
		t.Fatal("a falha de callback não foi recuperada na interface")
	}
	r.update(func(snapshot *recorder.Snapshot) { snapshot.Duration = 37 })
	h.await(func() bool { return h.d.timer.Text == "00:37" })
	test.Tap(h.d.pauseButton)
	if !r.Snapshot().Paused {
		t.Fatal("a falha de callback parou o loop ou os controles")
	}
	h.d.finish()
}
