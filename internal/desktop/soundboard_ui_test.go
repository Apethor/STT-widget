package desktop

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"ai-talker/internal/playback"
	"ai-talker/internal/soundboard"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

type soundPlaybackState struct {
	mu      sync.Mutex
	active  int
	maximum int
	started chan string
	stopped chan int
}

type fakeSoundEffectPlayer struct{ state *soundPlaybackState }

func (p *fakeSoundEffectPlayer) Run(ctx context.Context, source string) error {
	p.state.mu.Lock()
	p.state.active++
	p.state.maximum = max(p.state.maximum, p.state.active)
	active := p.state.active
	p.state.mu.Unlock()
	p.state.started <- source
	<-ctx.Done()
	p.state.mu.Lock()
	p.state.active--
	p.state.mu.Unlock()
	p.state.stopped <- active
	return ctx.Err()
}

func (*fakeSoundEffectPlayer) SetPaused(bool)              {}
func (*fakeSoundEffectPlayer) SetVolume(int)               {}
func (*fakeSoundEffectPlayer) SetSpeed(float64)            {}
func (*fakeSoundEffectPlayer) Snapshot() playback.Snapshot { return playback.Snapshot{} }

func TestSoundboardWindowLoadsDefaultsAndPersistsItsIndependentVolume(t *testing.T) {
	h := newHarness(t, nil, nil)
	test.Tap(h.d.soundboardButton)
	h.await(func() bool { return h.d.soundboardWindow != nil && h.d.soundboard != nil })
	buttons := h.d.soundboard.Buttons()
	if len(buttons) != 6 || buttons[0].Name != "Aplausos" || buttons[1].Name != "Risadas" ||
		h.d.soundboardWindow.Content() == nil || h.d.soundboardVolume == nil {
		t.Fatalf("a botoneira não mostrou seus sons padrão: %+v", buttons)
	}
	h.d.soundboardVolume.SetValue(42)
	h.d.soundboardVolume.OnChangeEnded(42)
	h.await(func() bool { return h.d.soundboardVolume.Value == 42 && !h.d.soundboardVolume.Disabled() })
	if h.d.soundboard.Volume() != 42 {
		t.Fatal("o volume próprio da botoneira não foi persistido")
	}
	if !soundboardLabelExists(h.d.soundboardWindow.Content(), "42%") {
		t.Fatal("o percentual da botoneira ficou diferente do volume aplicado")
	}
}

func TestSoundboardUsesResponsiveMatrixAndIntegratedWindowControls(t *testing.T) {
	h := newHarness(t, nil, nil)
	test.Tap(h.d.soundboardButton)
	h.await(func() bool { return h.d.soundboardWindow != nil })
	window := h.d.soundboardWindow
	// Three 180 px cells plus gaps require 556 useful pixels. The headless
	// window adds canvas padding to the panel's insets, so 580 is insufficient.
	window.Resize(fyne.NewSize(640, 420))
	window.Canvas().Refresh(window.Content())
	if window.Title() != "Painel de sons" {
		t.Fatal("soundboard title was not updated:", window.Title())
	}
	if len(h.d.soundboard.Buttons()) != 6 {
		t.Fatal("soundboard did not retain all six default effects")
	}
	find := func(name string) *widget.Button {
		t.Helper()
		var result *widget.Button
		var visit func(fyne.CanvasObject)
		visit = func(object fyne.CanvasObject) {
			if button, ok := object.(*widget.Button); ok && button.Text == name {
				result = button
			}
			if content, ok := object.(*fyne.Container); ok {
				for _, child := range content.Objects {
					visit(child)
				}
			}
			if scroll, ok := object.(*container.Scroll); ok {
				visit(scroll.Content)
			}
			if themed, ok := object.(*container.ThemeOverride); ok {
				visit(themed.Content)
			}
		}
		visit(window.Content())
		if result == nil {
			t.Fatal("missing sound button:", name)
		}
		return result
	}
	position := h.d.application.Driver().AbsolutePositionForObject
	applause := position(find("Aplausos"))
	laughter := position(find("Risadas"))
	pipe := position(find("Impulso"))
	star := position(find("Brilho"))
	rimshot := position(find("Ba dum tss"))
	wow := position(find("Uau!"))
	if applause.Y != laughter.Y || laughter.Y != pipe.Y ||
		applause.X >= laughter.X || laughter.X >= pipe.X || star.Y <= applause.Y ||
		star.Y != rimshot.Y || rimshot.Y != wow.Y ||
		star.X != applause.X || rimshot.X != laughter.X || wow.X != pipe.X {
		t.Fatalf("six effects were not displayed as a three-column matrix: %+v %+v %+v %+v %+v %+v",
			applause, laughter, pipe, star, rimshot, wow)
	}
	t.Logf("wide canvas=%+v: effects %+v %+v %+v / %+v %+v %+v",
		window.Canvas().Size(), applause, laughter, pipe, star, rimshot, wow)
	window.Resize(fyne.NewSize(360, 600))
	window.Canvas().Refresh(window.Content())
	applause = position(find("Aplausos"))
	previous := applause
	for _, name := range []string{"Risadas", "Impulso", "Brilho", "Ba dum tss", "Uau!"} {
		current := position(find(name))
		if current.X != applause.X || current.Y <= previous.Y {
			t.Fatalf("narrowing the window did not display one column: %s at %+v after %+v", name, current, previous)
		}
		previous = current
	}
	t.Logf("narrow canvas=%+v: first effect %+v, last effect %+v", window.Canvas().Size(), applause, previous)
	var controls []string
	var visit func(fyne.CanvasObject)
	visit = func(object fyne.CanvasObject) {
		if button, ok := object.(*actionButton); ok {
			controls = append(controls, button.AccessibilityLabel())
		}
		if content, ok := object.(*fyne.Container); ok {
			for _, child := range content.Objects {
				visit(child)
			}
		}
		if scroll, ok := object.(*container.Scroll); ok {
			visit(scroll.Content)
		}
		if themed, ok := object.(*container.ThemeOverride); ok {
			visit(themed.Content)
		}
	}
	visit(window.Content())
	for _, expected := range []string{"Minimizar janela", "Maximizar ou restaurar janela", "Fechar painel de sons"} {
		found := false
		for _, actual := range controls {
			if actual == expected {
				found = true
			}
		}
		if !found {
			t.Fatal("missing integrated window control:", expected, controls)
		}
	}
}

func soundboardLabelExists(object fyne.CanvasObject, expected string) bool {
	if label, ok := object.(*widget.Label); ok && label.Text == expected {
		return true
	}
	if content, ok := object.(*fyne.Container); ok {
		for _, child := range content.Objects {
			if soundboardLabelExists(child, expected) {
				return true
			}
		}
	}
	return false
}

func TestSoundboardSerializesEffectsAndCancelsThePreviousSound(t *testing.T) {
	state := &soundPlaybackState{started: make(chan string, 2), stopped: make(chan int, 2)}
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.newPlayer = func(string) audioPlayer { return &fakeSoundEffectPlayer{state: state} }
	})
	board, err := soundboard.Load(h.d.directory)
	if err != nil {
		t.Fatal(err)
	}
	h.d.soundboard = board
	buttons := board.Buttons()
	h.d.playSoundEffect(buttons[0])
	first := waitValue(t, state.started)
	h.d.playSoundEffect(buttons[1])
	second := waitValue(t, state.started)
	if first == second {
		t.Fatal("dois botões diferentes tocaram o mesmo arquivo")
	}
	stoppedActive := waitValue(t, state.stopped)
	state.mu.Lock()
	active, maximum := state.active, state.maximum
	state.mu.Unlock()
	if stoppedActive != 1 || active != 1 || maximum != 1 {
		t.Fatalf("efeitos se sobrepuseram durante a troca: ativo=%d máximo=%d", active, maximum)
	}
	h.d.stopSoundboardPlayback()
	waitValue(t, state.stopped)
	state.mu.Lock()
	active = state.active
	state.mu.Unlock()
	if active != 0 {
		t.Fatalf("Parar deixou %d efeito(s) ativo(s)", active)
	}
}

func TestSoundboardVolumeSaveFailureRestoresSliderAndDisplayedPercent(t *testing.T) {
	h := newHarness(t, nil, nil)
	test.Tap(h.d.soundboardButton)
	h.await(func() bool { return h.d.soundboardWindow != nil && h.d.soundboardVolume != nil })
	manifest := filepath.Join(h.d.directory, "soundboard", "soundboard.json")
	if err := os.Remove(manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(manifest, 0o700); err != nil {
		t.Fatal(err)
	}
	h.d.soundboardVolume.SetValue(31)
	h.d.soundboardVolume.OnChangeEnded(31)
	h.await(func() bool { return !h.d.soundboardVolume.Disabled() })
	if h.d.soundboardVolume.Value != 100 || h.d.soundboard.Volume() != 100 ||
		!soundboardLabelExists(h.d.soundboardWindow.Content(), "100%") {
		t.Fatal("uma falha de persistência deixou o controle indicando um volume rejeitado")
	}
}

func TestSoundboardCanPlayWhileMainAudioIsPausedAndDoesNotBlockCapture(t *testing.T) {
	main := newFakeAudioPlayer()
	state := &soundPlaybackState{started: make(chan string, 2), stopped: make(chan int, 2)}
	h := loadedPlaybackHarness(t, main)
	first := true
	h.d.deps.newPlayer = func(string) audioPlayer {
		if first {
			first = false
			return main
		}
		return &fakeSoundEffectPlayer{state: state}
	}
	board, err := soundboard.Load(h.d.directory)
	if err != nil {
		t.Fatal(err)
	}
	h.d.soundboard = board
	h.d.playbackAction()
	waitValue(t, main.started)
	main.SetPaused(true)
	h.d.playSoundEffect(board.Buttons()[0])
	waitValue(t, state.started)
	if h.d.playback == nil || !main.Snapshot().Paused || h.d.soundPlayback == nil {
		t.Fatal("tocar um efeito interrompeu o player principal ou ficou esperando sua pausa terminar")
	}
	h.d.stopPlaybackAction()
	waitValue(t, main.done)
	h.drain()
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	state.mu.Lock()
	active := state.active
	state.mu.Unlock()
	if active != 1 {
		t.Fatal("iniciar a gravação interrompeu o efeito que deve chegar pelo loopback")
	}
	h.d.stopSoundboardPlayback()
	waitValue(t, state.stopped)
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureIdle && !h.d.busy })
}
