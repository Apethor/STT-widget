package desktop

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ai-talker/internal/config"
	"ai-talker/internal/platform"
	"ai-talker/internal/recorder"
	capturevideo "ai-talker/internal/video"

	"fyne.io/fyne/v2/test"
)

type fakeScreenVideo struct {
	mu           sync.Mutex
	startErr     error
	finishErr    error
	started      bool
	paused       int
	resumed      int
	aborted      int
	finished     int
	tracks       []recorder.Track
	output       string
	origin       recorder.Snapshot
	preset       capturevideo.Preset
	startPreset  capturevideo.Preset
	order        []string
	pauseEntered chan struct{}
	pauseRelease <-chan struct{}
}

func (v *fakeScreenVideo) SetPreset(preset capturevideo.Preset) error {
	v.mu.Lock()
	v.preset = preset
	v.mu.Unlock()
	return nil
}

func (v *fakeScreenVideo) SetAudioOrigin(snapshot recorder.Snapshot) {
	v.mu.Lock()
	v.origin = snapshot
	v.mu.Unlock()
}

func (v *fakeScreenVideo) Start(context.Context) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.startErr == nil {
		v.started = true
		v.startPreset = v.preset
	}
	return v.startErr
}

func (v *fakeScreenVideo) Pause(ctx context.Context) error {
	v.mu.Lock()
	v.paused++
	v.order = append(v.order, "pause")
	entered, release := v.pauseEntered, v.pauseRelease
	v.mu.Unlock()
	if entered != nil {
		select {
		case entered <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (v *fakeScreenVideo) Resume(context.Context) error {
	v.mu.Lock()
	v.resumed++
	v.order = append(v.order, "resume")
	v.mu.Unlock()
	return nil
}

func (v *fakeScreenVideo) Finish(_ context.Context, tracks []recorder.Track) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.finished++
	v.order = append(v.order, "finish")
	v.tracks = append([]recorder.Track(nil), tracks...)
	return v.output, v.finishErr
}

func (v *fakeScreenVideo) Abort() error {
	v.mu.Lock()
	v.aborted++
	v.mu.Unlock()
	return nil
}

func (v *fakeScreenVideo) snapshot() (started bool, paused, resumed, finished, aborted int, tracks []recorder.Track) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.started, v.paused, v.resumed, v.finished, v.aborted, append([]recorder.Track(nil), v.tracks...)
}

func TestVideoCaptureFollowsAudioPauseAndSavesOpenableVideoReference(t *testing.T) {
	audioPath := filepath.Join(t.TempDir(), "20261011-120000.wav")
	r := &fakeRecording{state: recorder.Snapshot{
		Path: audioPath, Duration: 2, SampleRate: 16000, WrittenSamples: 32000,
		StartedAt: time.Now().Add(-250 * time.Millisecond),
	}}
	videoPath := filepath.Join(t.TempDir(), "20261011-120000-video.mp4")
	video := &fakeScreenVideo{output: videoPath}
	opened := make(chan string, 1)
	folder := make(chan string, 1)
	startedAt := time.Date(2026, 10, 12, 12, 0, 0, 0, time.Local)
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.now = func() time.Time { return startedAt }
		deps.newRecorder = func(int, string) recording { return r }
		deps.selectVideoRegion = func(context.Context) (platform.ScreenRegion, error) {
			return platform.ScreenRegion{X: -40, Y: 20, Width: 640, Height: 480}, nil
		}
		deps.newVideoRecorder = func(directory, path string, region platform.ScreenRegion) (screenVideoCapture, error) {
			folder <- directory
			if path != audioPath || region.X != -40 || region.Width != 640 {
				t.Errorf("o vídeo não recebeu o áudio ou a área escolhida: %q %+v", path, region)
			}
			return video, nil
		}
		deps.openFile = func(path string) error {
			opened <- path
			return nil
		}
	})
	h.d.settings.VideoPreset = config.VideoLow

	test.Tap(h.d.videoButton)
	h.await(func() bool { return h.d.videoArmed })
	if h.d.videoRegion == nil || h.d.recordButton.Text != "●  Gravar áudio e vídeo" {
		t.Fatal("a área selecionada não preparou a próxima gravação")
	}
	test.Tap(h.d.recordButton)
	h.await(func() bool {
		started, _, _, _, _, _ := video.snapshot()
		return h.d.capture == captureRunning && started
	})
	if h.d.videoArmed || h.d.videoSession != video {
		t.Fatal("a gravação não consumiu a preparação nem guardou a sessão de vídeo")
	}
	if got := waitValue(t, folder); got != filepath.Join(h.d.directory, "videos", "2026-10-12") {
		t.Fatal("o vídeo não foi preparado na pasta de capturas do dia", got)
	}
	video.mu.Lock()
	origin := video.origin
	preset, startPreset := video.preset, video.startPreset
	video.mu.Unlock()
	if !origin.StartedAt.Equal(r.Snapshot().StartedAt) {
		t.Fatal("o primeiro quadro não recebeu a origem temporal do áudio")
	}
	if preset != capturevideo.PresetLow || startPreset != capturevideo.PresetLow {
		t.Fatal("a captura começou sem fixar o preset selecionado", preset, startPreset)
	}
	h.d.settings.VideoPreset = config.VideoHigh

	test.Tap(h.d.pauseButton)
	h.await(func() bool {
		_, paused, _, _, _, _ := video.snapshot()
		return paused == 1
	})
	test.Tap(h.d.pauseButton)
	h.await(func() bool {
		_, _, resumed, _, _, _ := video.snapshot()
		return resumed == 1
	})
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureIdle && !h.d.busy })

	started, paused, resumed, finished, aborted, tracks := video.snapshot()
	video.mu.Lock()
	preset = video.preset
	video.mu.Unlock()
	if preset != capturevideo.PresetLow {
		t.Fatal("trocar a preferência durante a captura alterou o vídeo em andamento")
	}
	if !started || paused != 1 || resumed != 1 || finished != 1 || aborted != 0 ||
		len(tracks) != 1 || tracks[0].Snapshot.Path != audioPath ||
		h.d.recordedAppend == nil || h.d.recordedAppend.VideoPath != videoPath ||
		!h.d.openVideoButton.Visible() {
		t.Fatalf("a sessão de vídeo não acompanhou a gravação ou perdeu o WAV original: %+v %+v",
			video, h.d.recordedAppend)
	}
	test.Tap(h.d.openVideoButton)
	if got := waitValue(t, opened); got != videoPath {
		t.Fatalf("o botão abriu outro vídeo: %q", got)
	}
}

func TestVideoRapidPauseResumeAndStopKeepClickOrderAndWaitForEncoder(t *testing.T) {
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	video := &fakeScreenVideo{
		output:       filepath.Join(t.TempDir(), "video.mp4"),
		pauseEntered: entered, pauseRelease: release,
	}
	r := &fakeRecording{state: recorder.Snapshot{
		Path: filepath.Join(t.TempDir(), "audio.wav"), Duration: 2,
		SampleRate: 16000, WrittenSamples: 32000,
	}}
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.newRecorder = func(int, string) recording { return r }
		deps.newVideoRecorder = func(string, string, platform.ScreenRegion) (screenVideoCapture, error) {
			return video, nil
		}
	})
	var unlock sync.Once
	t.Cleanup(func() { unlock.Do(func() { close(release) }) })
	h.d.videoRegion = &platform.ScreenRegion{Width: 640, Height: 480}
	h.d.videoArmed = true
	h.d.syncControls()
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	test.Tap(h.d.pauseButton)
	waitValue(t, entered)
	test.Tap(h.d.pauseButton)
	test.Tap(h.d.pauseButton)
	test.Tap(h.d.recordButton)
	_, paused, resumed, finished, _, _ := video.snapshot()
	if h.d.capture != captureStopping || paused != 1 || resumed != 0 || finished != 0 {
		t.Fatal("retomar ou finalizar ultrapassou a pausa que ainda libera o codificador")
	}
	unlock.Do(func() { close(release) })
	h.await(func() bool { return h.d.capture == captureIdle && !h.d.busy })
	video.mu.Lock()
	order := append([]string(nil), video.order...)
	video.mu.Unlock()
	if strings.Join(order, ",") != "pause,resume,pause,finish" {
		t.Fatalf("os comandos de vídeo ficaram fora da ordem do usuário: %v", order)
	}
}

func TestVideoStartupFailureLeavesAudioRecordingActive(t *testing.T) {
	r := &fakeRecording{state: recorder.Snapshot{
		Path: filepath.Join(t.TempDir(), "gravacao.wav"), Duration: 2, SampleRate: 16000, WrittenSamples: 32000,
	}}
	video := &fakeScreenVideo{startErr: errors.New("FFmpeg não iniciou")}
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.newRecorder = func(int, string) recording { return r }
		deps.newVideoRecorder = func(string, string, platform.ScreenRegion) (screenVideoCapture, error) {
			return video, nil
		}
	})
	h.d.videoRegion = &platform.ScreenRegion{X: 0, Y: 0, Width: 640, Height: 480}
	h.d.videoArmed = true
	h.d.syncControls()
	test.Tap(h.d.recordButton)
	h.await(func() bool {
		_, _, _, _, aborted, _ := video.snapshot()
		return h.d.capture == captureRunning && aborted == 1
	})
	if !r.Snapshot().Active || h.d.videoSession != nil || h.d.videoArmed ||
		!strings.Contains(h.d.recordState.Text, "vídeo") {
		t.Fatal("a falha de vídeo interrompeu o áudio ou deixou a opção armada")
	}
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureIdle && !h.d.busy })
	if h.d.lastAudio == "" || h.d.recordedAppend != nil && h.d.recordedAppend.VideoPath != "" {
		t.Fatal("a falha de vídeo descartou o áudio ou registrou um vídeo inexistente")
	}
}

func TestVideoCompletionDoesNotHideAudioErrorsOrAnnounceAResetAsSuccess(t *testing.T) {
	for _, tc := range []struct {
		name      string
		action    captureAction
		noAudio   bool
		audioErr  error
		videoErr  error
		wantError string
	}{
		{name: "áudio parcial com erro", action: transcribeCapture,
			audioErr: errors.New("falha ao salvar áudio"), wantError: "falha ao salvar áudio"},
		{name: "sem áudio salvo", action: transcribeCapture, noAudio: true,
			audioErr: errors.New("falha ao salvar áudio"), wantError: "falha ao salvar áudio"},
		{name: "erros de áudio e vídeo", action: transcribeCapture,
			audioErr: errors.New("falha ao salvar áudio"), videoErr: errors.New("falha do encoder"),
			wantError: "falha do encoder"},
		{name: "novo áudio", action: newCapture},
	} {
		t.Run(tc.name, func(t *testing.T) {
			audioPath := filepath.Join(t.TempDir(), "gravacao.wav")
			r := &fakeRecording{
				state: recorder.Snapshot{Path: audioPath, Duration: 2},
				stop: func() (string, error) {
					if tc.noAudio {
						return "", tc.audioErr
					}
					return audioPath, tc.audioErr
				},
			}
			video := &fakeScreenVideo{output: audioPath + ".mp4", finishErr: tc.videoErr}
			h := newHarness(t, nil, func(deps *dependencies) {
				deps.newRecorder = func(int, string) recording { return r }
				deps.newVideoRecorder = func(string, string, platform.ScreenRegion) (screenVideoCapture, error) {
					return video, nil
				}
			})
			h.d.videoRegion = &platform.ScreenRegion{Width: 640, Height: 480}
			h.d.videoArmed = true
			h.d.syncControls()
			test.Tap(h.d.recordButton)
			h.await(func() bool { return h.d.capture == captureRunning })
			h.d.stopCapture(tc.action)
			h.await(func() bool { return h.d.capture == captureIdle })
			if strings.Contains(h.d.status.Text, "Áudio e vídeo salvos") ||
				tc.noAudio && strings.Contains(h.d.status.Text, "O áudio foi preservado") {
				t.Fatal("o status anunciou sucesso ou preservação de áudio que não foi salvo", h.d.status.Text)
			}
			if tc.wantError != "" && (!h.d.errorLabel.Visible() ||
				!strings.Contains(h.d.errorLabel.Text, tc.wantError)) {
				t.Fatal("o resultado do vídeo escondeu o erro da captura", h.d.errorLabel.Text)
			}
			if tc.audioErr != nil && !strings.Contains(h.d.errorLabel.Text, tc.audioErr.Error()) {
				t.Fatal("a falha de áudio desapareceu ao apresentar o erro de vídeo")
			}
			if tc.action == newCapture && h.d.lastAudio != "" {
				t.Fatal("Novo áudio restaurou o arquivo que acabou de encerrar")
			}
		})
	}
}
