package config

import "errors"

// Recovery controls file recognition. Zero values preserve the conservative
// default and allow fallback to a Whisper installation already configured.
type Recovery struct {
	Concurrency     int  `json:"concurrency,omitempty"`
	WhisperDisabled bool `json:"whisper_disabled,omitempty"`
}

func (r Recovery) Validate() error {
	if r.Concurrency < 0 || r.Concurrency > 4 {
		return errors.New("use entre uma e quatro transcrições simultâneas")
	}
	return nil
}

func (s Settings) Parallelism() int {
	if s.Recovery.Concurrency == 0 {
		return 2
	}
	return s.Recovery.Concurrency
}

func (s Settings) VoxtralRecovery() bool {
	return s.TranscriptionProvider() == ProviderBedrock && s.CloudModelID() == VoxtralSmall
}

func (s Settings) SupportsSubdivision() bool {
	if s.TranscriptionProvider() != ProviderBedrock {
		return false
	}
	model, _ := CloudModelByID(s.CloudModelID())
	return model.Backend == CloudConverse
}
