package config

import (
	"errors"
	"strings"
)

const (
	ProviderBedrock = "bedrock"
	ProviderWhisper = "whisper"
	ProviderAPI     = "api"
)

// Whisper uses whisper.cpp's multilingual GGML models. Paths may point to an
// existing installation; empty paths select the app's managed downloads.
type Whisper struct {
	Model      string `json:"model"`
	BinaryPath string `json:"binary_path,omitempty"`
	ModelPath  string `json:"model_path,omitempty"`
	Language   string `json:"language"`
	Threads    int    `json:"threads"`
	BeamSize   int    `json:"beam_size"`
}

func DefaultWhisper() Whisper {
	return Whisper{Model: "base", Language: "auto", BeamSize: 5}
}

func (w Whisper) Validate() error {
	switch w.Model {
	case "tiny", "tiny-q5_1", "base", "base-q5_1", "small", "small-q5_1", "medium",
		"large-v3", "large-v3-q8_0", "large-v3-turbo", "large-v3-turbo-q5_0":
	case "custom":
		if strings.TrimSpace(w.ModelPath) == "" {
			return errors.New("selecione o arquivo GGML do modelo Whisper")
		}
	default:
		return errors.New("selecione um modelo Whisper válido")
	}
	switch w.Language {
	case "auto", "pt", "en", "es", "fr", "de", "it", "ja", "zh":
	default:
		return errors.New("selecione um idioma válido para o Whisper")
	}
	if w.Threads < 0 || w.Threads > 64 {
		return errors.New("use de zero a 64 threads; zero escolhe automaticamente")
	}
	if w.BeamSize < 1 || w.BeamSize > 10 {
		return errors.New("use de uma a dez alternativas na busca do Whisper")
	}
	if strings.ContainsAny(w.BinaryPath+w.ModelPath, "\x00\r\n") {
		return errors.New("informe caminhos de arquivo válidos para o Whisper")
	}
	return nil
}

func (s Settings) TranscriptionProvider() string {
	if s.Provider == ProviderAPI {
		return ProviderAPI
	}
	if s.Provider == ProviderWhisper {
		return ProviderWhisper
	}
	return ProviderBedrock
}
