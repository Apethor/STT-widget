package desktop

import (
	"errors"
	"fmt"
	"strings"

	"ai-talker/internal/config"
	"ai-talker/internal/recorder"

	"fyne.io/fyne/v2/widget"
)

const unavailableDevice = "Dispositivo indisponível"

func deviceChoices(devices []recorder.Device, endpoint string, legacy int, compact bool) ([]string, map[string]recorder.Device, string, bool) {
	names := []string{defaultMicrophone}
	items := map[string]recorder.Device{defaultMicrophone: {ID: -1}}
	selected := defaultMicrophone
	found := endpoint == "" && legacy < 0
	for index, device := range devices {
		if device.ID < 0 && device.EndpointID == "" {
			continue
		}
		name := fmt.Sprintf("%s (%d)", device.Name, device.ID)
		if compact {
			name = compactDeviceName(device.Name, index+1)
		}
		items[name] = device
		names = append(names, name)
		if endpoint != "" && device.EndpointID == endpoint || endpoint == "" && legacy >= 0 && device.ID == legacy {
			selected, found = name, true
		}
	}
	if !found {
		selected = unavailableDevice
		names = append(names, selected)
		items[selected] = recorder.Device{ID: legacy, EndpointID: endpoint}
	}
	return names, items, selected, !found
}

func setDeviceChoices(selectBox *widget.Select, names []string, selected string) {
	changed := selectBox.OnChanged
	selectBox.OnChanged = nil
	preserved := make([]string, 0, len(names))
	for _, name := range names {
		if name != defaultMicrophone && name != unavailableDevice {
			preserved = append(preserved, name)
		}
	}
	preserveUIChoices(selectBox, preserved)
	selectBox.SetOptions(names)
	setUISelected(selectBox, selected)
	selectBox.OnChanged = changed
}

func (d *desktop) reloadSettingsDevices(form *settingsForm) {
	if d.settingsForm != form || form.devicesLoading {
		return
	}
	micEndpoint, micLegacy := d.settings.Capture.MicrophoneEndpoint, d.settings.Device()
	outEndpoint := d.settings.Capture.PlaybackEndpoint
	if id, exists := form.devices[uiSelected(form.microphone)]; exists && uiSelected(form.microphone) != defaultMicrophone {
		micEndpoint, micLegacy = form.endpoints[uiSelected(form.microphone)], id
	} else if !form.loading {
		micEndpoint, micLegacy = "", -1
	}
	if output, exists := form.playbackChoices[uiSelected(form.playback)]; exists && uiSelected(form.playback) != defaultMicrophone {
		outEndpoint = output.EndpointID
	} else if !form.loading {
		outEndpoint = ""
	}
	form.devicesLoading = true
	form.microphone.Disable()
	form.playback.Disable()
	form.deviceRefresh.Disable()
	var microphones, outputs []recorder.Device
	var microphoneErr, outputErr error
	d.operation(func() error {
		microphones, microphoneErr = d.deps.microphones()
		if d.deps.playbackDevices != nil {
			outputs, outputErr = d.deps.playbackDevices()
		}
		return errors.Join(microphoneErr, outputErr)
	}, func(err error) {
		if d.settingsForm != form {
			return
		}
		names, items, selected, _ := deviceChoices(microphones, micEndpoint, micLegacy, false)
		form.devices, form.endpoints = make(map[string]int), make(map[string]string)
		for name, device := range items {
			form.devices[name], form.endpoints[name] = device.ID, device.EndpointID
		}
		setDeviceChoices(form.microphone, names, selected)
		names, form.playbackChoices, selected, _ = deviceChoices(outputs, outEndpoint, -1, false)
		setDeviceChoices(form.playback, names, selected)
		d.updateDeviceAvailability(microphones, outputs, microphoneErr, outputErr)
		form.devicesLoading = false
		form.loading = form.profilesLoading
		form.microphone.Enable()
		form.playback.Enable()
		form.deviceRefresh.Enable()
		if !form.loading && !form.whisper.busy {
			form.save.Enable()
		}
		if err != nil {
			form.errorLabel.SetText("Não foi possível listar todos os dispositivos: " + shortMessage(err.Error()))
			form.errorLabel.Show()
		} else if strings.HasPrefix(form.errorLabel.Text, "Não foi possível listar todos os dispositivos:") {
			form.errorLabel.Hide()
		}
	})
}

func (d *desktop) updateDeviceAvailability(microphones, outputs []recorder.Device, microphoneErr, outputErr error) {
	if microphoneErr == nil {
		_, _, _, d.unavailableMicrophone = deviceChoices(microphones, d.settings.Capture.MicrophoneEndpoint, d.settings.Device(), true)
	}
	if outputErr == nil {
		_, _, _, d.unavailablePlayback = deviceChoices(outputs, d.settings.Capture.PlaybackEndpoint, -1, true)
	}
}

func (d *desktop) captureDeviceSelectionError() error {
	mode := d.settings.Capture.SourceMode()
	if (mode == config.CaptureMicrophone || mode == config.CaptureTeam) && d.unavailableMicrophone {
		return errors.New("Dispositivo indisponível: escolha outro microfone em Fontes e dispositivos ou Configurações → Áudio antes de gravar.")
	}
	if (mode == config.CaptureSystem || mode == config.CaptureTeam) && d.unavailablePlayback {
		return errors.New("Dispositivo indisponível: escolha outra saída capturada como Sistema antes de gravar.")
	}
	return nil
}
