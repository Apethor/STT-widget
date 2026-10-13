package desktop

import (
	"strings"
	"testing"

	"ai-talker/internal/config"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func TestVoiceRecorderShowsItsActualServiceAndIndependentModelChoices(t *testing.T) {
	h := newHarness(t, nil, nil)
	d, doc := h.d, h.d.document
	d.settings.Provider = config.ProviderWhisper
	d.selectAudioMode(doc, voiceModeLive)
	if !strings.Contains(doc.providerButton.hint.text, "AWS Bedrock") ||
		!strings.Contains(doc.qualityBars.hint.text, config.NovaSonic25) {
		t.Fatal("live recorder showed the conventional transcription service/model")
	}
	d.chooseTranscriptionModel()
	popup, ok := d.window.Canvas().Focused().(*widget.PopUpMenu)
	if !ok || len(popup.Items) != 2 {
		t.Fatal("live model control did not offer the two supported conversation models")
	}
	test.Tap(popup.Items[1].(fyne.Tappable))
	popup.Dismiss()
	h.settle()
	if d.settings.Conversation.Model() != config.NovaSonic ||
		d.settings.TranscriptionProvider() != config.ProviderWhisper ||
		!strings.Contains(doc.qualityBars.hint.text, config.NovaSonic) {
		t.Fatal("live model choice changed the conventional engine or did not update its indicator")
	}
	d.selectAudioMode(doc, voiceModeRecording)
	if !strings.Contains(doc.providerButton.hint.text, "Whisper") {
		t.Fatal("conventional service/model did not return after leaving the live mode")
	}
}
