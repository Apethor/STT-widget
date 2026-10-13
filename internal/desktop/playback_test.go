package desktop

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"ai-talker/internal/config"
	"ai-talker/internal/playback"
	"ai-talker/internal/recorder"
	"ai-talker/internal/transcript"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type playbackCall struct {
	ctx    context.Context
	source string
}

type fakeAudioPlayer struct {
	mu       sync.Mutex
	state    playback.Snapshot
	volume   int
	speed    float64
	started  chan playbackCall
	finish   chan error
	done     chan struct{}
	teardown <-chan struct{}
}

func newFakeAudioPlayer() *fakeAudioPlayer {
	return &fakeAudioPlayer{
		started: make(chan playbackCall, 1), finish: make(chan error, 1),
		done: make(chan struct{}),
	}
}

func (p *fakeAudioPlayer) Run(ctx context.Context, source string) error {
	p.mu.Lock()
	p.state = playback.Snapshot{Ready: true, Active: true, Duration: 125}
	p.mu.Unlock()
	p.started <- playbackCall{ctx, source}
	var err error
	select {
	case <-ctx.Done():
		err = ctx.Err()
	case err = <-p.finish:
	}
	if p.teardown != nil {
		<-p.teardown
	}
	p.mu.Lock()
	p.state.Active = false
	p.mu.Unlock()
	close(p.done)
	return err
}

func (p *fakeAudioPlayer) Snapshot() playback.Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
}

func (p *fakeAudioPlayer) SetPaused(paused bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.state.Paused = paused
}

func (p *fakeAudioPlayer) SetVolume(volume int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.volume = volume
}

func (p *fakeAudioPlayer) SetSpeed(speed float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.speed = speed
}

func (p *fakeAudioPlayer) outputVolume() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.volume
}

func (p *fakeAudioPlayer) playbackSpeed() float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.speed
}

func (p *fakeAudioPlayer) position(seconds float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.state.Position = seconds
}

func loadedPlaybackHarness(t *testing.T, player *fakeAudioPlayer) *testHarness {
	t.Helper()
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.newPlayer = func(string) audioPlayer { return player }
	})
	h.d.lastAudio = "carregado.wav"
	h.d.editor.SetText("Texto que já está na aba.")
	h.d.view.timer = "02:05"
	h.d.renderDocument(h.d.document)
	h.d.syncControls()
	return h
}

func TestPlaybackButtonPlaysPausesResumesAndRestoresDuration(t *testing.T) {
	player := newFakeAudioPlayer()
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.newPlayer = func(string) audioPlayer { return player }
	})
	if h.d.pauseButton.Visible() || !h.d.pauseButton.Disabled() || h.d.pauseButton.Icon != theme.MediaPlayIcon() {
		t.Fatal("sem áudio carregado, Play deve permanecer oculto")
	}
	h.d.startImportedJob(h.d.document, "importado.wav")
	h.await(func() bool { return !h.d.busy })
	text, timer := h.d.editor.Text, h.d.timer.Text
	test.Tap(h.d.pauseButton)
	call := waitValue(t, player.started)
	h.d.tick()
	if call.source != "importado.wav" || h.d.pauseButton.Icon != theme.MediaPauseIcon() ||
		h.d.pauseButton.Disabled() || h.d.editor.Text != text || h.d.busy {
		t.Fatal("ouvir o áudio alterou o texto ou iniciou outra transcrição")
	}
	player.position(35)
	h.d.tick()
	if h.d.timer.Text != "00:35" {
		t.Fatal("o contador não acompanha a posição da reprodução")
	}
	test.Tap(h.d.pauseButton)
	if !player.Snapshot().Paused || h.d.pauseButton.Icon != theme.MediaPlayIcon() {
		t.Fatal("pausar o áudio não ofereceu Play para retomar")
	}
	test.Tap(h.d.pauseButton)
	if player.Snapshot().Paused || h.d.pauseButton.Icon != theme.MediaPauseIcon() {
		t.Fatal("Play não retomou o áudio pausado")
	}
	player.finish <- nil
	h.await(func() bool { return h.d.playback == nil })
	if h.d.timer.Text != timer || h.d.pauseButton.Disabled() ||
		h.d.pauseButton.Icon != theme.MediaPlayIcon() || h.d.editor.Text != text {
		t.Fatal("terminar a reprodução não restaurou a duração e o botão Play")
	}
}

func TestPlaybackAfterRecordingKeepsCapturePauseAndAllowsReplay(t *testing.T) {
	players := []*fakeAudioPlayer{newFakeAudioPlayer(), newFakeAudioPlayer()}
	index := 0
	r := &fakeRecording{state: recorder.Snapshot{Path: "gravado.wav", Duration: 4}}
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.newRecorder = func(int, string) recording { return r }
		deps.newPlayer = func(string) audioPlayer {
			player := players[index]
			index++
			return player
		}
	})
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	test.Tap(h.d.pauseButton)
	if !r.Snapshot().Paused || index != 0 {
		t.Fatal("Pausa durante a gravação iniciou reprodução")
	}
	test.Tap(h.d.pauseButton)
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureIdle && !h.d.busy })
	for _, player := range players {
		test.Tap(h.d.pauseButton)
		if call := waitValue(t, player.started); call.source != "gravado.wav" {
			t.Fatal("Play não usou o áudio recém-gravado")
		}
		player.finish <- nil
		h.await(func() bool { return h.d.playback == nil })
	}
	if starts, stops := r.counts(); starts != 1 || stops != 1 {
		t.Fatal("ouvir novamente acionou o gravador:", starts, stops)
	}
}

func TestPlaybackIsIndependentOfPendingTranscriptionAndClear(t *testing.T) {
	requests := make(chan context.Context, 1)
	player := newFakeAudioPlayer()
	h := newHarness(t, runnerFunc(func(ctx context.Context, _ string, _ config.Settings, _ transcript.Request) (
		transcript.Result, error,
	) {
		requests <- ctx
		<-ctx.Done()
		return transcript.Result{}, ctx.Err()
	}), func(deps *dependencies) {
		deps.newPlayer = func(string) audioPlayer { return player }
	})
	h.d.startImportedJob(h.d.document, "longo.wav")
	transcription := waitValue(t, requests)
	test.Tap(h.d.pauseButton)
	listening := waitValue(t, player.started)
	h.d.cancelJob()
	if transcription.Err() == nil || listening.ctx.Err() != nil || h.d.playback == nil {
		t.Fatal("cancelar a transcrição interrompeu a reprodução")
	}
	h.d.clearText(false)
	if listening.ctx.Err() != nil || h.d.lastAudio != "longo.wav" {
		t.Fatal("limpar o texto interrompeu o áudio carregado")
	}
	h.d.stopPlayback()
	h.settle()
}

func TestPlaybackStopsWhenReplacingOrLeavingItsAudio(t *testing.T) {
	for _, action := range []string{"new", "import", "tab", "close-tab"} {
		t.Run(action, func(t *testing.T) {
			player := newFakeAudioPlayer()
			h := loadedPlaybackHarness(t, player)
			first := h.d.document
			test.Tap(h.d.pauseButton)
			call := waitValue(t, player.started)
			player.position(35)
			h.d.tick()
			switch action {
			case "new":
				h.d.newAudio()
			case "import":
				h.d.startImportedJob(first, "outro.wav")
			case "tab":
				h.d.addDocument()
			case "close-tab":
				h.d.closeDocument(first.item)
			}
			if call.ctx.Err() == nil || h.d.playback != nil {
				t.Fatal("o áudio anterior continuou tocando:", action)
			}
			h.settle()
			if h.d.errorLabel.Visible() {
				t.Fatal("o cancelamento normal da reprodução mostrou um erro")
			}
			if action == "tab" {
				h.d.tabs.Select(first.item)
				if h.d.timer.Text != "02:05" {
					t.Fatal("trocar de aba persistiu a posição passageira da reprodução")
				}
			}
		})
	}
}

func TestPlaybackFailurePreservesAudioAndCanBeRetried(t *testing.T) {
	players := []*fakeAudioPlayer{newFakeAudioPlayer(), newFakeAudioPlayer()}
	index := 0
	h := loadedPlaybackHarness(t, players[0])
	h.d.deps.newPlayer = func(string) audioPlayer {
		player := players[index]
		index++
		return player
	}
	test.Tap(h.d.pauseButton)
	waitValue(t, players[0].started)
	players[0].finish <- errors.New("saída indisponível")
	h.await(func() bool { return h.d.playback == nil })
	if !h.d.errorLabel.Visible() || !strings.Contains(h.d.errorLabel.Text, "saída indisponível") ||
		h.d.lastAudio != "carregado.wav" || h.d.editor.Text != "Texto que já está na aba." ||
		h.d.pauseButton.Disabled() {
		t.Fatal("a falha de reprodução perdeu o áudio, o texto ou bloqueou a recuperação")
	}
	test.Tap(h.d.pauseButton)
	waitValue(t, players[1].started)
	players[1].finish <- nil
	h.await(func() bool { return h.d.playback == nil })
}

func TestPlaybackTeardownPrecedesNewRecording(t *testing.T) {
	player := newFakeAudioPlayer()
	release := make(chan struct{})
	releasePlayer := sync.OnceFunc(func() { close(release) })
	player.teardown = release
	started := make(chan context.Context, 1)
	r := &fakeRecording{start: func(ctx context.Context) (string, error) {
		started <- ctx
		return filepath.Join(t.TempDir(), "nova.wav"), nil
	}}
	h := loadedPlaybackHarness(t, player)
	h.d.deps.newRecorder = func(int, string) recording { return r }
	t.Cleanup(releasePlayer)
	test.Tap(h.d.pauseButton)
	listening := waitValue(t, player.started)
	test.Tap(h.d.recordButton)
	if listening.ctx.Err() == nil || h.d.capture != captureStarting {
		t.Fatal("nova gravação não cancelou o som anterior")
	}
	select {
	case <-started:
		t.Fatal("o microfone abriu antes de o player liberar a saída")
	default:
	}
	releasePlayer()
	waitValue(t, started)
	h.await(func() bool { return h.d.capture == captureRunning })
	if h.d.playback != nil || h.d.pauseButton.Icon != theme.MediaPauseIcon() {
		t.Fatal("a conclusão antiga do player alterou a nova gravação")
	}
}

func TestPlaybackStopsImmediatelyWhenClosingDuringTranscription(t *testing.T) {
	requests := make(chan context.Context, 1)
	player := newFakeAudioPlayer()
	h := newHarness(t, runnerFunc(func(ctx context.Context, _ string, _ config.Settings, _ transcript.Request) (
		transcript.Result, error,
	) {
		requests <- ctx
		<-ctx.Done()
		return transcript.Result{}, ctx.Err()
	}), func(deps *dependencies) {
		deps.newPlayer = func(string) audioPlayer { return player }
	})
	h.d.startImportedJob(h.d.document, "longo.wav")
	waitValue(t, requests)
	test.Tap(h.d.pauseButton)
	call := waitValue(t, player.started)
	h.d.requestClose()
	if call.ctx.Err() == nil || h.d.playback != nil || !h.d.hidden || !h.d.closeAfterJob {
		t.Fatal("fechar a janela deixou o som tocando enquanto esperava a transcrição")
	}
	h.d.invalidateJob()
	h.settle()
}

func TestPlaybackStopReturnsNextPlayToBeginningAndIgnoresOldCompletion(t *testing.T) {
	players := []*fakeAudioPlayer{newFakeAudioPlayer(), newFakeAudioPlayer()}
	release := make(chan struct{})
	releaseOld := sync.OnceFunc(func() { close(release) })
	players[0].teardown = release
	t.Cleanup(releaseOld)
	h := loadedPlaybackHarness(t, players[0])
	index := 0
	h.d.deps.newPlayer = func(string) audioPlayer {
		player := players[index]
		index++
		return player
	}
	if h.d.stopPlaybackButton.Visible() {
		t.Fatal("Stop apareceu sem reprodução em andamento")
	}
	test.Tap(h.d.pauseButton)
	first := waitValue(t, players[0].started)
	players[0].position(35)
	h.d.tick()
	test.Tap(h.d.pauseButton)
	if !players[0].Snapshot().Paused || !h.d.stopPlaybackButton.Visible() {
		t.Fatal("pausar não manteve Stop disponível")
	}
	test.Tap(h.d.stopPlaybackButton)
	if first.ctx.Err() == nil || h.d.playback != nil || h.d.stopPlaybackButton.Visible() ||
		h.d.pauseButton.Icon != theme.MediaPlayIcon() || h.d.timer.Text != "02:05" {
		t.Fatal("Stop não cancelou a reprodução e restaurou Play")
	}
	test.Tap(h.d.pauseButton)
	releaseOld()
	second := waitValue(t, players[1].started)
	h.d.tick()
	if second.source != first.source || players[1].Snapshot().Position != 0 ||
		h.d.playback == nil || h.d.playback.player != players[1] || !h.d.stopPlaybackButton.Visible() {
		t.Fatal("Play após Stop não começou do início ou perdeu seu estado para a reprodução antiga")
	}
	players[1].finish <- nil
	h.await(func() bool { return h.d.playback == nil })
	if h.d.editor.Text != "Texto que já está na aba." || h.d.lastAudio != "carregado.wav" {
		t.Fatal("Stop alterou a transcrição ou descartou o áudio")
	}
}

func TestPlaybackSpeedRemainsWithAudioAcrossStopAndResetsForNewAudio(t *testing.T) {
	players := []*fakeAudioPlayer{newFakeAudioPlayer(), newFakeAudioPlayer(), newFakeAudioPlayer()}
	index := 0
	h := loadedPlaybackHarness(t, players[0])
	h.d.deps.newPlayer = func(string) audioPlayer {
		player := players[index]
		index++
		return player
	}
	if !h.d.playbackSpeedButton.Visible() || h.d.playbackSpeedButton.Text != "1×" {
		t.Fatal("a velocidade padrão de 1× não ficou junto dos controles do áudio")
	}
	h.d.setPlaybackSpeed(0.75)
	if h.d.playbackSpeedButton.Text != "0,75×" {
		t.Fatal("a velocidade escolhida não apareceu no controle")
	}
	test.Tap(h.d.pauseButton)
	waitValue(t, players[0].started)
	if players[0].playbackSpeed() != 0.75 {
		t.Fatal("o player não recebeu a velocidade da gravação atual")
	}
	test.Tap(h.d.stopPlaybackButton)
	h.settle()
	test.Tap(h.d.pauseButton)
	waitValue(t, players[1].started)
	if players[1].playbackSpeed() != 0.75 {
		t.Fatal("Stop perdeu a velocidade escolhida para o mesmo áudio")
	}
	h.d.stopPlaybackAction()
	h.d.lastAudio = "outro.wav"
	h.d.syncPlaybackControls()
	if h.d.playbackSpeedButton.Text != "1×" {
		t.Fatal("um áudio novo herdou a velocidade escolhida para outro arquivo")
	}
	test.Tap(h.d.pauseButton)
	waitValue(t, players[2].started)
	if players[2].playbackSpeed() != 1 {
		t.Fatal("um áudio novo não começou em velocidade normal")
	}
	h.d.stopPlaybackAction()
	h.settle()
}

func TestPlaybackMenuExposesBothSlowRates(t *testing.T) {
	player := newFakeAudioPlayer()
	h := loadedPlaybackHarness(t, player)
	for index, speed := range []float64{0.5, 0.75} {
		h.d.openPlaybackSpeed()
		menu, ok := h.d.window.Canvas().Focused().(*widget.PopUpMenu)
		if !ok || len(menu.Items) != 8 {
			t.Fatal("o menu não mostrou as oito velocidades")
		}
		test.Tap(menu.Items[index].(fyne.Tappable))
		if h.d.playbackSpeed(h.d.lastAudio) != speed || h.d.playbackSpeedButton.Text != playbackSpeedLabel(speed) {
			t.Fatal("a velocidade lenta selecionada no menu não foi aplicada")
		}
	}
}
