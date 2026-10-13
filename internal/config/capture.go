package config

import (
	"errors"
	"strings"
	"unicode/utf8"
)

const (
	CaptureMicrophone = "microphone"
	CaptureSystem     = "system"
	CaptureTeam       = "team"
)

type Capture struct {
	Mode               string `json:"mode"`
	Stream             bool   `json:"stream"`
	MicrophoneEndpoint string `json:"microphone_endpoint,omitempty"`
	PlaybackEndpoint   string `json:"playback_endpoint,omitempty"`
	MicrophoneVolume   *int   `json:"microphone_volume,omitempty"`
	PlaybackVolume     *int   `json:"playback_volume,omitempty"`
}

func DefaultCapture() Capture { return Capture{Mode: CaptureMicrophone} }

func (c Capture) SourceMode() string {
	if c.Mode == "" {
		return CaptureMicrophone
	}
	return c.Mode
}

func (c Capture) Validate() error {
	switch c.SourceMode() {
	case CaptureMicrophone, CaptureSystem, CaptureTeam:
	default:
		return errors.New("selecione Microfone, Áudio do sistema ou Equipe")
	}
	for _, volume := range []*int{c.MicrophoneVolume, c.PlaybackVolume} {
		if volume != nil && (*volume < 0 || *volume > 200) {
			return errors.New("o volume de gravação deve ficar entre 0 e 200%")
		}
	}
	for _, endpoint := range []string{c.MicrophoneEndpoint, c.PlaybackEndpoint} {
		if !utf8.ValidString(endpoint) || len(endpoint) > 4096 || strings.ContainsRune(endpoint, '\x00') {
			return errors.New("identificador de dispositivo de áudio inválido")
		}
	}
	return nil
}

func CaptureVolume(volume *int) int {
	if volume == nil {
		return 100
	}
	return *volume
}
