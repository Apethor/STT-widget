package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ai-talker/internal/config"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
)

func TestPlaybackVolumeOpensOnlyOnClickAndPersistsIndependentOutputGain(t *testing.T) {
	player := newFakeAudioPlayer()
	h := loadedPlaybackHarness(t, player)
	if !h.d.outputVolumeButton.Visible() || h.d.outputVolumeForm != nil {
		t.Fatal("o controle discreto não ficou disponível ou exibiu a barra permanentemente")
	}
	mic, others, output := 45, 130, 70
	h.d.settings.Capture.MicrophoneVolume, h.d.settings.Capture.PlaybackVolume = &mic, &others
	h.d.settings.OutputVolume = &output
	test.Tap(h.d.pauseButton)
	waitValue(t, player.started)
	if player.outputVolume() != 70 {
		t.Fatal("Play não começou com o volume salvo")
	}
	test.Tap(h.d.outputVolumeButton)
	form := h.d.outputVolumeForm
	if form == nil || !form.popup.Visible() || form.slider.Value != 70 || form.popup.Size().Width > 250 {
		t.Fatal("o botão não abriu uma barra compacta com o volume correto")
	}
	assertPopupInCanvas(t, h, form.popup)
	form.slider.Value = 32
	form.slider.OnChanged(32)
	h.d.tick()
	if player.outputVolume() != 32 || form.slider.Value != 32 || form.value.Text != "32%" {
		t.Fatal("ajustar a barra não alterou o som durante a reprodução")
	}
	test.Tap(h.d.pauseButton)
	form.slider.OnChangeEnded(32)
	h.await(func() bool {
		saved, err := config.Load(h.d.directory)
		return err == nil && config.CaptureVolume(saved.OutputVolume) == 32
	})
	h.drain()
	saved, err := config.Load(h.d.directory)
	if err != nil || config.CaptureVolume(saved.OutputVolume) != 32 ||
		config.CaptureVolume(saved.Capture.MicrophoneVolume) != mic ||
		config.CaptureVolume(saved.Capture.PlaybackVolume) != others || !player.Snapshot().Paused {
		t.Fatal("volume de saída alterou a captura ou retomou a reprodução", saved, err)
	}
	test.Tap(h.d.outputVolumeButton)
	if form.popup.Visible() {
		t.Fatal("o segundo clique não fechou a barra de saída")
	}
	h.d.stopPlaybackAction()
	h.settle()
}

func TestPlaybackOutputGainAboveOneHundredIsAvailableAndPersisted(t *testing.T) {
	player := newFakeAudioPlayer()
	h := loadedPlaybackHarness(t, player)
	test.Tap(h.d.pauseButton)
	waitValue(t, player.started)
	test.Tap(h.d.outputVolumeButton)
	form := h.d.outputVolumeForm
	if form == nil || form.slider.Max != 200 {
		t.Fatal("o controle não oferece ganho até 200%")
	}
	form.slider.SetValue(140)
	form.slider.OnChanged(140)
	if player.outputVolume() != 140 || form.value.Text != "140%" {
		t.Fatal("o ganho acima de 100% não foi aplicado à reprodução")
	}
	form.slider.OnChangeEnded(140)
	h.await(func() bool {
		saved, err := config.Load(h.d.directory)
		return err == nil && config.CaptureVolume(saved.OutputVolume) == 140
	})
	h.d.stopPlaybackAction()
	h.settle()
}

func TestPlaybackVolumeFailedSaveRestoresSoundAndKeepsPopupUsable(t *testing.T) {
	player := newFakeAudioPlayer()
	h := loadedPlaybackHarness(t, player)
	test.Tap(h.d.pauseButton)
	waitValue(t, player.started)
	if err := os.Mkdir(filepath.Join(h.d.directory, "settings.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	test.Tap(h.d.outputVolumeButton)
	form := h.d.outputVolumeForm
	form.slider.SetValue(0)
	h.await(func() bool { return h.d.errorLabel.Visible() })
	if !form.popup.Visible() || form.slider.Value != 100 || player.outputVolume() != 100 ||
		h.d.outputVolumeButton.Icon != theme.VolumeUpIcon() || !strings.Contains(h.d.errorLabel.Text, "volume") {
		t.Fatal("a falha de salvar deixou o volume falso ou fechou o controle")
	}
	h.d.stopPlaybackAction()
	h.settle()
	h.d.newAudio()
	if form.popup.Visible() || h.d.outputVolumeButton.Visible() {
		t.Fatal("Novo áudio manteve a barra de saída sem arquivo carregado")
	}
}

func TestPlaybackVolumeThumbDoubleClickRestoresOutputWithoutChangingCapture(t *testing.T) {
	h := loadedPlaybackHarness(t, newFakeAudioPlayer())
	test.Tap(h.d.outputVolumeButton)
	form := h.d.outputVolumeForm
	form.slider.SetValue(50)
	h.settle()
	thumbDiameter := h.d.palette.Size(theme.SizeNameInlineIcon) - 4
	padding := thumbDiameter/2 + h.d.palette.Size(theme.SizeNameInnerPadding) - 1.5
	x := padding + (form.slider.Size().Width-padding*2)*0.25
	form.slider.DoubleTapped(&fyne.PointEvent{
		Position: fyne.NewPos(x, form.slider.Size().Height/2),
	})
	h.settle()
	saved, err := config.Load(h.d.directory)
	if err != nil || form.slider.Value != 100 || config.CaptureVolume(saved.OutputVolume) != 100 ||
		config.CaptureVolume(saved.Capture.MicrophoneVolume) != 100 ||
		config.CaptureVolume(saved.Capture.PlaybackVolume) != 100 {
		t.Fatal("o duplo clique não restaurou o volume de reprodução", form.slider.Value, err)
	}
}
