package api

import (
	"bytes"
	"context"
	"errors"
	"mime/multipart"
	"time"

	"ai-talker/internal/apiconfig"
	"ai-talker/internal/audio"
	"ai-talker/internal/config"
	"ai-talker/internal/secrets"
	"ai-talker/internal/transcript"
)

type transcriber struct {
	client                    *Client
	model, language, glossary string
}

func Factory(vault secrets.Vault) transcript.Factory {
	return func(ctx context.Context, settings config.Settings) (transcript.Transcriber, error) {
		if err := settings.AudioAPI.Validate(); err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		client, err := NewClient(settings.AudioAPI.Connection, vault, nil)
		if err != nil {
			return nil, err
		}
		return &transcriber{client: client, model: settings.AudioAPI.Model,
			language: settings.AudioAPI.Language, glossary: settings.Glossary}, nil
	}
}

func (t *transcriber) Transcribe(ctx context.Context, chunk audio.Chunk) (transcript.ChunkResult, error) {
	if err := ctx.Err(); err != nil {
		return transcript.ChunkResult{}, err
	}
	if t.client.connection.Provider == apiconfig.AzureSpeech {
		return t.transcribeAzureSpeech(ctx, chunk)
	}
	languages, err := (apiconfig.Audio{Connection: t.client.connection, Model: t.model, Language: t.language}).LanguageCodes()
	if err != nil {
		return transcript.ChunkResult{}, err
	}
	if len(chunk.Audio) > 25*1024*1024 || len(chunk.Audio) == 0 {
		return transcript.ChunkResult{}, errors.New("a parte de áudio excedeu o limite de upload da API")
	}
	started := time.Now()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	fields := map[string]string{"model": t.model, "prompt": t.glossary}
	if t.client.connection.Provider == apiconfig.OpenAI && t.model == "gpt-4o-transcribe-diarize" {
		delete(fields, "prompt")
		fields["chunking_strategy"] = "auto"
	}
	for key, value := range fields {
		if value != "" {
			if err := form.WriteField(key, value); err != nil {
				return transcript.ChunkResult{}, err
			}
		}
	}
	languageField := "language"
	if (apiconfig.Audio{Connection: t.client.connection, Model: t.model}).MultipleLanguages() {
		languageField = "languages[]"
	}
	for _, language := range languages {
		if err := form.WriteField(languageField, language); err != nil {
			return transcript.ChunkResult{}, err
		}
	}
	filename := "audio.mp3"
	if chunk.Format == audio.FormatWAV {
		filename = "audio.wav"
	}
	file, err := form.CreateFormFile("file", filename)
	if err != nil {
		return transcript.ChunkResult{}, err
	}
	if _, err := file.Write(chunk.Audio); err != nil {
		return transcript.ChunkResult{}, err
	}
	if err := form.Close(); err != nil {
		return transcript.ChunkResult{}, err
	}
	var response struct {
		Text  string         `json:"text"`
		Usage map[string]any `json:"usage"`
	}
	if err := t.client.send(ctx, "POST", "/audio/transcriptions", form.FormDataContentType(), &body, &response); err != nil {
		return transcript.ChunkResult{}, err
	}
	return transcript.ChunkResult{Text: response.Text, Usage: response.Usage,
		ElapsedSeconds: time.Since(started).Seconds()}, nil
}
func (t *transcriber) Close() error { return nil }
