package desktop

import (
	"fmt"
	"math"

	"ai-talker/internal/config"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type playbackVolumeForm struct {
	popup     *widget.PopUp
	slider    *resetSlider
	value     *widget.Label
	syncing   bool
	adjusting bool
}

func (d *desktop) openPlaybackVolume() {
	if form := d.outputVolumeForm; form != nil && form.popup.Visible() {
		form.popup.Hide()
		return
	}
	if d.capture != captureIdle || d.lastAudio == "" {
		return
	}
	form := &playbackVolumeForm{slider: newResetSlider(0, 200, 100), value: widget.NewLabel("")}
	form.slider.Step = 1
	form.slider.SetValue(float64(config.CaptureVolume(d.settings.OutputVolume)))
	form.value.SetText(fmt.Sprintf("%.0f%%", form.slider.Value))
	width := min(float32(240), d.window.Canvas().Size().Width-32)
	content := container.NewVBox(
		settingLabel(uiText("Volume da reprodução")),
		d.smallText(widget.NewLabel("Acima de 100%, o ganho usa um limitador suave.")),
		container.NewThemeOverride(
			container.NewBorder(nil, nil, nil, form.value, form.slider),
			volumeTheme{Theme: d.palette},
		),
	)
	form.popup = widget.NewPopUp(content, d.window.Canvas())
	d.outputVolumeForm = form
	form.slider.OnChanged = func(volume float64) {
		form.value.SetText(fmt.Sprintf("%.0f%%", volume))
		if form.syncing {
			return
		}
		form.adjusting = true
		if session := d.playback; session != nil {
			session.player.SetVolume(int(math.Round(volume)))
		}
		d.syncPlaybackVolume()
	}
	form.slider.OnChangeEnded = func(volume float64) {
		if form.syncing {
			return
		}
		form.adjusting = false
		updated := cloneSettings(d.settings)
		value := int(math.Round(volume))
		updated.OutputVolume = &value
		d.persistPreferences(updated, func(err error) {
			d.syncPlaybackVolume()
			if err != nil {
				d.showError("Não foi possível salvar o volume de reprodução: "+err.Error(), "")
			}
		})
	}
	form.popup.Resize(fyne.NewSize(width, content.MinSize().Height))
	d.positionShortcut(form.popup, d.outputVolumeButton)
}

func (d *desktop) syncPlaybackVolume() {
	if d.outputVolumeButton == nil {
		return
	}
	volume := config.CaptureVolume(d.settings.OutputVolume)
	form := d.outputVolumeForm
	if form != nil && form.adjusting {
		volume = int(math.Round(form.slider.Value))
	} else if form != nil {
		form.syncing = true
		form.slider.SetValue(float64(volume))
		form.syncing = false
	}
	icon := theme.VolumeUpIcon()
	if volume == 0 {
		icon = theme.VolumeMuteIcon()
	}
	d.outputVolumeButton.SetIcon(icon)
	d.outputVolumeButton.setHint(fmt.Sprintf("Volume da reprodução · %d%% · clicar para ajustar", volume))
	if session := d.playback; session != nil {
		session.player.SetVolume(volume)
	}
	setEnabled(d.outputVolumeButton, d.capture == captureIdle && d.lastAudio != "" && !d.closing)
	if form != nil && form.popup.Visible() && (d.capture != captureIdle || d.lastAudio == "") {
		form.popup.Hide()
	}
}
