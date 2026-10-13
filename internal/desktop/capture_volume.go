package desktop

import (
	"fmt"
	"math"

	"ai-talker/internal/config"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type captureVolumeControls struct {
	content    fyne.CanvasObject
	button     *actionButton
	rows       fyne.CanvasObject
	popup      *widget.PopUp
	microphone *resetSlider
	playback   *resetSlider
	micValue   *widget.Label
	sysValue   *widget.Label
	micRow     *fyne.Container
	sysRow     *fyne.Container
	syncing    bool
	adjusting  [2]bool
}

// BorderLayout takes padding from the active rendering theme. A row revealed
// later can otherwise have different spacing from the already visible row.
// Both source rows share these immutable column widths and gaps.
type captureVolumeRowLayout struct {
	titleWidth, valueWidth float32
}

func (l captureVolumeRowLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	slider := objects[1].MinSize()
	return fyne.NewSize(
		l.titleWidth+8+slider.Width+l.valueWidth,
		max(objects[0].MinSize().Height, slider.Height, objects[2].MinSize().Height),
	)
}

func (l captureVolumeRowLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	title, slider, value := objects[0], objects[1], objects[2]
	title.Move(fyne.NewPos(0, 0))
	title.Resize(fyne.NewSize(l.titleWidth, size.Height))
	start := l.titleWidth + 4
	slider.Move(fyne.NewPos(start, 0))
	slider.Resize(fyne.NewSize(max(0, size.Width-start-l.valueWidth-4), size.Height))
	value.Move(fyne.NewPos(size.Width-l.valueWidth, 0))
	value.Resize(fyne.NewSize(l.valueWidth, size.Height))
}

func (d *desktop) newCaptureVolumes() *captureVolumeControls {
	controls := &captureVolumeControls{}
	controls.microphone, controls.playback = newResetSlider(0, 200, 100), newResetSlider(0, 200, 100)
	controls.microphone.Step, controls.playback.Step = 1, 1
	controls.micValue, controls.sysValue = widget.NewLabel("100%"), widget.NewLabel("100%")
	labelSize := label("Microfone", 11, mutedColor).MinSize()
	valueWidth := label("200%", 11, mutedColor).MinSize().Width + 8
	columns := captureVolumeRowLayout{titleWidth: labelSize.Width, valueWidth: valueWidth}
	row := func(name string, slider *resetSlider, value *widget.Label) *fyne.Container {
		title := container.NewCenter(label(name, 11, mutedColor))
		value.Alignment = fyne.TextAlignTrailing
		return container.New(columns, title, slider, value)
	}
	controls.micRow = row("Microfone", controls.microphone, controls.micValue)
	controls.sysRow = row("Sistema", controls.playback, controls.sysValue)
	rows := container.New(layout.NewCustomPaddedVBoxLayout(4), controls.micRow, controls.sysRow)
	controls.rows = container.NewThemeOverride(rows, volumeTheme{Theme: d.palette})
	controls.button = d.iconButton("Volume da captura · clicar para ajustar", theme.VolumeUpIcon(), d.openCaptureVolumes)
	controls.content = controls.button
	for _, source := range []bool{false, true} {
		slider, value := controls.microphone, controls.micValue
		if source {
			slider, value = controls.playback, controls.sysValue
		}
		slider.OnChanged = func(volume float64) {
			value.SetText(fmt.Sprintf("%.0f%%", volume))
			if !controls.syncing {
				index := 0
				if source {
					index = 1
				}
				controls.adjusting[index] = true
			}
			if !controls.syncing && d.recorder != nil {
				d.applyCaptureVolumes(d.recorder, d.captureVolumeSettings())
			}
			if !controls.syncing {
				d.syncCaptureVolumeButton()
			}
		}
		slider.OnChangeEnded = func(volume float64) {
			if controls.syncing {
				return
			}
			index := 0
			if source {
				index = 1
			}
			controls.adjusting[index] = false
			settings := cloneSettings(d.settings)
			selected := int(math.Round(volume))
			if source {
				settings.Capture.PlaybackVolume = &selected
			} else {
				settings.Capture.MicrophoneVolume = &selected
			}
			d.persistPreferences(settings, func(err error) {
				if err != nil {
					d.showError("Não foi possível salvar o volume: "+err.Error(), "")
				}
			})
		}
	}
	return controls
}

func (d *desktop) openCaptureVolumes() {
	controls := d.captureVolumes
	if controls == nil || d.closing || d.capture == captureStopping {
		return
	}
	if controls.popup != nil && controls.popup.Visible() {
		controls.popup.Hide()
		return
	}
	d.hints.hide()
	if controls.popup == nil {
		controls.popup = widget.NewPopUp(container.NewVBox(
			settingLabel(uiText("Volumes da captura")),
			controls.rows,
		), d.window.Canvas())
	}
	d.positionCaptureVolumes()
}

func (d *desktop) positionCaptureVolumes() {
	controls := d.captureVolumes
	width := min(float32(300), d.window.Canvas().Size().Width-24)
	controls.popup.Resize(fyne.NewSize(width, controls.popup.Content.MinSize().Height))
	d.positionShortcut(controls.popup, controls.button)
}

func (d *desktop) syncCaptureVolumeButton() {
	controls := d.captureVolumes
	mode := d.effectiveCaptureSourceMode()
	mic, sys := controls.microphone.Value, controls.playback.Value
	hint := fmt.Sprintf("Volume do microfone · %.0f%% · clicar para ajustar", mic)
	muted := mic == 0
	if mode == config.CaptureSystem {
		hint = fmt.Sprintf("Volume do sistema · %.0f%% · clicar para ajustar", sys)
		muted = sys == 0
	} else if mode == config.CaptureTeam {
		hint = fmt.Sprintf("Volumes da captura · microfone %.0f%% · sistema %.0f%% · clicar para ajustar", mic, sys)
		muted = mic == 0 && sys == 0
	}
	icon := theme.VolumeUpIcon()
	if muted {
		icon = theme.VolumeMuteIcon()
	}
	controls.button.SetIcon(icon)
	controls.button.setHint(hint)
}

func (d *desktop) syncCaptureVolumes() {
	controls := d.captureVolumes
	if controls == nil {
		return
	}
	controls.syncing = true
	if !controls.adjusting[0] {
		controls.microphone.SetValue(float64(config.CaptureVolume(d.settings.Capture.MicrophoneVolume)))
	}
	if !controls.adjusting[1] {
		controls.playback.SetValue(float64(config.CaptureVolume(d.settings.Capture.PlaybackVolume)))
	}
	controls.syncing = false
	mode := d.effectiveCaptureSourceMode()
	controls.micRow.Hide()
	controls.sysRow.Hide()
	if mode != config.CaptureSystem {
		controls.micRow.Show()
	}
	if mode != config.CaptureMicrophone {
		controls.sysRow.Show()
	}
	setEnabled(controls.microphone, !d.closing && d.capture != captureStopping)
	setEnabled(controls.playback, !d.closing && d.capture != captureStopping)
	setEnabled(controls.button, !d.closing && d.capture != captureStopping)
	controls.content.Refresh()
	d.syncCaptureVolumeButton()
	if controls.popup != nil && controls.popup.Visible() {
		if d.closing || d.capture == captureStopping {
			controls.popup.Hide()
		} else {
			controls.rows.Refresh()
			d.positionCaptureVolumes()
		}
	}
	if d.recorder != nil {
		d.applyCaptureVolumes(d.recorder, d.captureVolumeSettings())
	}
}

func (d *desktop) captureVolumeSettings() config.Settings {
	settings := cloneSettings(d.settings)
	controls := d.captureVolumes
	if controls.adjusting[0] {
		value := int(math.Round(controls.microphone.Value))
		settings.Capture.MicrophoneVolume = &value
	}
	if controls.adjusting[1] {
		value := int(math.Round(controls.playback.Value))
		settings.Capture.PlaybackVolume = &value
	}
	return settings
}

func (d *desktop) applyCaptureVolumes(r recording, settings config.Settings) {
	microphone := float64(config.CaptureVolume(settings.Capture.MicrophoneVolume)) / 100
	playback := float64(config.CaptureVolume(settings.Capture.PlaybackVolume)) / 100
	if team, ok := r.(interface{ SetGains(float64, float64) }); ok {
		team.SetGains(microphone, playback)
	} else if single, ok := r.(interface{ SetGain(float64) }); ok {
		mode := settings.Capture.SourceMode()
		if d.captureOrigin != nil {
			mode = d.captureOrigin.settings.Capture.SourceMode()
		}
		if mode == config.CaptureSystem {
			single.SetGain(playback)
		} else {
			single.SetGain(microphone)
		}
	}
}
