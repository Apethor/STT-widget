// Package realtime implements a continuous Amazon Nova Sonic speech-to-speech
// session. It does not capture a microphone, play audio, or own exported files.
package realtime

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

const (
	Nova2            = "amazon.nova-2-sonic-v1:0"
	Nova25           = "amazon.nova-2-5-sonic"
	InputSampleRate  = 16000
	OutputSampleRate = 24000
)

var (
	ErrClosed              = errors.New("a conversa ao vivo foi encerrada")
	ErrUnsupportedProvider = errors.New("esta conexão não oferece conversa bidirecional ao vivo comprovada")
)

type Message struct {
	Role string
	Text string
}

// Options are fixed when a session starts. Changing voice requires a new
// session; History restores completed conversation context before live audio.
type Options struct {
	ModelID         string
	VoiceID         string
	SystemPrompt    string
	History         []Message
	InputSampleRate int
	MaxTokens       int
}

type Kind string

const (
	UserText       Kind = "user-text"
	AssistantText  Kind = "assistant-text"
	AssistantAudio Kind = "assistant-audio"
	ResponseEnd    Kind = "response-end"
	Interrupted    Kind = "interrupted"
	SessionEnd     Kind = "session-end"
)

// Event.Text contains the current complete text, rather than a delta. PCM is a
// newly owned mono signed little-endian 16-bit block; the consumer may retain it.
// The callback executes on a session goroutine and must return promptly.
type Event struct {
	Kind         Kind
	CompletionID string
	ContentID    string
	Text         string
	PCM          []byte
	SampleRate   int
	Final        bool
	Interrupted  bool
	Err          error
}

// Response preserves received audio even after interruption or failure.
type Response struct {
	CompletionID string
	UserText     string
	Text         string
	PCM          []byte
	SampleRate   int
	Complete     bool
	Interrupted  bool
}

// Stream is the small SDK contract needed by a speech session.
type Stream interface {
	Send(context.Context, types.InvokeModelWithBidirectionalStreamInput) error
	Events() <-chan types.InvokeModelWithBidirectionalStreamOutput
	Err() error
	Close() error
}

type Voice struct {
	ID     string
	Locale string
}

func Voices(modelID string) []Voice {
	if modelID == Nova25 {
		// The Nova 2 table alone does not establish Nova 2.5 availability.
		// Both Portuguese voices were verified in synthetic live sessions.
		return []Voice{{"carolina", "pt-BR"}, {"leo", "pt-BR"}}
	}
	if modelID != Nova2 {
		return nil
	}
	return []Voice{
		{"carolina", "pt-BR"}, {"leo", "pt-BR"},
		{"tiffany", "en-US"}, {"matthew", "en-US"},
		{"amy", "en-GB"}, {"olivia", "en-AU"},
		{"kiara", "en-IN/hi-IN"}, {"arjun", "en-IN/hi-IN"},
		{"ambre", "fr-FR"}, {"florian", "fr-FR"},
		{"beatrice", "it-IT"}, {"lorenzo", "it-IT"},
		{"tina", "de-DE"}, {"lennart", "de-DE"},
		{"lupe", "es-US"}, {"carlos", "es-US"},
	}
}

func SupportsRegion(region string) bool {
	return slices.Contains([]string{"us-east-1", "us-west-2", "eu-north-1", "ap-northeast-1"}, region)
}

func SupportsModelRegion(modelID, region string) bool {
	return (modelID == Nova2 || modelID == Nova25) && SupportsRegion(region)
}

// ValidateOptions checks the model, voice, format and history before a caller
// resolves credentials or opens a billable stream.
func ValidateOptions(options Options) error {
	_, err := normalizeOptions(options)
	return err
}

func normalizeOptions(options Options) (Options, error) {
	if options.ModelID == "" {
		options.ModelID = Nova25
	}
	if options.ModelID != Nova2 && options.ModelID != Nova25 {
		return Options{}, ErrUnsupportedProvider
	}
	if options.VoiceID == "" {
		options.VoiceID = "carolina"
	}
	voiceSupported := false
	for _, voice := range Voices(options.ModelID) {
		voiceSupported = voiceSupported || voice.ID == options.VoiceID
	}
	if !voiceSupported {
		return Options{}, errors.New("selecione uma voz disponível no Nova Sonic")
	}
	if options.InputSampleRate == 0 {
		options.InputSampleRate = InputSampleRate
	}
	if !slices.Contains([]int{8000, 16000, 24000}, options.InputSampleRate) {
		return Options{}, errors.New("Nova Sonic aceita PCM de entrada em 8, 16 ou 24 kHz")
	}
	if options.MaxTokens == 0 {
		options.MaxTokens = 2048
	}
	if options.MaxTokens < 1 || options.MaxTokens > 4096 {
		return Options{}, errors.New("o limite de resposta deve ficar entre 1 e 4096 tokens")
	}
	if strings.TrimSpace(options.SystemPrompt) == "" {
		options.SystemPrompt = "Você é um assistente de conversa por voz. Responda de forma clara e breve no idioma em que a pessoa falar."
	}
	characters := len(options.SystemPrompt)
	options.History = slices.Clone(options.History)
	for i := range options.History {
		options.History[i].Role = strings.ToUpper(options.History[i].Role)
		if options.History[i].Role != "USER" && options.History[i].Role != "ASSISTANT" {
			return Options{}, errors.New("o histórico da conversa aceita somente USER e ASSISTANT")
		}
		characters += len(options.History[i].Text)
	}
	if characters > 240000 {
		return Options{}, errors.New("o contexto da conversa ao vivo excede 240 mil caracteres")
	}
	return options, nil
}
