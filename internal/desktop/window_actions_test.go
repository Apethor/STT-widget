package desktop

import (
	"errors"
	"sync/atomic"
	"testing"

	"ai-talker/internal/recorder"

	"fyne.io/fyne/v2/test"
)

func TestHeaderMinimizePreservesRecordingAndReportsFailure(t *testing.T) {
	var calls atomic.Int32
	captured := &fakeRecording{state: recorder.Snapshot{Path: "captura.wav"}}
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.newRecorder = func(int, string) recording { return captured }
		deps.minimize = func() error {
			if calls.Add(1) == 2 {
				return errors.New("minimizar indisponível")
			}
			return nil
		}
	})
	h.d.editor.SetText("Texto preservado ao minimizar.")
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	test.Tap(h.d.minimizeButton)
	h.settle()
	test.Tap(h.d.minimizeButton)
	h.settle()
	if calls.Load() != 2 || !captured.Snapshot().Active || h.d.capture != captureRunning ||
		h.d.editor.Text != "Texto preservado ao minimizar." || h.d.closing ||
		!h.d.errorLabel.Visible() || h.d.recordButton.Disabled() {
		t.Fatal("minimizar interrompeu a gravação, perdeu texto ou ocultou sua falha")
	}
}
