package desktop

import (
	"errors"
	"fmt"
	"strings"

	"ai-talker/internal/config"
	"ai-talker/internal/recorder"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const (
	microphoneChoice = "Microfone"
	systemChoice     = "Sistema"
	teamChoice       = "Equipe"
)

type captureDeviceForm struct {
	popup      *widget.PopUp
	microphone *widget.Select
	playback   *widget.Select
	micRow     *fyne.Container
	outputRow  *fyne.Container
	devices    map[string]recorder.Device
	outputs    map[string]recorder.Device
	message    *widget.Label
	loading    bool
	saving     bool
	refresh    *actionButton
}

func (d *desktop) captureDevicesOpen() bool {
	return d.deviceForm != nil && d.deviceForm.popup.Visible()
}

func captureModeName(mode string) string {
	switch mode {
	case config.CaptureTeam:
		return teamChoice
	case config.CaptureSystem:
		return systemChoice
	default:
		return microphoneChoice
	}
}

func (d *desktop) effectiveCaptureSourceMode() string {
	if d.voiceSession != nil {
		return config.CaptureMicrophone
	}
	if d.captureOrigin != nil && d.capture != captureIdle {
		return d.captureOrigin.settings.Capture.SourceMode()
	}
	if d.documentAudioMode(d.document) == voiceModeLive {
		return config.CaptureMicrophone
	}
	return d.settings.Capture.SourceMode()
}

func (d *desktop) syncCaptureOptions() {
	d.syncCaptureVolumes()
	if d.deviceButton == nil || d.streamButton == nil {
		return
	}
	mode := d.effectiveCaptureSourceMode()
	icon := inputDevicesIcon
	if mode == config.CaptureTeam {
		icon = teamInputIcon
	}
	d.deviceButton.SetIcon(icon)
	d.deviceButton.setHint("Fontes e dispositivos · " + captureModeName(mode))
	d.streamButton.Importance = widget.LowImportance
	name := cloudChoiceName(d.settings.StreamModelID())
	d.streamButton.setHint("Ativar Stream · " + name + " · transcrição durante a gravação")
	local := d.settings.TranscriptionProvider() != config.ProviderBedrock
	if local {
		d.streamButton.setHint("Stream disponível com AWS Cloud · Whisper local e API transcrevem ao parar")
	} else if d.documentAudioMode(d.document) == voiceModeStream {
		d.streamButton.Importance = widget.HighImportance
		d.streamButton.setHint("Stream · " + name + " · clicar para transcrever somente ao parar")
	}
	d.streamButton.Refresh()
	setEnabled(d.deviceButton, true)
	setEnabled(d.streamButton, d.capture == captureIdle)
	if d.captureSelect != nil {
		d.syncingCapture = true
		live := d.voiceSession != nil || d.documentAudioMode(d.document) == voiceModeLive
		d.captureSelect.Options = []string{microphoneChoice, systemChoice, teamChoice}
		if live {
			d.captureSelect.Options = []string{microphoneChoice}
		}
		setUISelected(d.captureSelect, captureModeName(mode))
		d.syncingCapture = false
		if d.capture == captureIdle && !live && (d.deviceForm == nil || !d.deviceForm.loading && !d.deviceForm.saving) {
			d.captureSelect.Enable()
		} else {
			d.captureSelect.Disable()
		}
	}
	if d.status != nil && d.capture == captureIdle && !d.busy &&
		(uiSource(d.status.Text) == "O áudio só é enviado ao concluir." || uiSource(d.status.Text) == "Stream: o texto aparece durante a gravação.") {
		message := "O áudio só é enviado ao concluir."
		if d.documentAudioMode(d.document) == voiceModeStream && !local {
			message = "Stream: o texto aparece durante a gravação."
		}
		d.status.SetText(message)
	}
	if d.document != nil && d.document.voice != nil {
		mode := d.documentAudioMode(d.document)
		hint := uiText(audioModeName(mode))
		if mode == voiceModeStream {
			hint += " · " + name
		}
		d.streamButton.setHint(hint + " · " + uiText("Modos de gravação"))
		d.streamButton.Importance = widget.LowImportance
		if mode != voiceModeRecording {
			d.streamButton.Importance = widget.HighImportance
		}
		d.streamButton.Refresh()
	}
}

func (d *desktop) toggleStream() {
	if d.capture != captureIdle || d.settings.TranscriptionProvider() != config.ProviderBedrock {
		return
	}
	updated := cloneSettings(d.settings)
	updated.Capture.Stream = !updated.Capture.Stream
	d.persistPreferences(updated, func(err error) {
		if err != nil {
			d.showError(err.Error(), "")
		}
	})
}

func (d *desktop) changeCaptureMode(choice string) {
	if d.syncingCapture || d.capture != captureIdle || d.documentAudioMode(d.document) == voiceModeLive {
		return
	}
	form := d.deviceForm
	if form != nil && (form.loading || form.saving) {
		d.syncCaptureOptions()
		return
	}
	updated := cloneSettings(d.settings)
	switch choice {
	case microphoneChoice:
		updated.Capture.Mode = config.CaptureMicrophone
	case systemChoice:
		updated.Capture.Mode = config.CaptureSystem
	case teamChoice:
		updated.Capture.Mode = config.CaptureTeam
	default:
		return
	}
	if updated.Capture.SourceMode() == d.settings.Capture.SourceMode() {
		return
	}
	if d.captureDevicesOpen() {
		form.saving = true
		d.refreshCaptureDeviceForm(form)
	}
	d.persistPreferences(updated, func(err error) {
		if form != nil && d.deviceForm == form {
			form.saving = false
			if err != nil {
				form.message.SetText(shortMessage(err.Error()))
				form.message.Show()
			} else {
				form.message.Hide()
			}
			d.refreshCaptureDeviceForm(form)
		} else if err != nil {
			d.showError(err.Error(), "")
		}
	})
}

func (d *desktop) openCaptureDevices() {
	if d.captureDevicesOpen() {
		d.deviceForm.popup.Hide()
		return
	}
	form := &captureDeviceForm{
		microphone: widget.NewSelect([]string{defaultMicrophone}, nil),
		playback:   widget.NewSelect([]string{defaultMicrophone}, nil),
		devices:    map[string]recorder.Device{defaultMicrophone: {ID: -1}},
		outputs:    map[string]recorder.Device{defaultMicrophone: {ID: -1}},
		message:    widget.NewLabel("Carregando os dispositivos…"), loading: true,
	}
	setUISelected(form.microphone, defaultMicrophone)
	setUISelected(form.playback, defaultMicrophone)
	form.microphone.Disable()
	form.playback.Disable()
	form.message.Wrapping = fyne.TextWrapWord
	width := min(float32(300), d.window.Canvas().Size().Width-32)
	content := container.NewVBox()
	form.refresh = d.iconButton("Atualizar dispositivos", theme.ViewRefreshIcon(), func() { d.reloadCaptureDevices(form) })
	content.Add(container.NewBorder(nil, nil, settingLabel("Dispositivos de áudio"), form.refresh))
	selectRow := func(title string, selectBox *widget.Select) *fyne.Container {
		// Device names must not grow the recorder or the main window.
		return container.NewVBox(settingLabel(title),
			container.NewGridWrap(fyne.NewSize(width-16, selectBox.MinSize().Height), selectBox))
	}
	content.Add(selectRow("Fonte de áudio", d.captureSelect))
	form.micRow = selectRow("Microfone", form.microphone)
	form.outputRow = selectRow("Saída de som", form.playback)
	content.Add(form.micRow)
	content.Add(form.outputRow)
	content.Add(d.smallText(form.message))
	form.popup = widget.NewPopUp(content, d.window.Canvas())
	d.deviceForm = form
	form.popup.Resize(fyne.NewSize(width, content.MinSize().Height))
	d.refreshCaptureDeviceForm(form)
	d.positionShortcut(form.popup, d.deviceButton)
	form.loading = false
	d.reloadCaptureDevices(form)
}

func (d *desktop) reloadCaptureDevices(form *captureDeviceForm) {
	if d.deviceForm != form || form.loading || form.saving {
		return
	}
	settings := cloneSettings(d.settings)
	form.loading = true
	form.message.SetText("Atualizando os dispositivos…")
	form.message.Show()
	d.refreshCaptureDeviceForm(form)
	var microphones, outputs []recorder.Device
	var micErr, outErr error
	d.operation(func() error {
		microphones, micErr = d.deps.microphones()
		if d.deps.playbackDevices != nil {
			outputs, outErr = d.deps.playbackDevices()
		}
		return errors.Join(micErr, outErr)
	}, func(err error) {
		if d.deviceForm != form || !form.popup.Visible() {
			return
		}
		names, devices, selected, _ := deviceChoices(microphones, settings.Capture.MicrophoneEndpoint, settings.Device(), true)
		form.devices = devices
		setDeviceChoices(form.microphone, names, selected)
		names, outputsMap, selected, _ := deviceChoices(outputs, settings.Capture.PlaybackEndpoint, -1, true)
		form.outputs = outputsMap
		setDeviceChoices(form.playback, names, selected)
		d.updateDeviceAvailability(microphones, outputs, micErr, outErr)
		form.loading = false
		form.microphone.OnChanged = func(string) { d.applyCaptureDevice(form, true) }
		form.playback.OnChanged = func(string) { d.applyCaptureDevice(form, false) }
		form.message.Hide()
		if err != nil {
			form.message.SetText("Não foi possível listar todos os dispositivos: " + shortMessage(err.Error()))
			form.message.Show()
		} else if d.unavailableMicrophone || d.unavailablePlayback {
			form.message.SetText("Dispositivo indisponível. Escolha outro antes da próxima gravação.")
			form.message.Show()
		}
		d.refreshCaptureDeviceForm(form)
	})
}

func (d *desktop) refreshCaptureDeviceForm(form *captureDeviceForm) {
	if d.deviceForm != form {
		return
	}
	form.micRow.Show()
	form.outputRow.Show()
	switch d.effectiveCaptureSourceMode() {
	case config.CaptureMicrophone:
		form.outputRow.Hide()
	case config.CaptureSystem:
		form.micRow.Hide()
	}
	enabled := d.capture == captureIdle && !form.loading && !form.saving
	for _, selectBox := range []*widget.Select{d.captureSelect, form.microphone, form.playback} {
		setEnabled(selectBox, enabled)
	}
	if d.documentAudioMode(d.document) == voiceModeLive {
		d.captureSelect.Disable()
	}
	setEnabled(form.refresh, !form.loading && !form.saving)
	form.popup.Refresh()
	form.popup.Resize(fyne.NewSize(form.popup.Size().Width, form.popup.Content.MinSize().Height))
	if form.popup.Visible() {
		d.positionShortcut(form.popup, d.deviceButton)
	}
}

func compactDeviceName(name string, index int) string {
	runes := []rune(strings.TrimSpace(name))
	if len(runes) > 30 {
		name = string(runes[:29]) + "…"
	}
	return fmt.Sprintf("%s (%d)", name, index)
}

func (d *desktop) applyCaptureDevice(form *captureDeviceForm, microphone bool) {
	if d.deviceForm != form || form.loading || form.saving || !form.popup.Visible() || d.capture != captureIdle {
		return
	}
	selectBox, devices := form.playback, form.outputs
	if microphone {
		selectBox, devices = form.microphone, form.devices
	}
	selected := uiSelected(selectBox)
	device, ok := devices[selected]
	if !ok || selected == unavailableDevice {
		form.message.SetText("Selecione dispositivos válidos.")
		form.message.Show()
		return
	}
	updated := cloneSettings(d.settings)
	if microphone {
		updated.Microphone = nil
		if device.ID >= 0 {
			id := device.ID
			updated.Microphone = &id
		}
		updated.Capture.MicrophoneEndpoint = device.EndpointID
	} else {
		updated.Capture.PlaybackEndpoint = device.EndpointID
	}
	previous := selected
	if microphone {
		previous = selectedDevice(form.devices, d.settings.Capture.MicrophoneEndpoint, d.settings.Device())
	} else {
		previous = selectedDevice(form.outputs, d.settings.Capture.PlaybackEndpoint, -1)
	}
	form.saving = true
	d.refreshCaptureDeviceForm(form)
	d.persistPreferences(updated, func(err error) {
		if d.deviceForm != form {
			return
		}
		if err != nil {
			setUISelected(selectBox, previous)
			form.message.SetText(shortMessage(err.Error()))
			form.message.Show()
		} else {
			if microphone {
				d.unavailableMicrophone = false
			} else {
				d.unavailablePlayback = false
			}
			form.message.Hide()
		}
		form.saving = false
		d.refreshCaptureDeviceForm(form)
	})
}

func selectedDevice(devices map[string]recorder.Device, endpoint string, legacy int) string {
	for name, device := range devices {
		if endpoint != "" && device.EndpointID == endpoint || endpoint == "" && device.ID == legacy {
			return name
		}
	}
	return defaultMicrophone
}
