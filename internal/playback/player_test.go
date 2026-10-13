package playback

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"ai-talker/internal/audio"
)

type fakeOutput struct {
	mu                                     sync.Mutex
	position, duration                     float64
	playing                                bool
	plays, pauses, closes                  int
	volumes                                []int
	seeks                                  []float64
	playErr, pauseErr, closeErr, volumeErr error
}

func (d *fakeOutput) Play() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.plays++
	d.playing = true
	return d.playErr
}

func (d *fakeOutput) Pause() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pauses++
	d.playing = false
	return d.pauseErr
}

func (d *fakeOutput) Seek(seconds float64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.position, d.playing = seconds, false
	d.seeks = append(d.seeks, seconds)
	return nil
}

func (d *fakeOutput) Status() (float64, float64, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.position, d.duration, d.playing, nil
}

func (d *fakeOutput) SetVolume(volume int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.volumes = append(d.volumes, volume)
	return d.volumeErr
}

func (d *fakeOutput) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closes++
	d.playing = false
	return d.closeErr
}

func (d *fakeOutput) update(action func(*fakeOutput)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	action(d)
}

func silenceFixture(t *testing.T, path string, seconds int) []byte {
	t.Helper()
	wav, err := audio.EncodeWAV(t.Context(), make([]byte, seconds*audio.SampleRate*audio.SampleBytes))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, wav, 0o600); err != nil {
		t.Fatal(err)
	}
	return wav
}

func awaitPlayback(t *testing.T, predicate func() bool) {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for !predicate() {
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("a reprodução não chegou ao estado esperado")
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		}
	}
}

func playbackResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("a reprodução não liberou seus recursos")
		return nil
	}
}

func TestPlayerPauseResumeCompletesAndRemovesOnlyPrivateCopy(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "original com acento áudio.wav")
	before := silenceFixture(t, source, 6)
	player := New(directory)
	device := &fakeOutput{duration: 6}
	opened := make(chan string, 1)
	player.open = func(path string) (output, error) {
		opened <- path
		return device, nil
	}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	result := make(chan error, 1)
	go func() { result <- player.Run(ctx, source) }()
	awaitPlayback(t, func() bool { return player.Snapshot().Ready })
	private := <-opened
	copy, err := os.ReadFile(private)
	if err != nil || private == source || !bytes.Equal(copy, before) {
		t.Fatal("a reprodução não isolou seu arquivo da gravação original")
	}
	device.update(func(d *fakeOutput) { d.position = 1.5 })
	awaitPlayback(t, func() bool { return player.Snapshot().Position == 1.5 })
	player.SetPaused(true)
	awaitPlayback(t, func() bool {
		device.mu.Lock()
		defer device.mu.Unlock()
		return device.pauses == 1 && !device.playing
	})
	if !player.Snapshot().Paused || player.Snapshot().Position != 1.5 {
		t.Fatal("pausar perdeu a posição ou não atualizou o estado")
	}
	player.SetPaused(false)
	awaitPlayback(t, func() bool {
		device.mu.Lock()
		defer device.mu.Unlock()
		return device.plays == 2 && device.playing
	})
	device.update(func(d *fakeOutput) { d.position, d.playing = 6, false })
	if err := playbackResult(t, result); err != nil {
		t.Fatal(err)
	}
	if player.Snapshot().Active || device.closes != 1 {
		t.Fatal("terminar manteve a saída de áudio aberta")
	}
	if _, err := os.Stat(private); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("o áudio temporário permaneceu após concluir")
	}
	after, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(after, before) {
		t.Fatal("ouvir alterou ou removeu a gravação original")
	}
}

func TestPlayerSeekMovesInBothDirectionsAndKeepsPauseWithoutChangingSource(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "original.wav")
	before := silenceFixture(t, source, 10)
	player := New(directory)
	device := &fakeOutput{duration: 10}
	player.open = func(string) (output, error) { return device, nil }
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- player.Run(ctx, source) }()
	awaitPlayback(t, func() bool { return player.Snapshot().Ready })
	player.Seek(7)
	awaitPlayback(t, func() bool { return player.Snapshot().Position == 7 })
	player.SetPaused(true)
	awaitPlayback(t, func() bool {
		device.mu.Lock()
		defer device.mu.Unlock()
		return device.pauses == 1
	})
	player.Seek(2)
	awaitPlayback(t, func() bool { return player.Snapshot().Position == 2 })
	device.mu.Lock()
	playing, seeks := device.playing, append([]float64(nil), device.seeks...)
	device.mu.Unlock()
	if playing || !player.Snapshot().Paused || len(seeks) != 2 || seeks[0] != 7 || seeks[1] != 2 {
		t.Fatal("mudar a posição perdeu a pausa ou inverteu as coordenadas", seeks)
	}
	player.SetPaused(false)
	awaitPlayback(t, func() bool {
		device.mu.Lock()
		defer device.mu.Unlock()
		return device.playing
	})
	cancel()
	if err := playbackResult(t, done); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	after, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("selecionar a posição modificou o áudio original")
	}
}

func TestPlayerCancellationWhilePausedReleasesDeviceAndTemporaryAudio(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "original.wav")
	silenceFixture(t, source, 6)
	player := New(directory)
	device := &fakeOutput{duration: 6}
	var private string
	player.open = func(path string) (output, error) { private = path; return device, nil }
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	result := make(chan error, 1)
	go func() { result <- player.Run(ctx, source) }()
	awaitPlayback(t, func() bool { return player.Snapshot().Ready })
	player.SetPaused(true)
	awaitPlayback(t, func() bool {
		device.mu.Lock()
		defer device.mu.Unlock()
		return device.pauses == 1
	})
	cancel()
	if err := playbackResult(t, result); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelar uma reprodução pausada não encerrou o fluxo:", err)
	}
	if _, err := os.Stat(private); !errors.Is(err, os.ErrNotExist) || device.closes != 1 {
		t.Fatal("cancelar deixou a saída ou o WAV temporário aberto")
	}
}

func TestPlayerOutputFailuresKeepOriginalAndRemoveTemporaryAudio(t *testing.T) {
	for _, stage := range []string{"open", "volume", "play", "pause", "close"} {
		t.Run(stage, func(t *testing.T) {
			directory := t.TempDir()
			source := filepath.Join(directory, "original.wav")
			before := silenceFixture(t, source, 1)
			player := New(directory)
			failure := errors.New("saída indisponível")
			device := &fakeOutput{duration: 1}
			switch stage {
			case "volume":
				device.volumeErr = failure
			case "play":
				device.playErr = failure
			case "pause":
				device.pauseErr = failure
			case "close":
				device.closeErr = failure
			}
			player.open = func(string) (output, error) {
				if stage == "open" {
					return nil, failure
				}
				return device, nil
			}
			ctx, cancel := context.WithCancel(t.Context())
			t.Cleanup(cancel)
			result := make(chan error, 1)
			go func() { result <- player.Run(ctx, source) }()
			if stage == "pause" || stage == "close" {
				awaitPlayback(t, func() bool { return player.Snapshot().Ready })
				if stage == "pause" {
					player.SetPaused(true)
				} else {
					device.update(func(d *fakeOutput) { d.position, d.playing = 1, false })
				}
			}
			if err := playbackResult(t, result); !errors.Is(err, failure) {
				t.Fatal("a falha da saída não foi propagada:", err)
			}
			files, err := os.ReadDir(filepath.Join(directory, "playback"))
			if err != nil || len(files) != 0 {
				t.Fatal("a falha deixou o áudio temporário no disco")
			}
			after, err := os.ReadFile(source)
			if err != nil || !bytes.Equal(after, before) || player.Snapshot().Active {
				t.Fatal("a falha alterou o original ou manteve a reprodução ativa")
			}
		})
	}
}

func TestPlayerAppliesOutputVolumeBeforePlayAndWhilePaused(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "original.wav")
	before := silenceFixture(t, source, 6)
	player := New(directory)
	device := &fakeOutput{duration: 6}
	prepared := make(chan renderOptions, 3)
	player.prepare = func(_ context.Context, _, _ string, options renderOptions) (preparedAudio, error) {
		prepared <- options
		return preparedAudio{path: filepath.Join(directory, "prepared.wav")}, nil
	}
	player.open = func(string) (output, error) { return device, nil }
	player.SetVolume(38)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	result := make(chan error, 1)
	go func() { result <- player.Run(ctx, source) }()
	if options := <-prepared; options.Speed != 1 || options.Gain != 0.38 {
		t.Fatal("a saída começou com velocidade ou ganho diferentes do pedido")
	}
	awaitPlayback(t, func() bool { return player.Snapshot().Ready })
	device.mu.Lock()
	if len(device.volumes) != 1 || device.volumes[0] != 100 || device.plays != 1 {
		t.Error("o volume salvo não foi aplicado antes de começar a reprodução")
	}
	device.mu.Unlock()

	player.SetVolume(-5)
	if options := <-prepared; options.Speed != 1 || options.Gain != 0 {
		t.Fatal("silenciar não foi aplicado à cópia privada:", options)
	}
	awaitPlayback(t, func() bool {
		device.mu.Lock()
		defer device.mu.Unlock()
		return len(device.volumes) == 2 && device.volumes[1] == 100
	})
	player.SetPaused(true)
	awaitPlayback(t, func() bool {
		device.mu.Lock()
		defer device.mu.Unlock()
		return device.pauses == 1
	})
	player.SetVolume(140)
	if options := <-prepared; options.Speed != 1 || options.Gain != 1.4 {
		t.Fatal("o ganho acima de 100% não foi preparado para limitação suave:", options)
	}
	awaitPlayback(t, func() bool {
		device.mu.Lock()
		defer device.mu.Unlock()
		return len(device.volumes) == 3 && device.volumes[2] == 100 && device.closes == 2
	})
	if volume, speed := player.requestedSettings(); volume != 140 || speed != 1 {
		t.Fatal("o volume acima de 100% não foi mantido como ganho renderizado:", volume, speed)
	}
	if !player.Snapshot().Paused {
		t.Fatal("ajustar o volume retomou a reprodução pausada")
	}
	cancel()
	if err := playbackResult(t, result); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	after, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(after, before) {
		t.Fatal("o volume de saída alterou as amostras da gravação")
	}
}

func TestPlayerChangesPitchPreservingRateAndKeepsLogicalPosition(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "original.wav")
	silenceFixture(t, source, 8)
	player := New(directory)
	prepared := make(chan renderOptions, 2)
	player.prepare = func(_ context.Context, _, _ string, options renderOptions) (preparedAudio, error) {
		prepared <- options
		return preparedAudio{path: filepath.Join(directory, "prepared.wav")}, nil
	}
	outputs := make(chan *fakeOutput, 2)
	player.open = func(path string) (output, error) {
		var options renderOptions
		select {
		case options = <-prepared:
		default:
			return nil, errors.New("o preparado não informa as opções")
		}
		device := &fakeOutput{duration: 8 / options.Speed}
		outputs <- device
		return device, nil
	}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	result := make(chan error, 1)
	go func() { result <- player.Run(ctx, source) }()
	first := <-outputs
	awaitPlayback(t, func() bool { return player.Snapshot().Ready })
	if state := player.Snapshot(); !state.Ready || state.Duration != 8 {
		t.Fatal("a reprodução normal não manteve a duração original:", state)
	}
	first.update(func(device *fakeOutput) { device.position = 2 })
	awaitPlayback(t, func() bool { return player.Snapshot().Position == 2 })
	player.SetPaused(true)
	awaitPlayback(t, func() bool { return player.Snapshot().Paused })
	player.SetSpeed(2)
	second := <-outputs
	awaitPlayback(t, func() bool {
		state := player.Snapshot()
		return state.Ready && state.Paused && state.Position == 2 && state.Duration == 8
	})
	second.mu.Lock()
	seeks, playing := append([]float64(nil), second.seeks...), second.playing
	second.mu.Unlock()
	if len(seeks) != 1 || seeks[0] != 1 || playing {
		t.Fatal("a velocidade não preservou a posição lógica nem a pausa:", seeks, playing)
	}
	player.Seek(6)
	awaitPlayback(t, func() bool { return player.Snapshot().Position == 6 })
	second.mu.Lock()
	seeks = append([]float64(nil), second.seeks...)
	second.mu.Unlock()
	if len(seeks) != 2 || seeks[1] != 3 {
		t.Fatal("o seek não converteu a posição pela velocidade selecionada:", seeks)
	}
	cancel()
	if err := playbackResult(t, result); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestPlayerKeepsPauseAndSeekRequestsMadeWhileRenderingChanges(t *testing.T) {
	for _, setting := range []string{"speed", "volume"} {
		for _, pause := range []bool{true, false} {
			name := setting + "/pause"
			if !pause {
				name = setting + "/resume"
			}
			t.Run(name, func(t *testing.T) {
				directory := t.TempDir()
				source := filepath.Join(directory, "original.wav")
				before := silenceFixture(t, source, 8)
				player := New(directory)
				started, release := make(chan struct{}), make(chan struct{})
				options := make(chan renderOptions, 2)
				preparations := 0
				player.prepare = func(ctx context.Context, _, _ string, settings renderOptions) (preparedAudio, error) {
					preparations++
					if preparations == 2 {
						close(started)
						select {
						case <-release:
						case <-ctx.Done():
							return preparedAudio{}, ctx.Err()
						}
					}
					options <- settings
					return preparedAudio{path: filepath.Join(directory, "prepared.wav")}, nil
				}
				outputs := make(chan *fakeOutput, 2)
				player.open = func(string) (output, error) {
					settings := <-options
					device := &fakeOutput{duration: 8 / settings.Speed}
					outputs <- device
					return device, nil
				}
				ctx, cancel := context.WithCancel(t.Context())
				t.Cleanup(cancel)
				result := make(chan error, 1)
				go func() { result <- player.Run(ctx, source) }()
				first := <-outputs
				awaitPlayback(t, func() bool { return player.Snapshot().Ready })
				if !pause {
					player.SetPaused(true)
					awaitPlayback(t, func() bool {
						first.mu.Lock()
						defer first.mu.Unlock()
						return first.pauses == 1
					})
				}
				if setting == "speed" {
					player.SetSpeed(2)
				} else {
					player.SetVolume(40)
				}
				select {
				case <-started:
				case <-time.After(3 * time.Second):
					t.Fatal("a nova renderização não começou")
				}
				player.SetPaused(pause)
				player.Seek(6)
				close(release)
				second := <-outputs
				awaitPlayback(t, func() bool {
					state := player.Snapshot()
					second.mu.Lock()
					defer second.mu.Unlock()
					return state.Position == 6 && state.Paused == pause && second.playing == !pause
				})
				cancel()
				if err := playbackResult(t, result); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				after, err := os.ReadFile(source)
				if err != nil || !bytes.Equal(before, after) {
					t.Fatal("alterar controles durante a renderização modificou o original")
				}
			})
		}
	}
}

func TestPlayerRejectsSecondRunAndMissingContextWithoutInterruptingPlayback(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "original.wav")
	silenceFixture(t, source, 1)
	player := New(directory)
	player.open = func(string) (output, error) { return &fakeOutput{duration: 1}, nil }
	if err := player.Run(nil, source); err == nil {
		t.Fatal("um contexto ausente foi aceito")
	}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	result := make(chan error, 1)
	go func() { result <- player.Run(ctx, source) }()
	awaitPlayback(t, func() bool { return player.Snapshot().Ready })
	if err := player.Run(t.Context(), source); err == nil || !player.Snapshot().Active {
		t.Fatal("uma segunda execução tomou a saída da reprodução atual")
	}
	cancel()
	if err := playbackResult(t, result); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
