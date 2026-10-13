package desktop

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ai-talker/internal/config"
	"ai-talker/internal/transcript"

	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/test"
)

func TestCancelWhisperOffersRetryWithSameAudioAndNewEngine(t *testing.T) {
	type call struct {
		source, provider string
		ctx              context.Context
	}
	calls := make(chan call, 2)
	h := newHarness(t, runnerFunc(func(ctx context.Context, source string, settings config.Settings, _ transcript.Request) (
		transcript.Result, error,
	) {
		calls <- call{source, settings.TranscriptionProvider(), ctx}
		if settings.TranscriptionProvider() == config.ProviderWhisper {
			<-ctx.Done()
			return resultFor(source, "Resposta local antiga."), nil
		}
		return resultFor(source, "Transcrição pelo novo motor."), nil
	}), nil)
	source := filepath.Join(h.d.directory, "original.wav")
	if err := os.WriteFile(source, []byte("áudio original preservado"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.d.settings.Provider = config.ProviderWhisper
	h.d.startJob(source)
	first := waitValue(t, calls)
	test.Tap(h.d.recordButton)
	if first.ctx.Err() == nil || h.d.busy || !h.d.retryButton.Visible() || h.d.retryButton.Disabled() {
		t.Fatal("cancelling local transcription did not expose a usable retry")
	}
	h.d.setTranscriptionProvider(config.ProviderBedrock)
	h.settle()
	test.Tap(h.d.retryButton)
	second := waitValue(t, calls)
	h.await(func() bool { return !h.d.busy })
	if first.source != source || second.source != source || second.provider != config.ProviderBedrock ||
		h.d.editor.Text != "Transcrição pelo novo motor." || h.d.ctx.Err() != nil {
		t.Fatal("retry reimported, lost the audio or accepted a stale local response")
	}
	requireFile(t, source, "áudio original preservado")
}

func TestImportedAudioCancellationRestoresRetryAfterReopening(t *testing.T) {
	h := newHarness(t, runnerFunc(func(ctx context.Context, source string, _ config.Settings, _ transcript.Request) (
		transcript.Result, error,
	) {
		<-ctx.Done()
		return transcript.Result{}, ctx.Err()
	}), nil)
	h.d.startJob("long-audio.wav")
	test.Tap(h.d.recordButton)
	h.settle()
	if err := h.d.saveWorkspace(); err != nil {
		t.Fatal(err)
	}
	if !h.d.loadWorkspace() || h.d.lastAudio != "long-audio.wav" ||
		!h.d.retryButton.Visible() || h.d.retryButton.Disabled() {
		t.Fatal("reopening lost the cancelled audio and retry")
	}
}

func TestImportAndSaveFileDialogsOpenWithoutPanicOrResizingMainWindow(t *testing.T) {
	for _, action := range []string{"import", "save transcript", "save processed"} {
		t.Run(action, func(t *testing.T) {
			h := newHarness(t, nil, nil)
			h.app.Preferences().SetString("fyne:fileDialogLastFolder", storage.NewFileURI(h.d.directory).String())
			h.d.editor.SetText("Transcrição preservada.")
			h.d.processEditor.SetText("Resultado preservado.")
			size := h.d.window.Canvas().Size()
			before := len(h.d.window.Canvas().Overlays().List())
			// Direct calls deliberately let a dialog initialization panic fail
			// this test instead of being hidden by the normal UI recovery.
			if action == "import" {
				h.d.importAudio()
			} else {
				h.d.saveDocumentText(h.d.document, strings.Contains(action, "processed"))
			}
			if len(h.d.window.Canvas().Overlays().List()) <= before ||
				h.d.window.Canvas().Size() != size || h.d.editor.Text != "Transcrição preservada." {
				t.Fatal("the file picker failed to open, resized the app or cleared the editor")
			}
		})
	}
}

func TestWhisperEngineAndModelFileDialogsOpenWithoutPanic(t *testing.T) {
	for _, engine := range []bool{false, true} {
		h := newHarness(t, nil, nil)
		h.app.Preferences().SetString("fyne:fileDialogLastFolder", storage.NewFileURI(h.d.directory).String())
		test.Tap(h.d.settingsButton)
		h.await(func() bool { return !h.d.settingsForm.loading })
		before := len(h.d.window.Canvas().Overlays().List())
		size := h.d.window.Canvas().Size()
		h.d.chooseWhisperFile(h.d.settingsForm, engine)
		if len(h.d.window.Canvas().Overlays().List()) <= before || h.d.window.Canvas().Size() != size {
			t.Fatal("the local engine/model file picker failed or resized the main window")
		}
	}
}
