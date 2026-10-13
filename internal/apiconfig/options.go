// Package apiconfig holds public API metadata. Secrets never belong here.
package apiconfig

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/url"
	"regexp"
	"strings"
)

const (
	OpenRouter  = "openrouter"
	OpenAI      = "openai"
	Generic     = "generic"
	Azure       = "azure"
	AzureSpeech = "azure_speech"

	AzureSpeechModel      = "MAI-Transcribe-2"
	MaxConnectionsPerFlow = 10
)

type Connection struct {
	ID       string `json:"id,omitempty"`
	Provider string `json:"provider,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
}

type SavedConnection struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Endpoint string `json:"endpoint,omitempty"`
	Text     bool   `json:"text,omitempty"`
	Audio    bool   `json:"audio,omitempty"`
}

func (s SavedConnection) Connection() Connection {
	return Connection{ID: s.ID, Provider: s.Provider, Endpoint: s.Endpoint}
}

func (s SavedConnection) Validate() error {
	if !validConnectionID(s.ID) || strings.TrimSpace(s.Name) == "" ||
		len([]rune(s.Name)) > 80 || strings.ContainsAny(s.Name, "\x00\r\n\t") ||
		!s.Text && !s.Audio || s.Provider == AzureSpeech && s.Text || s.Provider == Azure && s.Audio {
		return errors.New("o nome, a identificação ou o fluxo da conexão de API é inválido")
	}
	return s.Connection().Validate()
}

func NewConnectionID() (string, error) {
	var identifier [16]byte
	if _, err := rand.Read(identifier[:]); err != nil {
		return "", errors.New("não foi possível criar a identificação da conexão")
	}
	return hex.EncodeToString(identifier[:]), nil
}

func LegacyConnectionID(connection Connection) string {
	digest := sha256.Sum256([]byte(connection.Provider + "\x00" + connection.BaseURL()))
	return "legacy-" + hex.EncodeToString(digest[:16])
}

func validConnectionID(id string) bool {
	if len(id) < 16 || len(id) > 64 {
		return false
	}
	for _, character := range id {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-' {
			continue
		}
		return false
	}
	return true
}

type Audio struct {
	Connection
	Model    string `json:"model,omitempty"`
	Language string `json:"language,omitempty"`
}

func (c Connection) BaseURL() string {
	switch c.Provider {
	case OpenRouter:
		return "https://openrouter.ai/api/v1"
	case OpenAI:
		return "https://api.openai.com/v1"
	default:
		return strings.TrimRight(strings.TrimSpace(c.Endpoint), "/")
	}
}

func (c Connection) Validate() error {
	if c.ID != "" && !validConnectionID(c.ID) {
		return errors.New("a identificação da conexão de API é inválida")
	}
	if c.Provider != OpenRouter && c.Provider != OpenAI && c.Provider != Generic &&
		c.Provider != Azure && c.Provider != AzureSpeech {
		return errors.New("selecione OpenRouter, OpenAI, Azure OpenAI/Foundry, Azure Speech ou API compatível")
	}
	target, err := url.Parse(c.BaseURL())
	if err != nil || target.Hostname() == "" || target.User != nil ||
		target.RawQuery != "" || target.Fragment != "" || len(c.BaseURL()) > 2048 ||
		strings.ContainsAny(c.BaseURL(), "\x00\r\n\t") {
		return errors.New("informe um endpoint válido, sem chave ou parâmetros na URL")
	}
	if target.Scheme != "https" {
		address := net.ParseIP(target.Hostname())
		if target.Scheme != "http" || !(strings.EqualFold(target.Hostname(), "localhost") || address != nil && address.IsLoopback()) {
			return errors.New("o endpoint deve usar HTTPS; HTTP é permitido somente no computador local")
		}
	}
	if (c.Provider == Azure || c.Provider == AzureSpeech) && target.Scheme != "https" {
		return errors.New("Azure OpenAI/Foundry e Azure Speech exigem um endpoint HTTPS")
	}
	if c.Provider != Generic && c.Endpoint != "" && strings.TrimRight(c.Endpoint, "/") != c.BaseURL() {
		return errors.New("o endpoint deste preset é fixo; selecione API compatível para outro endereço")
	}
	return nil
}

// CredentialTarget binds reuse to the complete base endpoint, including path.
func (c Connection) CredentialTarget() string {
	if c.ID == "" {
		return c.LegacyCredentialTarget()
	}
	digest := sha256.Sum256([]byte(c.ID + "\x00" + c.BaseURL()))
	return "AI-Talker/API/" + hex.EncodeToString(digest[:])
}

// LegacyCredentialTarget is the endpoint-only credential name used before
// named connections were added. API clients use it only to migrate old keys.
func (c Connection) LegacyCredentialTarget() string {
	digest := sha256.Sum256([]byte(c.BaseURL()))
	return "AI-Talker/API/" + hex.EncodeToString(digest[:])
}

func (a Audio) Validate() error {
	if a.Provider == Azure {
		return errors.New("Azure OpenAI/Foundry é uma conexão de texto; transcrição usa Azure Speech em recurso separado")
	}
	if err := a.Connection.Validate(); err != nil {
		return err
	}
	if a.Provider == AzureSpeech && a.Model != AzureSpeechModel {
		return errors.New("Azure Speech usa o modelo MAI-Transcribe-2")
	}
	if strings.TrimSpace(a.Model) == "" || len(a.Model) > 2048 || strings.ContainsAny(a.Model, "\x00\r\n\t ") {
		return errors.New("informe o ID do modelo de transcrição por API")
	}
	_, err := a.LanguageCodes()
	return err
}

var (
	languageCode        = regexp.MustCompile(`^[a-z]{2,3}(?:-[A-Z]{2})?$`)
	singleLanguageCode  = regexp.MustCompile(`^[a-z]{2}$`)
	transcribeLanguages = regexp.MustCompile(`^(?:[a-z]{2,3}|zh-(?:cn|tw|hk))$`)
)

// MultipleLanguages is restricted to contracts which document a language list.
func (a Audio) MultipleLanguages() bool {
	return a.Provider == OpenAI && a.Model == "gpt-transcribe"
}

// LanguageCodes returns deduplicated hints supported by the provider/model
// contract. Automatic returns none, so the HTTP client omits its language field.
func (a Audio) LanguageCodes() ([]string, error) {
	text := strings.TrimSpace(a.Language)
	if text == "" || strings.EqualFold(text, "auto") {
		return nil, nil
	}
	if len(text) > 256 {
		return nil, errors.New("a lista de idiomas é muito longa")
	}
	var codes []string
	seen := make(map[string]bool)
	for _, value := range strings.Split(text, ",") {
		code := strings.TrimSpace(value)
		valid := languageCode.MatchString(code)
		if a.MultipleLanguages() {
			valid = transcribeLanguages.MatchString(code)
		} else if a.Provider == OpenAI || a.Provider == OpenRouter {
			valid = singleLanguageCode.MatchString(code)
		}
		if !valid {
			return nil, errors.New("use códigos como pt, en, es, fr ou de; deixe vazio para Automático")
		}
		if !seen[code] {
			codes = append(codes, code)
			seen[code] = true
		}
	}
	if len(codes) > 1 && !a.MultipleLanguages() {
		return nil, errors.New("este provider/modelo aceita um único idioma informado; escolha um código ou Automático")
	}
	return codes, nil
}

// KnownAudioModel identifies documented OpenAI transcription model families;
// availability for the account still comes from the requested catalog.
func KnownAudioModel(model string) bool {
	return model == "gpt-transcribe" || model == "whisper-1" ||
		model == "gpt-4o-transcribe" || model == "gpt-4o-mini-transcribe" ||
		model == "gpt-4o-transcribe-diarize" ||
		strings.HasPrefix(model, "gpt-4o-transcribe-20") ||
		strings.HasPrefix(model, "gpt-4o-mini-transcribe-20")
}
