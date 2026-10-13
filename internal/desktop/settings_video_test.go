package desktop

import (
	"slices"
	"testing"

	"ai-talker/internal/config"

	"fyne.io/fyne/v2/test"
)

func TestVideoSettingsOfferThreePresetsInMainCategoryAndPersistChoice(t *testing.T) {
	h := newHarness(t, nil, nil)
	test.Tap(h.d.settingsButton)
	form := h.d.settingsForm
	h.await(func() bool { return !form.loading })
	if form.videoPreset == nil || len(form.videoPreset.Options) != 3 ||
		form.videoPreset.Selected != videoMediumChoice ||
		!slices.Contains(form.videoPreset.Options, videoLowChoice) ||
		!slices.Contains(form.videoPreset.Options, videoHighChoice) {
		t.Fatal("Configurações não oferece os três presets com Média como padrão")
	}
	form.sections.selectID("video")
	if form.sections.selectedID != "video" || form.sections.pages["video"] == nil {
		t.Fatal("Vídeo não tem uma categoria principal navegável")
	}
	form.videoPreset.SetSelected(videoHighChoice)
	test.Tap(form.save)
	h.await(func() bool { return h.d.settingsForm == nil })
	saved, err := config.Load(h.d.directory)
	if err != nil || saved.VideoQuality() != config.VideoHigh {
		t.Fatal("a escolha de qualidade de vídeo não foi persistida", saved.VideoQuality(), err)
	}
	test.Tap(h.d.settingsButton)
	form = h.d.settingsForm
	h.await(func() bool { return !form.loading })
	if form.videoPreset.Selected != videoHighChoice {
		t.Fatal("reabrir as configurações perdeu a qualidade escolhida")
	}
}
