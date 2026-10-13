package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ai-talker/internal/config"
	"ai-talker/internal/recorder"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func chooseRecorderAudioMode(h *testHarness, index int) {
	h.t.Helper()
	test.Tap(h.d.streamButton)
	popup, ok := h.d.window.Canvas().Focused().(*widget.PopUpMenu)
	if !ok || len(popup.Items) != 3 {
		h.t.Fatal("the recorder did not offer its three audio modes")
	}
	test.Tap(popup.Items[index].(fyne.Tappable))
	popup.Dismiss()
	h.settle()
}

func TestRecorderDevicesPersistStableEndpointsAndTeamChoice(t *testing.T) {
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.microphones = func() ([]recorder.Device, error) {
			return []recorder.Device{{ID: 3, Name: "Microfone USB", EndpointID: "mic-usb"}}, nil
		}
		deps.playbackDevices = func() ([]recorder.Device, error) {
			return []recorder.Device{{ID: 100000, Name: "Fones USB", EndpointID: "headphones-usb"}}, nil
		}
	})
	size := h.d.window.Canvas().Size()
	for _, object := range test.LaidOutObjects(h.d.recordPanel) {
		if object == h.d.captureSelect {
			t.Fatal("the recorder permanently displays the source selector")
		}
	}
	test.Tap(h.d.deviceButton)
	form := h.d.deviceForm
	h.await(func() bool { return !form.loading })
	h.d.captureSelect.SetSelected(teamChoice)
	h.settle()
	if !form.popup.Visible() || !form.micRow.Visible() || !form.outputRow.Visible() {
		t.Fatal("choosing Team did not keep both device selectors in the shortcut")
	}
	if form.popup.Size().Width > 310 || form.popup.Size().Height > 320 {
		t.Fatalf("the shortcut became a large settings dialog: %v", form.popup.Size())
	}
	form.microphone.SetSelected(form.microphone.Options[1])
	h.settle()
	form.playback.SetSelected(form.playback.Options[1])
	h.settle()
	form.popup.Hide()
	saved, err := config.Load(h.d.directory)
	if err != nil || saved.Capture.SourceMode() != config.CaptureTeam || saved.Device() != 3 ||
		saved.Capture.MicrophoneEndpoint != "mic-usb" || saved.Capture.PlaybackEndpoint != "headphones-usb" ||
		h.d.window.Canvas().Size() != size {
		t.Fatalf("device selection did not persist or resized the app: %+v, %v", saved, err)
	}
	chooseRecorderAudioMode(h, 1)
	saved, err = config.Load(h.d.directory)
	if err != nil || h.d.documentAudioMode(h.d.document) != voiceModeStream {
		t.Fatal("stream choice was not persisted")
	}
	if h.d.streamButton.Text != "" || h.d.captureDevicesOpen() || h.d.deviceButton.Icon != teamInputIcon {
		t.Fatal("the recorder lost Stream or kept the source selector open")
	}
	h.d.window.Resize(fyne.NewSize(320, 700))
	h.d.window.Content().Refresh()
	if h.d.window.Canvas().Size().Width > 320 ||
		h.d.recordPanel.MinSize().Width > h.d.recordPanel.Size().Width {
		t.Fatalf("recorder controls no longer fit the narrow column: %v", h.d.recordPanel.MinSize())
	}
}

func TestRecorderSourceSelectionFailureKeepsCompactMenuUsable(t *testing.T) {
	h := newHarness(t, nil, nil)
	if err := os.Mkdir(filepath.Join(h.d.directory, "settings.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	test.Tap(h.d.deviceButton)
	form := h.d.deviceForm
	h.await(func() bool { return !form.loading })
	h.d.captureSelect.SetSelected(teamChoice)
	h.settle()
	if h.d.settings.Capture.SourceMode() != config.CaptureMicrophone ||
		h.d.captureSelect.Selected != microphoneChoice || !form.popup.Visible() ||
		!form.message.Visible() || h.d.captureSelect.Disabled() || form.outputRow.Visible() {
		t.Fatal("a failed source save closed the menu or left the wrong source selected")
	}
}

func TestRecorderDeviceSelectionFailureRestoresPreviousChoice(t *testing.T) {
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.microphones = func() ([]recorder.Device, error) {
			return []recorder.Device{{ID: 1, Name: "Microfone USB", EndpointID: "mic-usb"}}, nil
		}
	})
	if err := os.Mkdir(filepath.Join(h.d.directory, "settings.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	test.Tap(h.d.deviceButton)
	form := h.d.deviceForm
	h.await(func() bool { return !form.loading })
	form.microphone.SetSelected(form.microphone.Options[1])
	h.settle()
	if h.d.settings.Device() != -1 || form.microphone.Selected != defaultMicrophone ||
		!form.message.Visible() || form.microphone.Disabled() || !form.popup.Visible() {
		t.Fatal("failed save left a false device selection or an unusable shortcut")
	}
}

func TestRecorderDevicePopupDismissalAllowsRecording(t *testing.T) {
	h := newHarness(t, nil, nil)
	test.Tap(h.d.deviceButton)
	h.await(func() bool { return !h.d.deviceForm.loading })
	h.d.deviceForm.popup.Hide()
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	if !h.d.captureSelect.Disabled() {
		t.Fatal("the source can change during a recording")
	}
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureIdle && !h.d.busy })
}

func TestRecorderStreamUnavailableWithWhisperRetainsBedrockPreference(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.d.settings.Capture.Stream = true
	h.d.settings.Provider = config.ProviderWhisper
	h.d.syncCaptureOptions()
	if h.d.streamButton.Disabled() || h.d.streamButton.Text != "" {
		t.Fatal("the audio-mode menu was unavailable for live conversation")
	}
	chooseRecorderAudioMode(h, 1)
	if h.d.documentAudioMode(h.d.document) != voiceModeRecording {
		t.Fatal("Whisper allowed unsupported incremental transcription")
	}
	h.d.toggleStream()
	if !h.d.settings.Capture.Stream {
		t.Fatal("switching engines lost the saved Bedrock stream preference")
	}
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	if h.d.captureOrigin.settings.Capture.Stream || h.d.document.live != nil {
		t.Fatal("local inference started a stream despite the disabled control")
	}
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureIdle && !h.d.busy })
	h.d.setTranscriptionProvider(config.ProviderBedrock)
	h.settle()
	if h.d.streamButton.Disabled() || !h.d.settings.Capture.Stream {
		t.Fatal("returning to Bedrock did not restore the stream option")
	}
}

func TestRecorderStreamUsesAndShowsTheSelectedQuality(t *testing.T) {
	h, r := streamHarness(t, nil, nil)
	h.d.settings.Quality = "quality"
	h.d.syncDocumentProvider(h.d.document)
	if h.d.qualityBars.levels != 4 || !strings.Contains(h.d.qualityBars.hint.text, "Stream") {
		t.Fatal("stream hid the selected transcription model")
	}
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	if h.d.live.settings.Quality != "quality" || h.d.settings.Quality != "quality" {
		t.Fatal("stream silently changed the selected quality")
	}
	admitStream(h, r, 2)
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureIdle && !h.d.busy })
	chooseRecorderAudioMode(h, 0)
	if h.d.qualityBars.levels != 4 || h.d.settings.Quality != "quality" {
		t.Fatal("leaving Stream did not restore the normal model indication")
	}
}
