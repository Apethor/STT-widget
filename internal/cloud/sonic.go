package cloud

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"ai-talker/internal/audio"
	"ai-talker/internal/config"
	"ai-talker/internal/transcript"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

type sonicStream interface {
	Send(context.Context, types.InvokeModelWithBidirectionalStreamInput) error
	Events() <-chan types.InvokeModelWithBidirectionalStreamOutput
	Err() error
	Close() error
}

type sonicStreamStart func(context.Context, string) (sonicStream, error)

type sonicTranscriber struct {
	ownedCloudClient
	settings config.Settings
	start    sonicStreamStart
	// The API expects real-time audio cadence. Tests replace this wait only.
	wait func(context.Context, time.Duration) error
}

var _ transcript.Transcriber = (*sonicTranscriber)(nil)
var _ transcript.StreamingTranscriber = (*sonicTranscriber)(nil)

func newSonic(settings config.Settings, client *bedrockruntime.Client, closeHTTP func()) *sonicTranscriber {
	return &sonicTranscriber{
		ownedCloudClient: ownedCloudClient{closeHTTP: closeHTTP}, settings: settings, wait: waitSpeech,
		start: func(ctx context.Context, model string) (sonicStream, error) {
			response, err := client.InvokeModelWithBidirectionalStream(ctx,
				&bedrockruntime.InvokeModelWithBidirectionalStreamInput{ModelId: aws.String(model)})
			if err != nil {
				return nil, err
			}
			if response == nil || response.GetStream() == nil {
				return nil, incompleteSpeech(sonicModelName(model))
			}
			return response.GetStream(), nil
		},
	}
}

func (t *sonicTranscriber) Transcribe(ctx context.Context, chunk audio.Chunk) (transcript.ChunkResult, error) {
	return t.TranscribeStream(ctx, chunk, nil)
}

func (t *sonicTranscriber) TranscribeStream(ctx context.Context, chunk audio.Chunk, onPartial func(string)) (result transcript.ChunkResult, err error) {
	if err := ctx.Err(); err != nil {
		return result, readableError(err, t.settings.Profile)
	}
	if !chunk.HasSpeech {
		return transcript.ChunkResult{Usage: map[string]any{}}, nil
	}
	pcm, err := speechPCM(chunk)
	if err != nil {
		return result, err
	}
	started := time.Now()
	ctx, timings := withRequestTimings(ctx)
	firstText := &firstTextTimer{started: started}
	defer func() {
		result.Usage = firstText.record(timings.record(result.Usage))
		result.ElapsedSeconds = time.Since(started).Seconds()
	}()
	ctx, cancel := context.WithTimeout(ctx, speechTimeout(chunk.Duration))
	defer cancel()
	finishOpen := measurePhase(ctx, "request_open_seconds")
	stream, err := t.start(ctx, t.settings.CloudModelID())
	finishOpen()
	if err != nil {
		return result, readableError(err, t.settings.Profile)
	}
	if stream == nil {
		return result, incompleteSpeech(sonicModelName(t.settings.CloudModelID()))
	}
	senderDone := make(chan error, 1)
	go func() { senderDone <- t.sendSonicAudio(ctx, stream, pcm) }()
	sent := (<-chan error)(senderDone)
	senderFinished := false
	defer func() {
		cancel()
		finishClose := measurePhase(ctx, "stream_close_seconds")
		closeErr := stream.Close()
		finishClose()
		if !senderFinished {
			<-senderDone
		}
		if err == nil && closeErr != nil {
			err = readableError(closeErr, t.settings.Profile)
		}
	}()
	type content struct {
		text   strings.Builder
		closed bool
	}
	blocks := make(map[string]*content)
	var order []string
	usage := make(map[string]any)
	currentText := func() string {
		parts := make([]string, 0, len(order))
		for _, id := range order {
			if text := strings.TrimSpace(blocks[id].text.String()); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, " ")
	}
	events := stream.Events()
	for {
		select {
		case <-ctx.Done():
			return result, readableError(ctx.Err(), t.settings.Profile)
		case sendErr := <-sent:
			senderFinished = true
			sent = nil
			if sendErr != nil {
				return result, readableError(sendErr, t.settings.Profile)
			}
		case item, open := <-events:
			if !open {
				if streamErr := stream.Err(); streamErr != nil {
					return result, readableError(streamErr, t.settings.Profile)
				}
				if !senderFinished {
					select {
					case sendErr := <-senderDone:
						senderFinished = true
						if sendErr != nil {
							return result, readableError(sendErr, t.settings.Profile)
						}
					case <-ctx.Done():
						return result, readableError(ctx.Err(), t.settings.Profile)
					}
				}
				for _, block := range blocks {
					if !block.closed {
						return result, incompleteSpeech(sonicModelName(t.settings.CloudModelID()))
					}
				}
				result.Text, _ = transcript.CleanLiveText(currentText())
				result.ElapsedSeconds = time.Since(started).Seconds()
				usage["audio_seconds"], usage["sessions"] = chunk.Duration, 1
				result.Usage = usage
				return result, nil
			}
			event, ok := item.(*types.InvokeModelWithBidirectionalStreamOutputMemberChunk)
			if !ok || event == nil {
				continue
			}
			var envelope struct {
				Event struct {
					ContentStart *struct {
						ContentID             string `json:"contentId"`
						Type                  string `json:"type"`
						Role                  string `json:"role"`
						AdditionalModelFields string `json:"additionalModelFields"`
					} `json:"contentStart"`
					TextOutput *struct {
						ContentID string `json:"contentId"`
						Content   string `json:"content"`
					} `json:"textOutput"`
					ContentEnd *struct {
						ContentID string `json:"contentId"`
					} `json:"contentEnd"`
					Usage *struct {
						TotalInputTokens  float64        `json:"totalInputTokens"`
						TotalOutputTokens float64        `json:"totalOutputTokens"`
						TotalTokens       float64        `json:"totalTokens"`
						Details           map[string]any `json:"details"`
					} `json:"usageEvent"`
				} `json:"event"`
			}
			if len(event.Value.Bytes) > 1024*1024 || json.Unmarshal(event.Value.Bytes, &envelope) != nil {
				return result, incompleteSpeech(sonicModelName(t.settings.CloudModelID()))
			}
			switch {
			case envelope.Event.ContentStart != nil:
				start := envelope.Event.ContentStart
				if start.Role != "USER" || start.Type != "TEXT" {
					continue
				}
				var fields struct {
					GenerationStage string `json:"generationStage"`
				}
				if json.Unmarshal([]byte(start.AdditionalModelFields), &fields) != nil || fields.GenerationStage != "FINAL" {
					continue
				}
				if start.ContentID == "" || blocks[start.ContentID] != nil {
					return result, incompleteSpeech(sonicModelName(t.settings.CloudModelID()))
				}
				blocks[start.ContentID] = &content{}
				order = append(order, start.ContentID)
			case envelope.Event.TextOutput != nil:
				output := envelope.Event.TextOutput
				block := blocks[output.ContentID]
				// Replies, translations and generated speech belong to ASSISTANT,
				// and never become dictation, even if their stage is FINAL.
				if block == nil || block.closed {
					continue
				}
				block.text.WriteString(output.Content)
				text := transcript.LivePartialText(currentText())
				if transcript.ImplausibleLiveText(text, chunk.Duration) {
					return result, errLiveContinuation
				}
				if text != "" {
					firstText.observe(text, "final")
					if onPartial != nil {
						onPartial(text)
					}
				}
			case envelope.Event.ContentEnd != nil:
				if block := blocks[envelope.Event.ContentEnd.ContentID]; block != nil {
					block.closed = true
				}
			case envelope.Event.Usage != nil:
				event := envelope.Event.Usage
				usage["input_tokens"], usage["output_tokens"], usage["total_tokens"] =
					event.TotalInputTokens, event.TotalOutputTokens, event.TotalTokens
				usage["token_details"] = event.Details
			}
		}
	}
}

func sonicModelName(modelID string) string {
	if model, ok := config.CloudModelByID(modelID); ok && model.Backend == config.CloudSonic {
		name, _, _ := strings.Cut(model.Name, " · ")
		return name
	}
	return "Nova Sonic"
}

func sendSonicEvent(ctx context.Context, stream sonicStream, name string, body any) error {
	payload, err := json.Marshal(map[string]any{"event": map[string]any{name: body}})
	if err != nil {
		return err
	}
	return stream.Send(ctx, &types.InvokeModelWithBidirectionalStreamInputMemberChunk{
		Value: types.BidirectionalInputPayloadPart{Bytes: payload},
	})
}

func (t *sonicTranscriber) sendSonicAudio(ctx context.Context, stream sonicStream, pcm []byte) error {
	const prompt, system, input = "transcription", "instructions", "recorded-audio"
	finishSetup := measurePhase(ctx, "session_setup_seconds")
	events := []struct {
		name string
		body map[string]any
	}{
		{"sessionStart", map[string]any{
			"inferenceConfiguration":     map[string]any{"maxTokens": 64, "topP": 0.9, "temperature": 0.0},
			"turnDetectionConfiguration": map[string]any{"endpointingSensitivity": "HIGH"},
		}},
		{"promptStart", map[string]any{
			"promptName": prompt, "textOutputConfiguration": map[string]any{"mediaType": "text/plain"},
			"audioOutputConfiguration": map[string]any{
				"mediaType": "audio/lpcm", "sampleRateHertz": 24000, "sampleSizeBits": 16,
				"channelCount": 1, "voiceId": "carolina", "encoding": "base64", "audioType": "SPEECH",
			},
		}},
		{"contentStart", map[string]any{
			"promptName": prompt, "contentName": system, "type": "TEXT", "interactive": false, "role": "SYSTEM",
			"textInputConfiguration": map[string]any{"mediaType": "text/plain"},
		}},
		{"textInput", map[string]any{
			"promptName": prompt, "contentName": system,
			"content": "Você está ouvindo um ditado. Permaneça em silêncio enquanto a pessoa fala.",
		}},
		{"contentEnd", map[string]any{"promptName": prompt, "contentName": system}},
		{"contentStart", map[string]any{
			"promptName": prompt, "contentName": input, "type": "AUDIO", "interactive": true, "role": "USER",
			"audioInputConfiguration": map[string]any{
				"mediaType": "audio/lpcm", "sampleRateHertz": audio.SampleRate, "sampleSizeBits": 16,
				"channelCount": 1, "audioType": "SPEECH", "encoding": "base64",
			},
		}},
	}
	for _, event := range events {
		if err := sendSonicEvent(ctx, stream, event.name, event.body); err != nil {
			finishSetup()
			return err
		}
	}
	finishSetup()
	// A short silent tail lets endpoint detection finalize the last utterance.
	// It is transport padding; the recording and its screenshot clock stay exact.
	sendSamples := func(samples []byte) error {
		for len(samples) > 0 {
			frame := samples[:min(1024, len(samples))]
			if err := sendSonicEvent(ctx, stream, "audioInput", map[string]any{
				"promptName": prompt, "contentName": input, "content": base64.StdEncoding.EncodeToString(frame),
			}); err != nil {
				return err
			}
			if t.wait == nil {
				return errors.New("o controle de cadência do Nova Sonic está indisponível")
			}
			if err := t.wait(ctx, time.Duration(len(frame))*time.Second/(audio.SampleRate*audio.SampleBytes)); err != nil {
				return err
			}
			samples = samples[len(frame):]
		}
		return nil
	}
	finishAudio := measurePhase(ctx, "audio_send_seconds")
	err := sendSamples(pcm)
	finishAudio()
	if err != nil {
		return err
	}
	finishInput := measurePhase(ctx, "input_finalize_seconds")
	defer finishInput()
	if err := sendSamples(make([]byte, 2*audio.SampleRate*audio.SampleBytes)); err != nil {
		return err
	}
	for _, event := range []struct {
		name string
		body map[string]any
	}{
		{"contentEnd", map[string]any{"promptName": prompt, "contentName": input}},
		{"promptEnd", map[string]any{"promptName": prompt}},
		{"sessionEnd", map[string]any{}},
	} {
		if err := sendSonicEvent(ctx, stream, event.name, event.body); err != nil {
			return err
		}
	}
	return nil
}
