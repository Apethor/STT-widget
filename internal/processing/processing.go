// Package processing describes the optional text processing step after transcription.
package processing

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"ai-talker/internal/apiconfig"
)

const (
	ModeTeams = "teams"
	ModeFree  = "free"
	ModeMerge = "merge"

	DefaultModelID       = "global.openai.gpt-6-luna"
	MaxTextRunes         = 100000
	MaxPromptRunes       = 12000
	MaxConversationTurns = 20
	MaxConversationRunes = 240000
	DefaultWebSearches   = 20

	OutputMaxTokens           = "max_tokens"
	OutputMaxCompletionTokens = "max_completion_tokens"
)

// DefaultTeamsPrompt treats dictated writing instructions as part of the request.
const DefaultTeamsPrompt = `Você recebe, na mensagem do usuário, um texto vindo de uma transcrição de voz. Ele pode conter erros de reconhecimento, repetições, hesitações e frases próprias da fala.

Seu objetivo é preparar uma mensagem clara, bem escrita e profissional, pronta para copiar e enviar no Microsoft Teams.

O texto pode misturar o conteúdo da mensagem com instruções ditadas pelo usuário, como destinatário, tom, idioma, extensão, formato e o que deve ser dito. Siga essas orientações de redação e incorpore-as à mensagem final; não as copie como se fossem parte da mensagem. Por exemplo, "quero falar para o Mendes num tom cordial que..." indica o destinatário, o tom e o conteúdo.

Preserve a intenção, os fatos, os nomes, os números e os compromissos informados. Corrija erros evidentes de transcrição e retire vícios de fala sem inventar informações ou completar lacunas com fatos novos. Prefira um tom profissional e natural, sem formalidade excessiva, salvo orientação diferente no texto.

Responda somente com a mensagem pronta para enviar, sem explicação, título, comentários sobre a transcrição ou aspas envolvendo a resposta. Não acrescente assinatura, campos como "[Seu Nome]" ou pedidos de retorno que não foram ditados. Use o idioma do texto, a menos que o usuário peça outro.`

// Options are remembered independently from audio transcription preferences.
type Options struct {
	Provider                  string               `json:"provider,omitempty"`
	API                       apiconfig.Connection `json:"api,omitempty"`
	ProfileID                 string               `json:"profile_id,omitempty"`
	ModelID                   string               `json:"model_id"`
	Mode                      string               `json:"mode"`
	TeamsPrompt               string               `json:"teams_prompt"`
	ReasoningEffort           string               `json:"reasoning_effort,omitempty"`
	OutputTokenParameter      string               `json:"output_token_parameter,omitempty"`
	WebSearchDisabled         bool                 `json:"web_search_disabled,omitempty"`
	MergeModelID              string               `json:"merge_model_id,omitempty"`
	MergeReasoning            string               `json:"merge_reasoning,omitempty"`
	MergeOutputTokenParameter string               `json:"merge_output_token_parameter,omitempty"`
	MergePrompt               string               `json:"merge_prompt,omitempty"`
	MergeNeedsReview          bool                 `json:"merge_needs_review,omitempty"`
	InstructionName           string               `json:"instruction_name,omitempty"`
	InstructionPrompt         string               `json:"instruction_prompt,omitempty"`
}

func Default() Options {
	return Options{ModelID: DefaultModelID, Mode: ModeTeams, TeamsPrompt: DefaultTeamsPrompt}
}

func (o Options) Validate() error {
	if o.Provider != "" && o.Provider != "bedrock" && o.Provider != "api" {
		return errors.New("selecione AWS ou API para processar texto")
	}
	if o.Provider == "api" {
		if o.API.Provider == apiconfig.AzureSpeech {
			return errors.New("Azure Speech é uma conexão de transcrição; escolha uma conexão Azure OpenAI/Foundry para processar texto")
		}
		if err := o.API.Validate(); err != nil {
			return err
		}
	}
	for _, parameter := range []string{o.OutputTokenParameter, o.MergeOutputTokenParameter} {
		if parameter != "" && parameter != OutputMaxTokens && parameter != OutputMaxCompletionTokens {
			return errors.New("selecione Automático, max_tokens ou max_completion_tokens para o limite de saída da API")
		}
	}
	model := strings.TrimSpace(o.ModelID)
	if model == "" || len(model) > 2048 || strings.ContainsAny(model, "\x00\r\n\t ") {
		return errors.New("informe um ID válido do modelo de texto (ou ARN no Bedrock)")
	}
	if o.Mode != ModeTeams && o.Mode != ModeFree && o.Mode != ModeMerge && !CustomMode(o.Mode) {
		return errors.New("selecione um modo de processamento válido")
	}
	if !utf8.ValidString(o.TeamsPrompt) || utf8.RuneCountInString(o.TeamsPrompt) > MaxPromptRunes {
		return errors.New("a instrução deve ter no máximo 12.000 caracteres válidos")
	}
	if (o.Mode == ModeTeams || o.Mode == ModeMerge) && strings.TrimSpace(o.TeamsPrompt) == "" {
		return errors.New("informe a instrução de processamento")
	}
	if CustomMode(o.Mode) && (!validPrompt(o.InstructionPrompt) || strings.TrimSpace(o.InstructionName) == "") {
		return errors.New("escolha uma instrução personalizada válida")
	}
	if o.ReasoningEffort != "" {
		if o.Provider != "api" && !SupportsReasoning(model) {
			return errors.New("use o raciocínio Automático para este modelo")
		}
		switch o.ReasoningEffort {
		case "low", "medium", "high":
		default:
			return errors.New("selecione o raciocínio Automático, Baixo, Médio ou Alto")
		}
	}
	return nil
}

// SystemPrompt uses the instruction frozen with this request. Free mode has
// none; a custom definition never falls back to the Teams instruction.
func (o Options) SystemPrompt() string {
	if CustomMode(o.Mode) {
		return o.InstructionPrompt
	}
	if o.Mode == ModeFree {
		return ""
	}
	return o.TeamsPrompt
}

type Profile struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Options Options `json:"options"`
}

type Request struct {
	Text           string
	Options        Options
	History        []Turn
	OnProgress     func(string)
	WebSearchLimit int
}

func (r Request) SearchLimit() int {
	if r.WebSearchLimit == 0 {
		return DefaultWebSearches
	}
	return r.WebSearchLimit
}

// ContextWindow preserves the stored history and returns only a bounded tail
// for the next request, leaving room for its exact current editor text.
func (r Request) ContextWindow() (Request, bool) {
	remaining := MaxConversationRunes - utf8.RuneCountInString(r.Text)
	start := len(r.History)
	for start > 0 && len(r.History)-start < MaxConversationTurns-1 {
		turn := r.History[start-1]
		size := utf8.RuneCountInString(turn.Prompt) + utf8.RuneCountInString(turn.Response)
		if size > remaining {
			break
		}
		remaining -= size
		start--
	}
	reduced := start > 0
	r.History = r.History[start:]
	return r, reduced
}

// Turn contains one completed user request and assistant response. Tool calls
// are scoped to their original request; only their final cited text is retained.
type Turn struct {
	Prompt   string `json:"prompt"`
	Response string `json:"response"`
}

// Web grounding can take longer than a text-only Converse call. Both remain
// bounded and inherit the user's cancellation.
func (o Options) RequestTimeout() time.Duration {
	if !o.WebSearchDisabled && o.Mode != ModeMerge {
		return 5 * time.Minute
	}
	return 2 * time.Minute
}

func (r Request) Validate() error {
	if r.SearchLimit() < 1 || r.SearchLimit() > 100 {
		return errors.New("use entre 1 e 100 buscas por pedido")
	}
	if err := r.Options.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(r.Text) == "" {
		return errors.New("escreva ou transcreva um texto nesta aba antes de processar")
	}
	if !utf8.ValidString(r.Text) || utf8.RuneCountInString(r.Text) > MaxTextRunes {
		return errors.New("o texto deve ter no máximo 100.000 caracteres válidos")
	}
	if len(r.History) >= MaxConversationTurns {
		return errors.New("a conversa chegou ao limite de 20 respostas; use Processar para iniciar outra conversa")
	}
	total := utf8.RuneCountInString(r.Text)
	for _, turn := range r.History {
		if strings.TrimSpace(turn.Prompt) == "" || strings.TrimSpace(turn.Response) == "" ||
			!utf8.ValidString(turn.Prompt) || !utf8.ValidString(turn.Response) {
			return errors.New("a conversa contém um pedido ou uma resposta inválida; use Processar para iniciar outra conversa")
		}
		total += utf8.RuneCountInString(turn.Prompt) + utf8.RuneCountInString(turn.Response)
	}
	if total > MaxConversationRunes {
		return errors.New("a conversa excede 240.000 caracteres; use Processar para iniciar outra conversa")
	}
	return nil
}

type Result struct {
	Text           string         `json:"text"`
	ModelID        string         `json:"model_id"`
	Mode           string         `json:"mode"`
	Usage          map[string]any `json:"usage"`
	ElapsedSeconds float64        `json:"elapsed_seconds"`
	Sources        []Source       `json:"sources,omitempty"`
}

// Source keeps the citations used by a web-assisted response with its text.
type Source struct {
	Title string `json:"title,omitempty"`
	URL   string `json:"url"`
}

type Model struct {
	Name string
	ID   string
}

// Models starts with Kynera Hub's catalog and appends three Bedrock candidates
// selected for short text tasks. IDs are copied explicitly, without a dependency
// on the parallel project's files.
func Models() []Model {
	return []Model{
		{Name: "Claude Haiku 4.5 (Global)", ID: "global.anthropic.claude-haiku-4-5-20251001-v1:0"},
		{Name: "Claude Sonnet 5.5 (Global)", ID: "global.anthropic.claude-sonnet-5-5"},
		{Name: "Kimi K3 (Global)", ID: "global.moonshotai.kimi-k3"},
		{Name: "GPT-6.1 Sol (Global)", ID: "global.openai.gpt-6.1-sol"},
		{Name: "GPT-6 Luna (Global)", ID: DefaultModelID},
		{Name: "Grok 4.7 (Global)", ID: "global.xai.grok-4.7"},
		{Name: "GLM 5", ID: "zai.glm-5"},
		{Name: "Nova 2 Lite (Global)", ID: "global.amazon.nova-2-lite-v1:0"},
		{Name: "Qwen3 Next 80B A3B", ID: "qwen.qwen3-next-80b-a3b"},
		{Name: "gpt-oss-120b", ID: "openai.gpt-oss-120b-1:0"},
	}
}

// Other families have different reasoning schemas. Automatic sends no extra
// inference fields, preserving the provider default and custom model support.
func SupportsReasoning(modelID string) bool {
	return strings.Contains(modelID, "openai.gpt-6") ||
		strings.Contains(modelID, "openai.gpt-5.6") ||
		strings.Contains(modelID, "openai.gpt-oss-")
}
