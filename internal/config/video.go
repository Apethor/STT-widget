package config

import "errors"

const (
	VideoLow    = "low"
	VideoMedium = "medium"
	VideoHigh   = "high"
)

func (s Settings) VideoQuality() string {
	if s.VideoPreset == "" {
		return VideoMedium
	}
	return s.VideoPreset
}

func validateVideoPreset(preset string) error {
	switch preset {
	case "", VideoLow, VideoMedium, VideoHigh:
		return nil
	default:
		return errors.New("selecione qualidade de vídeo Baixa, Média ou Alta")
	}
}
