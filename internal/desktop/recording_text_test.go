package desktop

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"ai-talker/internal/config"
	"ai-talker/internal/recorder"
	"ai-talker/internal/transcript"

	"fyne.io/fyne/v2/test"
)

func TestConsecutiveRecordingsOnlyAppendToExistingText(t *testing.T) {
	recordings := 0
	h := newHarness(t, runnerFunc(func(_ context.Context, source string, _ config.Settings, _ transcript.Request) (
		transcript.Result, error,
	) {
		text := "Primeira gravação."
		if source == "gravacao-2.wav" {
			text = "Segunda gravação."
		}
		return resultFor(source, text), nil
	}), func(deps *dependencies) {
		deps.newRecorder = func(int, string) recording {
			recordings++
			return &fakeRecording{state: recorder.Snapshot{Path: fmt.Sprintf("gravacao-%d.wav", recordings)}}
		}
	})
	h.d.editor.SetText("Texto manual preservado.")
	h.d.processEditor.SetText("Resposta anterior preservada.")
	want := h.d.editor.Text
	for _, text := range []string{"Primeira gravação.", "Segunda gravação."} {
		test.Tap(h.d.recordButton)
		h.await(func() bool { return h.d.capture == captureRunning })
		if h.d.editor.Text != want {
			t.Fatal("gravar novamente limpou ou substituiu o texto existente")
		}
		test.Tap(h.d.recordButton)
		h.await(func() bool { return h.d.capture == captureIdle && !h.d.busy })
		want += "\n\n" + text
		if h.d.editor.Text != want || h.d.processEditor.Text != "Resposta anterior preservada." {
			t.Fatalf("a transcrição não foi acrescentada apenas ao final: %q", h.d.editor.Text)
		}
	}
	if saved := readResult(t, h.d.latestPath()); saved.Text != want {
		t.Fatal("o resultado salvo perdeu uma gravação ou o texto manual")
	}
}

func TestRecordingKeepsCurrentEditorsAndAppendsToTheirLatestText(t *testing.T) {
	release := make(chan struct{})
	releaseJob := sync.OnceFunc(func() { close(release) })
	started := make(chan struct{}, 1)
	r := &fakeRecording{state: recorder.Snapshot{Path: "nova-gravacao.wav"}}
	h := newHarness(t, runnerFunc(func(_ context.Context, source string, _ config.Settings, _ transcript.Request) (
		transcript.Result, error,
	) {
		started <- struct{}{}
		<-release
		return resultFor(source, "Texto da nova gravação."), nil
	}), func(deps *dependencies) { deps.newRecorder = func(int, string) recording { return r } })
	t.Cleanup(releaseJob)
	first := h.d.document
	first.editor.SetText("Texto que já existia.")
	first.processEditor.SetText("Mensagem que já estava pronta.")
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	if first.editor.Text != "Texto que já existia." || first.processEditor.Text != "Mensagem que já estava pronta." {
		t.Fatal("começar outra gravação limpou algum editor")
	}
	test.Tap(h.d.recordButton)
	h.await(func() bool { return first.busy })
	waitValue(t, started)
	first.editor.SetText("Texto anterior com uma edição feita durante a espera.")
	second := h.d.addDocument()
	second.editor.SetText("Outra aba isolada.")
	releaseJob()
	h.await(func() bool { return !first.busy })
	const combined = "Texto anterior com uma edição feita durante a espera.\n\nTexto da nova gravação."
	if first.editor.Text != combined || first.processEditor.Text != "Mensagem que já estava pronta." ||
		second.editor.Text != "Outra aba isolada." {
		t.Fatal("a nova transcrição substituiu a edição ou foi enviada à outra aba")
	}
	if saved := readResult(t, h.d.documentLatestPath(first)); saved.Text != combined {
		t.Fatal("o texto acumulado não foi preservado no snapshot da aba")
	}
}

func TestSilentRecordingKeepsExistingTextAndProcessedResult(t *testing.T) {
	r := &fakeRecording{state: recorder.Snapshot{Path: "silencio.wav"}}
	h := newHarness(t, runnerFunc(func(_ context.Context, source string, _ config.Settings, _ transcript.Request) (
		transcript.Result, error,
	) {
		return transcript.Result{Source: source, DurationSeconds: 2, Parts: []transcript.Part{{NoSpeech: true}}}, nil
	}), func(deps *dependencies) { deps.newRecorder = func(int, string) recording { return r } })
	h.d.editor.SetText("Texto anterior preservado.")
	h.d.processEditor.SetText("Mensagem anterior preservada.")
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureIdle && !h.d.busy })
	if h.d.editor.Text != "Texto anterior preservado." || h.d.processEditor.Text != "Mensagem anterior preservada." ||
		h.d.copyButton.Disabled() || h.d.recordState.Text != "Nenhuma fala detectada." {
		t.Fatal("uma gravação sem fala apagou o texto ou fingiu uma nova transcrição")
	}
}

func TestRecordedTranscriptionRetryReplacesOnlyItsUneditedSuffix(t *testing.T) {
	r := &fakeRecording{state: recorder.Snapshot{Path: "nova.wav"}}
	calls := 0
	h := newHarness(t, runnerFunc(func(_ context.Context, source string, _ config.Settings, _ transcript.Request) (
		transcript.Result, error,
	) {
		calls++
		if calls == 1 {
			return transcript.Result{}, &transcript.Error{Cause: errors.New("falha temporária"), PartialText: "Parte reconhecida."}
		}
		return resultFor(source, "Transcrição completa."), nil
	}), func(deps *dependencies) { deps.newRecorder = func(int, string) recording { return r } })
	h.d.editor.SetText("Texto das gravações anteriores.")
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureIdle && !h.d.busy })
	if h.d.editor.Text != "Texto das gravações anteriores.\n\nParte reconhecida." || !h.d.retryButton.Visible() {
		t.Fatal("a falha parcial perdeu o texto anterior ou a retomada")
	}
	test.Tap(h.d.retryButton)
	h.await(func() bool { return !h.d.busy })
	if h.d.editor.Text != "Texto das gravações anteriores.\n\nTranscrição completa." {
		t.Fatalf("a retomada apagou a origem ou duplicou a parte parcial: %q", h.d.editor.Text)
	}
}

func TestManualClearDuringRecordingStillStartsNewText(t *testing.T) {
	r := &fakeRecording{state: recorder.Snapshot{Path: "nova.wav"}}
	h := newHarness(t, nil, func(deps *dependencies) { deps.newRecorder = func(int, string) recording { return r } })
	h.d.editor.SetText("Texto que vou limpar manualmente.")
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	test.Tap(h.d.clearButton)
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureIdle && !h.d.busy })
	if h.d.editor.Text != "Texto desta gravação." || h.d.undo == nil {
		t.Fatal("a nova gravação recolocou o texto limpo ou apagou o desfazer da limpeza")
	}
	test.Tap(h.d.undoButton)
	if h.d.editor.Text != "Texto que vou limpar manualmente." {
		t.Fatal("desfazer não recuperou a limpeza manual depois da gravação")
	}
}

func TestRecordedRetryAfterReopenPreservesPriorTextAndManualCorrections(t *testing.T) {
	for _, edited := range []bool{false, true} {
		name := "trecho parcial intacto"
		if edited {
			name = "trecho parcial corrigido"
		}
		t.Run(name, func(t *testing.T) {
			r := &fakeRecording{state: recorder.Snapshot{Path: "retomar.wav"}}
			h := newHarness(t, runnerFunc(func(context.Context, string, config.Settings, transcript.Request) (
				transcript.Result, error,
			) {
				return transcript.Result{}, &transcript.Error{
					Cause: errors.New("falha temporária"), PartialText: "Trecho parcial.",
				}
			}), func(deps *dependencies) { deps.newRecorder = func(int, string) recording { return r } })
			h.d.editor.SetText("Texto das gravações anteriores.")
			previous := resultFor("anterior.wav", h.d.editor.Text)
			h.d.lastResult, h.d.lastAudio = &previous, previous.Source
			h.d.processEditor.SetText("Mensagem pronta que continua aqui.")
			test.Tap(h.d.recordButton)
			h.await(func() bool { return h.d.capture == captureRunning })
			test.Tap(h.d.recordButton)
			h.await(func() bool { return h.d.capture == captureIdle && !h.d.busy })
			prefix := "Texto das gravações anteriores."
			if edited {
				prefix = "Texto anterior editado.\n\nTrecho parcial corrigido manualmente."
				h.d.editor.SetText(prefix)
			}
			if err := h.d.saveWorkspace(); err != nil {
				t.Fatal(err)
			}
			restored := newHarness(t, runnerFunc(func(_ context.Context, source string, _ config.Settings, _ transcript.Request) (
				transcript.Result, error,
			) {
				return resultFor(source, "Transcrição completa."), nil
			}), nil)
			restored.d.directory = h.d.directory
			if !restored.d.loadWorkspace() || !restored.d.retryButton.Visible() || restored.d.retryButton.Disabled() {
				t.Fatal("reabrir perdeu a retomada da última gravação")
			}
			test.Tap(restored.d.retryButton)
			restored.await(func() bool { return !restored.d.busy })
			want := prefix + "\n\nTranscrição completa."
			if restored.d.editor.Text != want || restored.d.processEditor.Text != "Mensagem pronta que continua aqui." ||
				readResult(t, restored.d.latestPath()).Text != want {
				t.Fatalf("retomar após reabrir perdeu texto anterior, correção ou resultado: %q", restored.d.editor.Text)
			}
		})
	}
}
