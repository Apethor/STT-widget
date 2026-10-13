// Package api implements bounded OpenAI-compatible text and audio requests.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"ai-talker/internal/apiconfig"
	"ai-talker/internal/secrets"
)

const maxResponseBytes = 4 * 1024 * 1024

type Client struct {
	connection apiconfig.Connection
	vault      secrets.Vault
	http       *http.Client
}

func NewClient(connection apiconfig.Connection, vault secrets.Vault, client *http.Client) (*Client, error) {
	if err := connection.Validate(); err != nil {
		return nil, err
	}
	if vault == nil {
		return nil, errors.New("o armazenamento seguro de chaves está indisponível")
	}
	var transport http.RoundTripper = http.DefaultTransport
	if client != nil && client.Transport != nil {
		transport = client.Transport
	}
	// Always override redirect policy, including injected transports. The
	// bearer token never follows a new URL, even on the same host.
	safe := &http.Client{
		Transport: transport, Timeout: 5 * time.Minute,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return &Client{connection: connection, vault: vault, http: safe}, nil
}

type Error struct{ Status int }

func (e *Error) Error() string {
	switch e.Status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return "A API recusou o acesso. Confira ou substitua a chave deste endpoint."
	case http.StatusTooManyRequests:
		return "A API atingiu seu limite de chamadas. Aguarde e tente novamente."
	case http.StatusBadRequest, http.StatusNotFound, http.StatusUnprocessableEntity:
		return fmt.Sprintf("A API recusou a configuração (HTTP %d). Confira o endpoint, modelo e opções.", e.Status)
	default:
		return fmt.Sprintf("A API não concluiu o pedido (HTTP %d). Confira a conexão e o serviço.", e.Status)
	}
}
func (e *Error) Retryable() bool { return e.Status == 429 || e.Status >= 500 && e.Status <= 599 }

func (c *Client) send(ctx context.Context, method, route, contentType string, body io.Reader, target any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	credentialTarget := c.connection.CredentialTarget()
	key, err := c.vault.Read(credentialTarget)
	if errors.Is(err, secrets.ErrNotFound) && c.connection.ID == apiconfig.LegacyConnectionID(c.connection) {
		legacy := c.connection.LegacyCredentialTarget()
		key, err = c.vault.Read(legacy)
		if err == nil && strings.TrimSpace(key) != "" {
			// Presets with different providers can share this endpoint-only
			// legacy credential. Keep it until explicit key/connection removal
			// so another migrated preset can still make its own secure copy.
			_ = c.vault.Write(credentialTarget, key)
		}
	}
	if err != nil {
		return err
	}
	if strings.TrimSpace(key) == "" || strings.ContainsAny(key, "\x00\r\n") {
		return errors.New("salve uma chave válida para este endpoint nas configurações")
	}
	request, err := http.NewRequestWithContext(ctx, method, c.connection.BaseURL()+route, body)
	if err != nil {
		return errors.New("não foi possível preparar o pedido à API")
	}
	switch c.connection.Provider {
	case apiconfig.Azure:
		request.Header.Set("api-key", key)
	case apiconfig.AzureSpeech:
		request.Header.Set("Ocp-Apim-Subscription-Key", key)
	default:
		request.Header.Set("Authorization", "Bearer "+key)
	}
	request.Header.Set("User-Agent", "AI-Talker/0.8.0")
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := c.http.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Transport errors may echo the full URL or headers; don't persist
		// or show their text. The only exported detail is a safe category.
		return errors.New("não foi possível conectar à API; confira a rede e o endpoint")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &Error{Status: response.StatusCode}
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("a resposta da API foi interrompida")
	}
	if len(payload) > maxResponseBytes {
		return errors.New("a resposta da API excedeu o limite seguro")
	}
	if err := json.Unmarshal(payload, target); err != nil {
		return errors.New("a API retornou um formato incompatível; confira o endpoint e o modelo")
	}
	return ctx.Err()
}

func (c *Client) json(ctx context.Context, route string, value, target any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return errors.New("não foi possível preparar o pedido à API")
	}
	return c.send(ctx, http.MethodPost, route, "application/json", bytes.NewReader(payload), target)
}

type Model struct {
	ID           string `json:"id"`
	Name         string `json:"name,omitempty"`
	Architecture struct {
		InputModalities  []string `json:"input_modalities,omitempty"`
		OutputModalities []string `json:"output_modalities,omitempty"`
	} `json:"architecture,omitempty"`
}

func (c *Client) Models(ctx context.Context) ([]Model, error) {
	if c.connection.Provider == apiconfig.AzureSpeech {
		return nil, errors.New("Azure Speech usa o modelo fixo MAI-Transcribe-2 e não oferece catálogo de modelos")
	}
	return c.models(ctx, false)
}

// AudioModels uses OpenRouter's advertised transcription modality. Generic
// catalogs remain provider-defined; a manual model ID is always available.
func (c *Client) AudioModels(ctx context.Context) ([]Model, error) {
	if c.connection.Provider == apiconfig.AzureSpeech {
		return nil, errors.New("Azure Speech usa o modelo fixo MAI-Transcribe-2 e não oferece catálogo de modelos")
	}
	return c.models(ctx, true)
}

func (c *Client) models(ctx context.Context, audio bool) ([]Model, error) {
	var response struct {
		Data []Model `json:"data"`
	}
	route := "/models"
	if audio && c.connection.Provider == apiconfig.OpenRouter {
		route += "?output_modalities=transcription"
	}
	if err := c.send(ctx, http.MethodGet, route, "", nil, &response); err != nil {
		return nil, err
	}
	var result []Model
	seen := make(map[string]bool)
	for _, model := range response.Data {
		if model.ID == "" || len(model.ID) > 2048 || strings.ContainsAny(model.ID, "\x00\r\n\t ") || seen[model.ID] {
			continue
		}
		seen[model.ID] = true
		if audio && !c.audioModelConfirmed(model) {
			continue
		}
		result = append(result, model)
	}
	return result, nil
}

func (c *Client) audioModelConfirmed(model Model) bool {
	if c.connection.Provider == apiconfig.OpenAI {
		return apiconfig.KnownAudioModel(model.ID)
	}
	if slices.Contains(model.Architecture.OutputModalities, "transcription") {
		return true
	}
	// The OpenRouter endpoint was queried with the documented transcription
	// filter. Explicit contradictory metadata is still rejected.
	return c.connection.Provider == apiconfig.OpenRouter && len(model.Architecture.OutputModalities) == 0
}
