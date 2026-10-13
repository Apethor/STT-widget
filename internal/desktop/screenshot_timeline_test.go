package desktop

import (
	"bytes"
	"context"
	"errors"
	"image"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"ai-talker/internal/config"
	"ai-talker/internal/recorder"
	"ai-talker/internal/transcript"

	"fyne.io/fyne/v2/test"
)

func offlineScreenshotHarness(t *testing.T, runner Runner, configure func(*dependencies)) (*testHarness, *fakeRecording) {
	t.Helper()
	h, r := streamHarness(t, runner, configure)
	h.d.settings.Capture.Stream = false
	h.d.syncCaptureOptions()
	return h, r
}

func speechAroundScreenshot(source string) string {
	if strings.Contains(filepath.Base(source), "-000000000000-") {
		return "Olha essa imagem."
	}
	return "Veja como é bonita."
}

func TestOfflineScreenshotSplitsSavedAudioAndPlacesLinkBetweenSpeechForBothEngines(t *testing.T) {
	for _, scenario := range []struct {
		name, provider, mode string
		rate                 int
	}{
		{"bedrock-microfone", config.ProviderBedrock, config.CaptureMicrophone, 16000},
		{"whisper-microfone", config.ProviderWhisper, config.CaptureMicrophone, 16000},
		{"bedrock-sistema-48k", config.ProviderBedrock, config.CaptureSystem, 48000},
		{"whisper-sistema-48k", config.ProviderWhisper, config.CaptureSystem, 48000},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var calls atomic.Int32
			h, r := offlineScreenshotHarness(t, runnerFunc(func(_ context.Context, source string, settings config.Settings, request transcript.Request) (
				transcript.Result, error,
			) {
				calls.Add(1)
				if settings.TranscriptionProvider() != scenario.provider || settings.Quality != "fast" || request.OnPartial != nil || request.NativePCM ||
					request.NativeContextSamples != 0 || settings.OverlapSeconds != 0 {
					t.Error("a gravação comum usou transporte/contexto de Stream ou outro motor")
				}
				part, err := recorder.Inspect(source)
				if err != nil || part.SampleRate != scenario.rate ||
					part.Duration != 2.125 && part.Duration != 3.875 {
					t.Errorf("o corte não corresponde ao instante da foto: %+v / %v", part, err)
				}
				return resultFor(source, speechAroundScreenshot(source)), nil
			}), nil)
			h.d.settings.Provider, h.d.settings.Capture.Mode = scenario.provider, scenario.mode
			r.update(func(state *recorder.Snapshot) { state.SampleRate = scenario.rate })
			original := filepath.Join(fixtureRecordingFolder(h), "stream.wav")
			streamFixtureRate(t, original, 6, scenario.rate)
			before, err := os.ReadFile(original)
			if err != nil {
				t.Fatal(err)
			}
			doc := h.d.document
			doc.editor.SetText("Texto anterior.")
			test.Tap(h.d.recordButton)
			h.await(func() bool { return h.d.capture == captureRunning })
			admitStream(h, r, 2.125)
			shot := takeScreenshot(t, h)
			h.d.settings.Quality = "fast"
			admitStream(h, r, 6)
			if calls.Load() != 0 || doc.editor.Text != "Texto anterior." {
				t.Fatal("tirar screenshot antecipou o reconhecimento ou a publicação")
			}
			test.Tap(h.d.recordButton)
			h.await(func() bool { return h.d.capture == captureIdle && !doc.busy })
			h.settle()
			want := "Texto anterior.\n\nOlha essa imagem.\n\n" + shot.path + "\n\nVeja como é bonita."
			if calls.Load() != 2 || doc.editor.Text != want || doc.lastResult.DurationSeconds != 6 ||
				strings.Contains(doc.editor.Text, screenshotLinkHeading) ||
				len(collectLinks(doc.transcriptPreview.Segments)) != 1 {
				t.Fatalf("a foto não acompanhou a fala: %q", doc.editor.Text)
			}
			after, err := os.ReadFile(original)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("os cortes alteraram o WAV original")
			}
			if !doc.live.TranscribeOnStop || !h.d.validLiveArchive(doc.live.archive()) ||
				readResult(t, h.d.documentLatestPath(doc)).Text != want {
				t.Fatal("o resultado e a linha do tempo não foram preservados")
			}
		})
	}
}

func TestOfflineScreenshotPublishesOnceEvenWhenLaterSpeechCompletesFirst(t *testing.T) {
	firstStarted := make(chan struct{}, 1)
	secondStarted := make(chan struct{}, 1)
	releaseChannel := make(chan struct{})
	release := sync.OnceFunc(func() { close(releaseChannel) })
	h, r := offlineScreenshotHarness(t, runnerFunc(func(ctx context.Context, source string, _ config.Settings, _ transcript.Request) (
		transcript.Result, error,
	) {
		if strings.Contains(filepath.Base(source), "-000000000000-") {
			firstStarted <- struct{}{}
			select {
			case <-releaseChannel:
			case <-ctx.Done():
				return transcript.Result{}, ctx.Err()
			}
		} else {
			secondStarted <- struct{}{}
		}
		return resultFor(source, speechAroundScreenshot(source)), nil
	}), nil)
	t.Cleanup(release)
	h.d.editor.SetText("Aguardar sem apagar.")
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	admitStream(h, r, 2)
	shot := takeScreenshot(t, h)
	admitStream(h, r, 6)
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureIdle })
	waitValue(t, firstStarted)
	waitValue(t, secondStarted)
	h.await(func() bool { return len(h.d.live.running) == 1 })
	if h.d.editor.Text != "Aguardar sem apagar." || !h.d.busy || h.d.view.progress <= 0 {
		t.Fatal("a transcrição incompleta entrou no editor ou não mostrou progresso")
	}
	release()
	h.await(func() bool { return !h.d.busy })
	if h.d.editor.Text != "Aguardar sem apagar.\n\nOlha essa imagem.\n\n"+shot.path+"\n\nVeja como é bonita." {
		t.Fatal("a ordem de chegada das respostas deslocou o link")
	}
}

func TestOfflineScreenshotsDuringPauseShareRecordedPositionWithoutEmptyAudioParts(t *testing.T) {
	var calls atomic.Int32
	h, r := offlineScreenshotHarness(t, runnerFunc(func(_ context.Context, source string, _ config.Settings, _ transcript.Request) (
		transcript.Result, error,
	) {
		calls.Add(1)
		return resultFor(source, speechAroundScreenshot(source)), nil
	}), nil)
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	admitStream(h, r, 3)
	test.Tap(h.d.pauseButton)
	first, second := takeScreenshot(t, h), takeScreenshot(t, h)
	test.Tap(h.d.pauseButton)
	admitStream(h, r, 7)
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureIdle && !h.d.busy })
	want := "Olha essa imagem.\n\n" + first.path + "\n\n" + second.path + "\n\nVeja como é bonita."
	if calls.Load() != 2 || h.d.editor.Text != want || h.d.lastResult.DurationSeconds != 7 {
		t.Fatal("a pausa ou os marcadores iguais criaram trechos vazios/deslocaram a fala")
	}
}

func TestOfflineScreenshotUndoClearDuringCaptureKeepsEveryPhotoAndOriginalContext(t *testing.T) {
	h, r := offlineScreenshotHarness(t, runnerFunc(func(_ context.Context, source string, _ config.Settings, _ transcript.Request) (
		transcript.Result, error,
	) {
		text := "Fala depois das fotos."
		if strings.Contains(filepath.Base(source), "-000000000000-") {
			text = "Fala antes das fotos."
		} else if strings.Contains(filepath.Base(source), "-000000032000-") {
			text = "Fala entre as fotos."
		}
		return resultFor(source, text), nil
	}), nil)
	h.d.editor.SetText("Texto anterior.")
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	admitStream(h, r, 2)
	first := takeScreenshot(t, h)
	session, originalContext := h.d.live, h.d.live.ctx
	h.d.editor.SetText("Edição preservada.")
	test.Tap(h.d.clearButton)
	admitStream(h, r, 4)
	second := takeScreenshot(t, h)
	test.Tap(h.d.undoButton)
	if h.d.capture != captureRunning || h.d.live != session || session.ctx != originalContext ||
		session.ctx.Err() != nil || h.d.editor.Text != "Edição preservada." {
		t.Fatal("desfazer limpeza interrompeu a captura ou perdeu o texto restaurado")
	}
	admitStream(h, r, 6)
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureIdle && !h.d.busy })
	want := "Edição preservada.\n\nFala antes das fotos.\n\n" + first.path +
		"\n\nFala entre as fotos.\n\n" + second.path + "\n\nFala depois das fotos."
	if h.d.editor.Text != want {
		t.Fatalf("desfazer limpeza perdeu uma foto ainda não publicada: %q", h.d.editor.Text)
	}
}

func TestOfflineScreenshotSelectionFinishingAfterRecognitionKeepsReservedPositionAndTab(t *testing.T) {
	selected, releaseChannel := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(releaseChannel) })
	h, r := offlineScreenshotHarness(t, runnerFunc(func(_ context.Context, source string, _ config.Settings, _ transcript.Request) (
		transcript.Result, error,
	) {
		return resultFor(source, speechAroundScreenshot(source)), nil
	}), func(deps *dependencies) {
		deps.selectScreenshot = func(ctx context.Context) (image.Image, error) {
			close(selected)
			select {
			case <-releaseChannel:
				return screenshotFixture(), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	})
	t.Cleanup(release)
	first := h.d.document
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	admitStream(h, r, 2)
	test.Tap(h.d.screenshotButton)
	waitValue(t, selected)
	admitStream(h, r, 6)
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureIdle && !first.busy })
	second := h.d.addDocument()
	second.editor.SetText("Outra aba.")
	release()
	h.await(func() bool { return len(h.d.screenshotEditors) == 1 })
	shot := onlyScreenshotEditor(t, h.d)
	want := "Olha essa imagem.\n\n" + shot.path + "\n\nVeja como é bonita."
	if first.editor.Text != want || second.editor.Text != "Outra aba." ||
		readResult(t, h.d.documentLatestPath(first)).Text != want {
		t.Fatal("o fim da seleção alterou o ponto reservado ou a outra aba")
	}
}

func TestOfflineScreenshotRetryAndRetranscriptionKeepTimelineAfterReopen(t *testing.T) {
	h, r := offlineScreenshotHarness(t, runnerFunc(func(_ context.Context, source string, _ config.Settings, _ transcript.Request) (
		transcript.Result, error,
	) {
		if strings.Contains(filepath.Base(source), "-000000000000-") {
			return resultFor(source, "Olha essa imagem."), nil
		}
		return transcript.Result{}, errors.New("falha depois da foto")
	}), nil)
	h.d.editor.SetText("Texto anterior.")
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	admitStream(h, r, 2)
	shot := takeScreenshot(t, h)
	admitStream(h, r, 6)
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureIdle && !h.d.busy })
	h.settle()
	if !h.d.retryButton.Visible() || !h.d.lastResult.NeedsReview {
		t.Fatal("a falha parcial não ofereceu retomada")
	}
	if err := h.d.saveWorkspace(); err != nil {
		t.Fatal(err)
	}
	type call struct {
		source  string
		request transcript.Request
	}
	requests := make(chan call, 3)
	restored := newHarness(t, runnerFunc(func(_ context.Context, source string, settings config.Settings, request transcript.Request) (
		transcript.Result, error,
	) {
		requests <- call{source, request}
		if request.OnPartial != nil || request.NativePCM {
			t.Error("a retomada offline ativou Stream")
		}
		text := speechAroundScreenshot(source)
		if settings.TranscriptionProvider() == config.ProviderBedrock {
			text = "Nova " + text
		}
		return resultFor(source, text), nil
	}), nil)
	restored.d.directory = h.d.directory
	if !restored.d.loadWorkspace() || !restored.d.retryButton.Visible() {
		t.Fatal("reabrir perdeu a gravação ou a retomada")
	}
	restored.d.settings.Provider = config.ProviderWhisper
	test.Tap(restored.d.retryButton)
	restored.await(func() bool { return !restored.d.busy })
	retry := waitValue(t, requests)
	if strings.Contains(filepath.Base(retry.source), "-000000000000-") {
		t.Fatal("retomar repetiu o trecho já concluído")
	}
	want := "Texto anterior.\n\nOlha essa imagem.\n\n" + shot.path + "\n\nVeja como é bonita."
	if restored.d.editor.Text != want || restored.d.retryButton.Visible() {
		t.Fatal("retomar duplicou a fala ou moveu o link")
	}
	restored.d.settings.Provider, restored.d.settings.Quality = config.ProviderBedrock, "fast"
	test.Tap(restored.d.retranscribeButton)
	restored.await(func() bool { return !restored.d.busy })
	first, second := waitValue(t, requests), waitValue(t, requests)
	if first.request.CacheDir == "" || first.request.CacheDir != second.request.CacheDir ||
		restored.d.editor.Text != "Texto anterior.\n\nNova Olha essa imagem.\n\n"+shot.path+"\n\nNova Veja como é bonita." {
		t.Fatal("retranscrever não renovou as duas partes na posição preservada")
	}
	restored.settle()
}

func TestOfflineScreenshotAtRecordingBoundsDoesNotCreateEmptyAudio(t *testing.T) {
	for _, scenario := range []struct {
		name    string
		seconds float64
	}{
		{"inicio", 0},
		{"final", 5},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var calls atomic.Int32
			h, r := offlineScreenshotHarness(t, runnerFunc(func(_ context.Context, source string, _ config.Settings, _ transcript.Request) (
				transcript.Result, error,
			) {
				calls.Add(1)
				part, err := recorder.Inspect(source)
				if err != nil || part.Duration != 5 {
					t.Error("a captura na borda perdeu áudio ou gerou um trecho vazio")
				}
				return resultFor(source, "Fala completa."), nil
			}), nil)
			test.Tap(h.d.recordButton)
			h.await(func() bool { return h.d.capture == captureRunning })
			admitStream(h, r, scenario.seconds)
			shot := takeScreenshot(t, h)
			admitStream(h, r, 5)
			test.Tap(h.d.recordButton)
			h.await(func() bool { return h.d.capture == captureIdle && !h.d.busy })
			want := shot.path + "\n\nFala completa."
			if scenario.seconds > 0 {
				want = "Fala completa.\n\n" + shot.path
			}
			if calls.Load() != 1 || h.d.editor.Text != want {
				t.Fatal("a screenshot na borda não ficou antes/depois da fala")
			}
		})
	}
}

func TestOfflineLongRecordingWithScreenshotKeepsCoverageAndBoundedParts(t *testing.T) {
	var calls atomic.Int32
	h, r := offlineScreenshotHarness(t, runnerFunc(func(_ context.Context, source string, _ config.Settings, request transcript.Request) (
		transcript.Result, error,
	) {
		calls.Add(1)
		if request.OnPartial != nil || request.NativePCM {
			t.Error("o áudio longo foi enviado em Stream")
		}
		return resultFor(source, filepath.Base(source)), nil
	}), nil)
	streamFixtureSeconds(t, filepath.Join(fixtureRecordingFolder(h), "stream.wav"), 241)
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	admitStream(h, r, 121)
	shot := takeScreenshot(t, h)
	admitStream(h, r, 241)
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureIdle && !h.d.busy })
	var samples uint64
	var before, after string
	for _, entry := range h.d.live.Entries {
		if entry.Screenshot {
			continue
		}
		if entry.FirstSample != samples || entry.ContextSamples != 0 ||
			entry.LastSample-entry.FirstSample > cloudTeamPartSeconds*16000 {
			t.Fatal("a divisão perdeu/repetiu amostras ou excedeu o limite das partes")
		}
		samples = entry.LastSample
		if entry.End <= 121 {
			before = entry.Text
		} else {
			after = entry.Text
		}
	}
	text := h.d.editor.Text
	if calls.Load() != 5 || samples != 241*16000 || h.d.lastResult.DurationSeconds != 241 ||
		strings.Index(text, before) > strings.Index(text, shot.path) ||
		strings.Index(text, after) < strings.Index(text, shot.path) {
		t.Fatal("o áudio longo perdeu o fim ou o vínculo da screenshot")
	}
}

func TestOfflineTeamScreenshotAlignsBothTracksWhenTheirOffsetsFallBetweenSamples(t *testing.T) {
	r := &fakeTeamCapture{
		fakeRecording: &fakeRecording{state: recorder.Snapshot{SampleRate: 16000}},
		offset:        0.250013,
	}
	var calls atomic.Int32
	h := newHarness(t, runnerFunc(func(_ context.Context, source string, _ config.Settings, request transcript.Request) (
		transcript.Result, error,
	) {
		calls.Add(1)
		if request.OnPartial != nil || request.NativePCM {
			t.Error("a captura em Equipe ativou Stream")
		}
		text := "Microfone antes."
		system := strings.HasPrefix(filepath.Base(source), "system-")
		if system {
			text = "Tutorial antes."
		}
		if !strings.Contains(filepath.Base(source), "-000000000000-") {
			text = "Microfone depois."
			if system {
				text = "Tutorial depois."
			}
		}
		return resultFor(source, text), nil
	}), func(deps *dependencies) {
		deps.newConfiguredRecorder = func(_ config.Settings, directory string) recording {
			r.update(func(state *recorder.Snapshot) { state.Path = filepath.Join(directory, "mic.wav") })
			r.output = recorder.Snapshot{Path: filepath.Join(directory, "system.wav"), SampleRate: 16000, Active: true}
			return r
		}
		deps.selectScreenshot = func(context.Context) (image.Image, error) { return screenshotFixture(), nil }
	})
	h.d.settings.Capture.Mode = config.CaptureTeam
	for _, name := range []string{"mic.wav", "system.wav"} {
		streamFixtureSeconds(t, filepath.Join(fixtureRecordingFolder(h), name), 6)
	}
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	r.update(func(state *recorder.Snapshot) { state.Duration, state.WrittenSamples = 2, 32000 })
	r.outputMu.Lock()
	r.output.Duration, r.output.WrittenSamples = 1.75, 28000
	r.outputMu.Unlock()
	shot := takeScreenshot(t, h)
	r.update(func(state *recorder.Snapshot) { state.Duration, state.WrittenSamples = 6, 96000 })
	r.outputMu.Lock()
	r.output.Duration, r.output.WrittenSamples = 5.75, 92000
	r.outputMu.Unlock()
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureIdle && !h.d.busy })
	text := h.d.editor.Text
	if calls.Load() != 4 || !h.d.validLiveArchive(h.d.live.archive()) {
		t.Fatal("o corte não preservou as duas trilhas")
	}
	for _, phrase := range []string{"Microfone antes.", "Tutorial antes."} {
		if strings.Index(text, phrase) > strings.Index(text, shot.path) || !strings.Contains(text, phrase) {
			t.Fatal("a fala anterior de uma trilha apareceu depois do marcador")
		}
	}
	for _, phrase := range []string{"Microfone depois.", "Tutorial depois."} {
		if strings.Index(text, phrase) < strings.Index(text, shot.path) {
			t.Fatal("arredondar a amostra moveu a fala seguinte para antes do marcador")
		}
	}
}
