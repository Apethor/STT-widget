package processing

import "errors"

const DefaultMergePrompt = `Você recebe uma lista ordenada de textos de abas de transcrição. Cada item informa o nome da aba e seu texto. A fala pode conter hesitações, repetições e erros de reconhecimento.

Combine todos os textos em uma única versão clara e coerente. Preserve as informações, nomes, números, compromissos e a intenção de cada trecho. Mantenha a ordem das ideias quando ela fizer sentido, retire repetições e conecte os trechos sem inventar fatos. Siga orientações ditadas de redação, destinatário, tom, idioma e formato. Se houver contradições ou lacunas, não escolha nem preencha fatos por conta própria.

Retorne somente o texto combinado, pronto para editar ou copiar, sem comentários sobre as abas, sem título explicativo e sem mencionar este processamento.`

type MergeOptions struct {
	ModelID         string `json:"model_id"`
	Prompt          string `json:"prompt"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

func DefaultMerge() MergeOptions {
	return MergeOptions{
		ModelID: "global.openai.gpt-6.1-sol", Prompt: DefaultMergePrompt,
	}
}

func (o MergeOptions) Options() Options {
	return Options{
		ModelID: o.ModelID, Mode: ModeMerge, TeamsPrompt: o.Prompt,
		ReasoningEffort:   o.ReasoningEffort,
		WebSearchDisabled: true,
	}
}

func (o MergeOptions) Validate() error { return o.Options().Validate() }

// WithLegacyMerge preserves the previous global Bedrock configuration when
// migrating a tab/profile. API users explicitly choose an API merge model.
func (o Options) WithLegacyMerge(legacy MergeOptions) Options {
	if o.MergeModelID == "" {
		o.MergeModelID, o.MergeReasoning, o.MergePrompt = legacy.ModelID, legacy.ReasoningEffort, legacy.Prompt
		o.MergeNeedsReview = o.Provider == "api"
	}
	return o
}

// ForMerge keeps the tab's provider and endpoint, selects its merge model and
// reasoning, and disables web search for the combined transcript.
func (o Options) ForMerge(legacy MergeOptions) (Options, error) {
	o = o.WithLegacyMerge(legacy)
	if o.MergeNeedsReview {
		return o, errors.New("escolha o modelo de Mesclar em Configurações → LLM para usar o provider atual")
	}
	o.ModelID, o.ReasoningEffort = o.MergeModelID, o.MergeReasoning
	o.OutputTokenParameter = o.MergeOutputTokenParameter
	o.Mode, o.TeamsPrompt, o.WebSearchDisabled = ModeMerge, o.MergePrompt, true
	return o, o.Validate()
}
