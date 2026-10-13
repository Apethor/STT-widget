package desktop

import (
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"ai-talker/internal/config"
	"ai-talker/internal/recorder"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

func TestCaptureVolumesOpenOnClickAndAlignInsidePopupAcrossLayouts(t *testing.T) {
	h := newHarness(t, nil, nil)
	for _, disposition := range []string{config.LayoutColumn, config.LayoutInline, config.LayoutStacked} {
		for _, size := range []string{config.RecordingLarge, config.RecordingMedium} {
			t.Run(disposition+"/"+size, func(t *testing.T) {
				old := h.d.settings.Appearance
				next := old
				next.Layout, next.RecordingSize = disposition, size
				h.d.settings.Appearance = next
				h.d.applyAppearance(old, next)
				h.d.window.Resize(h.d.initialWindowSize())
				for _, mode := range []string{teamChoice, microphoneChoice, teamChoice} {
					h.d.changeCaptureMode(mode)
					h.settle()
					h.d.window.Content().Refresh()
					panelPosition := h.app.Driver().AbsolutePositionForObject(h.d.recordPanel)
					for _, control := range []fyne.CanvasObject{h.d.recordButton, h.d.pauseButton, h.d.captureVolumes.button} {
						if !control.Visible() {
							continue
						}
						position := h.app.Driver().AbsolutePositionForObject(control)
						if position.Y < panelPosition.Y ||
							position.Y+control.Size().Height > panelPosition.Y+h.d.recordPanel.Size().Height+1 {
							t.Fatalf("o controle saiu do gravador em %s: %T em %v/%v, painel %v/%v",
								mode, control, position, control.Size(), panelPosition, h.d.recordPanel.Size())
						}
					}
					device := h.app.Driver().AbsolutePositionForObject(h.d.deviceButton)
					volume := h.app.Driver().AbsolutePositionForObject(h.d.captureVolumes.button)
					stream := h.app.Driver().AbsolutePositionForObject(h.d.streamButton)
					if volume.X < device.X+h.d.deviceButton.Size().Width ||
						volume.X+h.d.captureVolumes.button.Size().Width > stream.X ||
						math.Abs(float64(volume.Y-device.Y)) > 1 || math.Abs(float64(volume.Y-stream.Y)) > 1 {
						t.Fatal("o volume não ficou entre Microfone e Stream na linha de atalhos", device, volume, stream)
					}
					for _, object := range test.LaidOutObjects(h.d.window.Content()) {
						if object == h.d.captureVolumes.microphone || object == h.d.captureVolumes.playback {
							t.Fatal("o gravador exibiu uma barra de volume antes de clicar no botão")
						}
					}
					test.Tap(h.d.captureVolumes.button)
					assertPopupInCanvas(t, h, h.d.captureVolumes.popup)
					if mode != teamChoice {
						test.Tap(h.d.captureVolumes.button)
						continue
					}
					test.Tap(h.d.captureVolumes.button)
					if h.d.captureVolumes.popup.Visible() {
						t.Fatal("o segundo clique não recolheu os volumes")
					}
					mic, sys := h.d.captureVolumes.microphone, h.d.captureVolumes.playback
					micPosition := h.app.Driver().AbsolutePositionForObject(mic)
					sysPosition := h.app.Driver().AbsolutePositionForObject(sys)
					if math.Abs(float64(micPosition.X-sysPosition.X)) > 1 ||
						math.Abs(float64(mic.Size().Width-sys.Size().Width)) > 1 {
						t.Fatalf("as barras não estão alinhadas: %v/%v, %v/%v",
							micPosition, mic.Size(), sysPosition, sys.Size())
					}
				}
			})
		}
	}
}

func TestCaptureVolumeDoubleClickRestoresOnlyItsSourceAndPersists(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.d.changeCaptureMode(teamChoice)
	h.settle()
	test.Tap(h.d.captureVolumes.button)
	controls := h.d.captureVolumes
	controls.microphone.SetValue(0)
	controls.playback.SetValue(150)
	h.settle()
	controls.microphone.DoubleTapped(&fyne.PointEvent{Position: fyne.NewPos(10, controls.microphone.Size().Height/2)})
	h.settle()
	saved, err := config.Load(h.d.directory)
	if err != nil || controls.microphone.Value != 100 || controls.playback.Value != 150 ||
		config.CaptureVolume(saved.Capture.MicrophoneVolume) != 100 ||
		config.CaptureVolume(saved.Capture.PlaybackVolume) != 150 || !controls.popup.Visible() {
		t.Fatal("o duplo clique não restaurou somente o microfone", saved, err)
	}
	controls.microphone.SetValue(0)
	h.settle()
	controls.microphone.DoubleTapped(&fyne.PointEvent{Position: fyne.NewPos(controls.microphone.Size().Width/2, controls.microphone.Size().Height/2)})
	if controls.microphone.Value != 0 {
		t.Fatal("um duplo clique fora da bolinha restaurou o volume")
	}
	controls.microphone.Disable()
	controls.microphone.DoubleTapped(&fyne.PointEvent{Position: fyne.NewPos(10, controls.microphone.Size().Height/2)})
	if controls.microphone.Value != 0 {
		t.Fatal("o volume desabilitado aceitou um duplo clique")
	}
}

type gainRecording struct {
	fakeRecording
	muGain   sync.Mutex
	mic, sys float64
}

func (r *gainRecording) SetGains(microphone, playback float64) {
	r.muGain.Lock()
	r.mic, r.sys = microphone, playback
	r.muGain.Unlock()
}

func (r *gainRecording) gains() (float64, float64) {
	r.muGain.Lock()
	defer r.muGain.Unlock()
	return r.mic, r.sys
}

func TestCaptureVolumesFollowModeAndApplyDuringRecordingWithoutTickReset(t *testing.T) {
	r := &gainRecording{}
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.newConfiguredRecorder = func(_ config.Settings, directory string) recording {
			r.update(func(state *recorder.Snapshot) { state.Path = filepath.Join(directory, "volumes.wav") })
			return r
		}
	})
	controls := h.d.captureVolumes
	if !controls.micRow.Visible() || controls.sysRow.Visible() ||
		controls.microphone.Value != 100 || controls.playback.Value != 100 {
		t.Fatal("normal recording did not default to a single 100% microphone control")
	}
	h.d.settings.Capture.Mode = config.CaptureSystem
	h.d.syncCaptureOptions()
	if controls.micRow.Visible() || !controls.sysRow.Visible() {
		t.Fatal("system recording displayed the microphone control")
	}
	h.d.settings.Capture.Mode = config.CaptureTeam
	h.d.syncCaptureOptions()
	if !controls.micRow.Visible() || !controls.sysRow.Visible() {
		t.Fatal("team recording did not offer both source volumes")
	}
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	// Dragging changes the value before releasing; SetValue also fires the
	// release callback in Fyne and would not exercise the preview interval.
	controls.microphone.Value, controls.playback.Value = 45, 130
	controls.microphone.OnChanged(45)
	controls.playback.OnChanged(130)
	h.d.tick()
	microphone, playback := r.gains()
	if controls.microphone.Value != 45 || controls.playback.Value != 130 ||
		microphone != 0.45 || playback != 1.3 || controls.micValue.Text != "45%" {
		t.Fatalf("drag preview: mic=%.0f sys=%.0f gains=%.2f/%.2f label=%q adjusting=%v",
			controls.microphone.Value, controls.playback.Value, microphone, playback, controls.micValue.Text, controls.adjusting)
	}
	controls.microphone.OnChangeEnded(45)
	controls.playback.OnChangeEnded(130)
	h.settle()
	settings, err := config.Load(h.d.directory)
	if err != nil || config.CaptureVolume(settings.Capture.MicrophoneVolume) != 45 ||
		config.CaptureVolume(settings.Capture.PlaybackVolume) != 130 {
		t.Fatalf("volume preferences were not persisted independently: %+v / %v", settings.Capture, err)
	}
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureIdle && !h.d.busy })
}

func TestCaptureVolumeSaveFailureRestoresTheControlAndLiveGain(t *testing.T) {
	r := &gainRecording{}
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.newConfiguredRecorder = func(_ config.Settings, directory string) recording {
			r.update(func(state *recorder.Snapshot) { state.Path = filepath.Join(directory, "volumes.wav") })
			return r
		}
	})
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	if err := os.Mkdir(filepath.Join(h.d.directory, "settings.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	control := h.d.captureVolumes.microphone
	control.SetValue(0)
	h.settle()
	microphone, _ := r.gains()
	if control.Value != 100 || microphone != 1 || !h.d.errorLabel.Visible() ||
		config.CaptureVolume(h.d.settings.Capture.MicrophoneVolume) != 100 {
		t.Fatalf("preference rollback: control=%.0f gain=%.2f setting=%d visible=%v error=%q",
			control.Value, microphone, config.CaptureVolume(h.d.settings.Capture.MicrophoneVolume),
			h.d.errorLabel.Visible(), h.d.errorLabel.Text)
	}
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureIdle && !h.d.busy })
}
