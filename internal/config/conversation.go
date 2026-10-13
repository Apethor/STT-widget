package config

import (
	"errors"
	"strings"
)

// Conversation is independent of transcription and text-processing choices.
// Each tab owns its mode; these preferences apply to future voice sessions.
type Conversation struct {
	ModelID        string `json:"model_id,omitempty"`
	VoiceID        string `json:"voice_id,omitempty"`
	OutputEndpoint string `json:"output_endpoint,omitempty"`
	Volume         *int   `json:"volume,omitempty"`
	Autoplay       *bool  `json:"autoplay,omitempty"`
}

func (c Conversation) Model() string {
	if c.ModelID == "" {
		return "amazon.nova-2-5-sonic"
	}
	return c.ModelID
}

func (c Conversation) Voice() string {
	if c.VoiceID == "" {
		return "carolina"
	}
	return c.VoiceID
}

func (c Conversation) OutputVolume() int {
	if c.Volume == nil {
		return 100
	}
	return *c.Volume
}

func (c Conversation) AutoPlay() bool { return c.Autoplay == nil || *c.Autoplay }

func (c Conversation) Validate() error {
	if c.Model() != "amazon.nova-2-5-sonic" && c.Model() != "amazon.nova-2-sonic-v1:0" {
		return errors.New("selecione um modelo com conversa ao vivo disponível")
	}
	if c.OutputVolume() < 0 || c.OutputVolume() > 200 {
		return errors.New("o volume da resposta deve ficar entre 0 e 200%")
	}
	if len(c.OutputEndpoint) > 4096 || strings.ContainsRune(c.OutputEndpoint+c.Voice(), '\x00') {
		return errors.New("dispositivo ou voz da conversa inválido")
	}
	return nil
}
