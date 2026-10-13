package processing

import "ai-talker/internal/apiconfig"

type APIPreset struct {
	Name       string
	ModelID    string
	Connection apiconfig.Connection
}

// Initial choices remain editable and do not imply that a model supports
// audio, reasoning or tools; the corresponding catalogs are independent.
func APIPresets() []APIPreset {
	return []APIPreset{
		{Name: "OpenRouter · GPT-4.1 mini", ModelID: "openai/gpt-4.1-mini", Connection: apiconfig.Connection{Provider: apiconfig.OpenRouter}},
		{Name: "OpenAI · GPT-4.1 mini", ModelID: "gpt-4.1-mini", Connection: apiconfig.Connection{Provider: apiconfig.OpenAI}},
	}
}
