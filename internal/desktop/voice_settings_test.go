package desktop

import (
	"testing"

	"ai-talker/internal/realtime"

	"fyne.io/fyne/v2/widget"
)

func TestVoiceSettingsReopenKeepsOnlyTheCurrentModeSelectorSynchronized(t *testing.T) {
	h := newHarness(t, nil, nil)
	doc := h.d.document
	var previous *widget.Select
	for _, selected := range []string{voiceModeRecording, voiceModeLive, voiceModeStream} {
		h.d.openSettings()
		form := h.d.settingsForm
		h.await(func() bool { return !form.loading && !form.profilesLoading && !form.cleanup.busy })
		if doc.voice == nil || len(doc.voice.modes) != 1 {
			t.Fatal("reopening settings retained multiple audio mode selectors")
		}
		current := doc.voice.modes[0]
		previousSelection := ""
		if previous != nil {
			if current == previous || previous.OnChanged != nil {
				t.Fatal("reopened settings retained the previous selector callback")
			}
			previousSelection = previous.Selected
		}
		setUISelected(current, audioModeName(selected))
		if h.d.documentAudioMode(doc) != selected {
			t.Fatalf("settings selector failed to select audio mode %s", selected)
		}
		fromRecorder := voiceModeRecording
		if selected == fromRecorder {
			fromRecorder = voiceModeLive
		}
		h.d.selectAudioMode(doc, fromRecorder)
		if uiSelected(current) != audioModeName(fromRecorder) {
			t.Fatal("current settings selector did not follow the recorder audio mode")
		}
		if previous != nil && previous.Selected != previousSelection {
			t.Fatal("mode synchronization still updated the closed settings selector")
		}
		form.sections.selectID("audio/conversation")
		if form.sections.selectedID != "audio/conversation" || form.conversationModel == nil ||
			len(form.conversationModel.Options) != 2 {
			t.Fatal("conversation model settings are not available in their own audio page")
		}
		setUISelected(form.conversationModel, realtime.Nova2)
		if h.d.settings.Conversation.Model() != realtime.Nova2 ||
			h.d.documentAudioMode(doc) != fromRecorder {
			t.Fatal("conversation model selection changed the audio mode or did not apply")
		}
		setUISelected(form.conversationModel, realtime.Nova25)
		form.sections.selectID("audio")
		if form.sections.selectedID != "audio" || uiSelected(current) != audioModeName(fromRecorder) {
			t.Fatal("returning to audio settings lost its independent mode selector")
		}
		form.dialog.Hide()
		h.await(func() bool { return h.d.settingsForm == nil })
		previous = current
	}
}
