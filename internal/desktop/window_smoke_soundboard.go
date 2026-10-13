//go:build windows && windowsmoke

package desktop

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"ai-talker/internal/config"
	"ai-talker/internal/platform"
	"ai-talker/internal/soundboard"
	"ai-talker/internal/store"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
)

type soundboardSmokeState struct {
	Open, Prepared, Playing                   bool
	Title, Language, Status, Error            string
	Scale, CanvasWidth, CanvasHeight          float32
	Effects                                   []smokeNamedControl
	Minimize, Maximize, Close, Add, Stop      smokeRectangle
	Position                                  platform.AuxiliaryGeometry
	SavedPosition                             *soundboard.Position
	LastCommand                               uint64
	LastEffect                                string
	RequestedNS, SubmittedNS                  int64
	RequestToPCMMS, ReportedDeviceAndBufferMS float64
}

// This state is confined to the single smoke application's UI thread. No
// actions or state exporters are included in production builds.
var soundboardSmokeLastCommand uint64
var soundboardSmokeLastError string

func (d *desktop) windowSmokeSoundboard() *soundboardSmokeState {
	state := &soundboardSmokeState{
		Language:    d.settings.InterfaceLocale(),
		LastCommand: soundboardSmokeLastCommand, Error: soundboardSmokeLastError,
	}
	if board := d.soundboard; board != nil {
		state.SavedPosition = board.Position()
		state.Prepared = d.soundEngine != nil
		if state.Prepared {
			for _, effect := range board.Buttons() {
				state.Prepared = state.Prepared && d.soundEngine.Ready(effect.ID)
			}
		}
	}
	if engine := d.soundEngine; engine != nil {
		timing := engine.Timing()
		state.Playing, state.LastEffect = engine.Active(), timing.ID
		if !timing.Requested.IsZero() {
			state.RequestedNS = timing.Requested.UnixNano()
		}
		if !timing.Submitted.IsZero() {
			state.SubmittedNS = timing.Submitted.UnixNano()
			state.RequestToPCMMS = float64(timing.Submitted.Sub(timing.Requested).Microseconds()) / 1000
		}
		state.ReportedDeviceAndBufferMS = float64(timing.DeviceLatency.Microseconds()) / 1000
	}
	window := d.soundboardWindow
	if window == nil {
		return state
	}
	state.Open, state.Title = true, window.Title()
	state.Scale = window.Canvas().Scale()
	state.CanvasWidth, state.CanvasHeight = window.Canvas().Size().Width, window.Canvas().Size().Height
	state.Position, _ = platform.AuxiliaryWindowGeometry(window.Title())
	if d.soundboardStatus != nil {
		state.Status = d.soundboardStatus.Text
	}
	buttons := d.smokeNamedButtons(window.Content())
	for _, effect := range d.soundboard.Buttons() {
		name := soundButtonName(effect)
		for _, button := range buttons {
			if button.Text == name {
				state.Effects = append(state.Effects, button)
			}
		}
	}
	for _, object := range smokeCanvasObjects(window.Content()) {
		switch button := object.(type) {
		case *actionButton:
			switch uiSource(button.hint.text) {
			case "Minimizar janela":
				state.Minimize = d.smokeRectangle(button)
			case "Maximizar ou restaurar janela":
				state.Maximize = d.smokeRectangle(button)
			case "Fechar painel de sons":
				state.Close = d.smokeRectangle(button)
			}
		case *widget.Button:
			switch uiSource(button.Text) {
			case "Adicionar som WAV ou MP3":
				state.Add = d.smokeRectangle(button)
			case "Parar som":
				state.Stop = d.smokeRectangle(button)
			}
		}
	}
	return state
}

func (d *desktop) runSoundboardSmokeActions() {
	var command struct {
		ID       uint64    `json:"id"`
		Action   string    `json:"action"`
		Language string    `json:"language"`
		Size     fyne.Size `json:"size"`
		Capture  string    `json:"capture"`
	}
	path := filepath.Join(d.directory, "soundboard-command.json")
	if err := store.ReadJSON(path, &command); err != nil {
		return
	}
	if err := os.Remove(path); err != nil {
		return
	}
	soundboardSmokeLastCommand, soundboardSmokeLastError = command.ID, ""
	switch command.Action {
	case "open":
		d.openSoundboard()
	case "mute":
		if d.soundboard == nil || d.soundEngine == nil {
			soundboardSmokeLastError = "soundboard is not prepared"
		} else if err := d.soundboard.SetVolume(0); err != nil {
			soundboardSmokeLastError = err.Error()
		} else {
			d.soundEngine.SetVolume(0)
			d.renderSoundboard()
		}
	case "resize":
		if d.soundboardWindow != nil {
			d.soundboardWindow.Resize(command.Size)
		}
	case "language":
		if d.settingsForm == nil {
			d.openSettings()
		}
		if form := d.settingsForm; form != nil {
			form.sections.selectID("visual")
			option := "Português (Brasil)"
			if command.Language == config.InterfaceEnglish {
				option = "Inglês"
			}
			setUISelected(form.interfaceLanguage, option)
		} else {
			soundboardSmokeLastError = "settings are not available"
		}
	case "close":
		d.closeSoundboardWindow(d.soundboardWindow)
	case "capture":
		window := d.soundboardWindow
		if window == nil || filepath.Base(command.Capture) != command.Capture ||
			strings.ContainsAny(command.Capture, "/\\\x00") {
			soundboardSmokeLastError = "invalid soundboard capture"
			return
		}
		file, err := os.Create(filepath.Join(d.directory, command.Capture+".png"))
		if err == nil {
			// This pinned Fyne version nests GL contexts in Canvas.Capture.
			// Use the same single-context painter path as the main smoke.
			painter := reflect.ValueOf(window.Canvas()).MethodByName("Painter").Call(nil)[0].Interface()
			capture := painter.(interface{ Capture(fyne.Canvas) image.Image })
			err = png.Encode(file, capture.Capture(window.Canvas()))
			closeErr := file.Close()
			if err == nil {
				err = closeErr
			}
		}
		if err != nil {
			soundboardSmokeLastError = fmt.Sprint(err)
		}
	}
}
