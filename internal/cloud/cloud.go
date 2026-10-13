// Package cloud implements bounded AWS speech transcription using the
// official AWS SDK for Go v2 and the user's existing AWS credential chain.
package cloud

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"ai-talker/internal/audio"
	"ai-talker/internal/config"
	"ai-talker/internal/transcript"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/aws-sdk-go-v2/service/transcribe"
	"github.com/aws/aws-sdk-go-v2/service/transcribestreaming"
)

const (
	callTimeout             = 120 * time.Second
	verifyTimeout           = 20 * time.Second
	connectTimeout          = 15 * time.Second
	shortSpeechRetrySeconds = 8
)

var errLiveContinuation = errors.New("a resposta do motor continuou além do trecho de áudio; tente reconhecer novamente")

// ConverseClient is the small SDK interface needed by a transcriber. Mock
// implementations never need AWS credentials or a network connection.
type ConverseClient interface {
	Converse(context.Context, *bedrockruntime.ConverseInput, ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error)
}

type converseStreamClient interface {
	ConverseStream(context.Context, *bedrockruntime.ConverseStreamInput, ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseStreamOutput, error)
}

type transcriber struct {
	settings  config.Settings
	client    ConverseClient
	close     func() error
	closeOnce sync.Once
	closeErr  error
}

var _ transcript.Factory = New
var _ transcript.Transcriber = (*transcriber)(nil)
var _ transcript.StreamingTranscriber = (*transcriber)(nil)

// New is the factory shared by the desktop and CLI. Loading the SDK config does
// not request credentials or invoke the model.
func New(ctx context.Context, settings config.Settings) (transcript.Transcriber, error) {
	if err := settings.Validate(); err != nil {
		return nil, fmt.Errorf("configurações AWS inválidas: %w", err)
	}
	model, ok := config.CloudModelByID(settings.CloudModelID())
	if !ok {
		return nil, errors.New("selecione um modelo Cloud válido")
	}
	if !model.SupportsRegion(settings.Region) {
		return nil, fmt.Errorf("%s está disponível no Bedrock somente em us-east-1, us-west-2, eu-north-1 e ap-northeast-1", model.Name)
	}
	if err := settings.TranscriptionSetupError(); err != nil {
		return nil, err
	}
	timeout := callTimeout
	if settings.DirectCloudAudio() {
		timeout = speechTimeout(float64(settings.ChunkSeconds))
	} else if model.Backend == config.CloudConverse {
		timeout = transcript.BedrockPartTimeout(float64(settings.ChunkSeconds))
	}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	cfg, closeHTTP, err := loadAWSConfig(ctx, settings, timeout)
	if err != nil {
		return nil, readableError(err, settings.Profile)
	}
	switch model.Backend {
	case config.CloudTranscribe:
		return &transcribeBatch{
			ownedCloudClient: ownedCloudClient{closeHTTP: closeHTTP}, settings: settings,
			jobs: transcribe.NewFromConfig(cfg), objects: s3.NewFromConfig(cfg), wait: waitSpeech,
		}, nil
	case config.CloudStreaming:
		return newTranscribeStreaming(settings, transcribestreaming.NewFromConfig(cfg), closeHTTP), nil
	case config.CloudSonic:
		return newSonic(settings, bedrockruntime.NewFromConfig(cfg), closeHTTP), nil
	}
	return &transcriber{
		settings: settings,
		client:   bedrockruntime.NewFromConfig(cfg),
		close: func() error {
			closeHTTP()
			return nil
		},
	}, nil
}

// NewWithClient constructs a transcriber with a supplied Converse implementation.
// It validates settings but neither loads AWS configuration nor fetches
// credentials. Close forwards to a client's optional Close() error method.
func NewWithClient(settings config.Settings, client ConverseClient) (transcript.Transcriber, error) {
	if err := settings.Validate(); err != nil {
		return nil, fmt.Errorf("configurações AWS inválidas: %w", err)
	}
	if client == nil {
		return nil, errors.New("informe um cliente Amazon Bedrock")
	}
	model, ok := config.CloudModelByID(settings.CloudModelID())
	if !ok || model.Backend != config.CloudConverse {
		return nil, errors.New("este cliente Converse aceita somente os modelos Voxtral")
	}
	t := &transcriber{settings: settings, client: client}
	if closer, ok := client.(interface{ Close() error }); ok {
		t.close = closer.Close
	}
	return t, nil
}

func (t *transcriber) Transcribe(ctx context.Context, chunk audio.Chunk) (transcript.ChunkResult, error) {
	return t.transcribe(ctx, chunk, nil)
}

func (t *transcriber) TranscribeStream(ctx context.Context, chunk audio.Chunk, onPartial func(string)) (transcript.ChunkResult, error) {
	return t.transcribe(ctx, chunk, onPartial)
}

func (t *transcriber) transcribe(ctx context.Context, chunk audio.Chunk, onPartial func(string)) (result transcript.ChunkResult, err error) {
	if err := ctx.Err(); err != nil {
		return transcript.ChunkResult{}, readableError(err, t.settings.Profile)
	}
	if !chunk.HasSpeech {
		return transcript.ChunkResult{Usage: map[string]any{}}, nil
	}
	if len(chunk.Audio) == 0 || chunk.Duration <= 0 || math.IsNaN(chunk.Duration) || math.IsInf(chunk.Duration, 0) {
		return transcript.ChunkResult{}, errors.New("a parte com fala precisa conter áudio e uma duração válida")
	}

	started := time.Now()
	ctx, timings := withRequestTimings(ctx)
	firstText := &firstTextTimer{started: started}
	timings.firstText = firstText
	defer func() {
		result.Usage = firstText.record(timings.record(result.Usage))
		result.ElapsedSeconds = time.Since(started).Seconds()
	}()
	ctx, cancel := context.WithTimeout(ctx, transcript.BedrockPartTimeout(chunk.Duration))
	defer cancel()
	prompt, retryPrompt := transcriptionPrompt(t.settings.Glossary), shortSpeechRetryPrompt(t.settings.Glossary)
	if onPartial != nil {
		prompt = streamTranscriptionPrompt()
		retryPrompt = prompt
	}
	text, firstUsage, err := t.requestTranscription(ctx, chunk,
		speakerPrompt(prompt, t.settings.SpeakerLabels), onPartial)
	if err != nil {
		return transcript.ChunkResult{}, err
	}
	usage := tokenUsage(firstUsage)
	reviewReason := ""
	unclear := false
	if onPartial != nil {
		text, unclear = transcript.CleanLiveText(text)
		if unclear {
			reviewReason = "O motor indicou palavras incertas; compare esse trecho com o áudio ou tente novamente."
		}
	}
	if text == "" && chunk.Duration <= shortSpeechRetrySeconds {
		recovered, secondUsage, err := t.requestTranscription(ctx, chunk,
			speakerPrompt(retryPrompt, t.settings.SpeakerLabels), onPartial)
		if err != nil {
			return transcript.ChunkResult{}, err
		}
		combinedUsage := make(map[string]any)
		addTokenUsage(combinedUsage, usage)
		addTokenUsage(combinedUsage, tokenUsage(secondUsage))
		usage = combinedUsage
		text = recovered
		if onPartial != nil {
			var retryUnclear bool
			text, retryUnclear = transcript.CleanLiveText(text)
			unclear = unclear || retryUnclear
		}
		if text != "" {
			reviewReason = "O modelo indicou ausência de fala na primeira tentativa; confira o texto recuperado."
		}
	}
	if unclear && reviewReason == "" {
		reviewReason = "O motor indicou palavras incertas; compare esse trecho com o áudio ou tente novamente."
	}
	firstText.observe(text, "final")
	return transcript.ChunkResult{
		Text:           text,
		Usage:          usage,
		ElapsedSeconds: time.Since(started).Seconds(),
		ReviewReason:   reviewReason,
	}, nil
}

func (t *transcriber) requestTranscription(
	ctx context.Context, chunk audio.Chunk, prompt string, onPartial func(string),
) (string, *types.TokenUsage, error) {
	if err := ctx.Err(); err != nil {
		return "", nil, readableError(err, t.settings.Profile)
	}
	input := t.transcriptionInput(chunk, prompt)
	if onPartial != nil {
		// Keep the transcription instruction after the audio. Spoken requests
		// at the end of a clip must remain dictation, not replace the task.
		content := input.Messages[0].Content
		content[0], content[1] = content[1], content[0]
		input.InferenceConfig.MaxTokens = aws.Int32(int32(max(256, min(2048, math.Ceil(chunk.Duration*48)))))
	}
	var usage *types.TokenUsage
	for attempt := 0; ; attempt++ {
		text, currentUsage, err := t.sendTranscription(ctx, input, onPartial, chunk.Duration, attempt == 0)
		usage = combineTokenUsage(usage, currentUsage)
		budget := aws.ToInt32(input.InferenceConfig.MaxTokens)
		outputLimit := errors.Is(err, ErrOutputLimit) && budget < 8192
		continuation := onPartial != nil && errors.Is(err, errLiveContinuation)
		if attempt > 0 || !outputLimit && !continuation || ctx.Err() != nil {
			return text, usage, err
		}
		// Retry the same audio once through Converse. Its complete response
		// also avoids exposing another failed provisional continuation.
		// A truncated response never becomes a completed cache part.
		if outputLimit {
			input.InferenceConfig.MaxTokens = aws.Int32(min(8192, max(1024, budget*2)))
		}
	}
}

func (t *transcriber) sendTranscription(
	ctx context.Context, input *bedrockruntime.ConverseInput, onPartial func(string), duration float64, allowStreaming bool,
) (string, *types.TokenUsage, error) {
	if err := transcript.CheckAdmission(ctx); err != nil {
		return "", nil, readableError(err, t.settings.Profile)
	}
	finishRequest := measurePhase(ctx, "model_request_seconds")
	defer finishRequest()
	countPhase(ctx, "model_requests")
	if streaming, ok := t.client.(converseStreamClient); ok && onPartial != nil && allowStreaming {
		finishOpen := measurePhase(ctx, "request_open_seconds")
		response, err := streaming.ConverseStream(ctx, &bedrockruntime.ConverseStreamInput{
			ModelId: input.ModelId, Messages: input.Messages, InferenceConfig: input.InferenceConfig,
		}, singleTranscriptionAttempt)
		finishOpen()
		if err != nil {
			return "", nil, readableError(err, t.settings.Profile)
		}
		if response == nil || response.GetStream() == nil {
			return "", nil, incompleteResponse("")
		}
		return t.readTranscriptionStream(ctx, response.GetStream(), onPartial, duration)
	}
	response, err := t.client.Converse(ctx, input, singleTranscriptionAttempt)
	if err != nil {
		return "", nil, readableError(err, t.settings.Profile)
	}
	if err := ctx.Err(); err != nil {
		return "", nil, readableError(err, t.settings.Profile)
	}
	if response == nil {
		return "", nil, incompleteResponse("")
	}
	switch response.StopReason {
	case types.StopReasonEndTurn, types.StopReasonStopSequence:
	default:
		return "", response.Usage, incompleteResponse(string(response.StopReason))
	}

	message, ok := response.Output.(*types.ConverseOutputMemberMessage)
	if !ok || message == nil {
		return "", nil, incompleteResponse("")
	}
	var text strings.Builder
	hasText := false
	for _, block := range message.Value.Content {
		if block, ok := block.(*types.ContentBlockMemberText); ok && block != nil {
			hasText = true
			text.WriteString(block.Value)
		}
	}
	if !hasText {
		return "", nil, incompleteResponse("")
	}
	result := strings.TrimSpace(text.String())
	if result == "[SEM_FALA]" {
		result = ""
	}
	if onPartial != nil && transcript.ImplausibleLiveText(result, duration) {
		return "", response.Usage, errLiveContinuation
	}
	if onPartial != nil {
		if clean := transcript.LivePartialText(result); clean != "" {
			observeFirstText(ctx, clean, "final")
			onPartial(clean)
		}
	} else {
		observeFirstText(ctx, result, "final")
	}
	return result, response.Usage, nil
}

func singleTranscriptionAttempt(options *bedrockruntime.Options) {
	// The transcription service owns retries, deadlines and their UI feedback.
	// Keep the SDK from multiplying those attempts behind the same deadline.
	options.Retryer = aws.NopRetryer{}
}

func (t *transcriber) transcriptionInput(chunk audio.Chunk, prompt string) *bedrockruntime.ConverseInput {
	format := types.AudioFormatMp3
	if chunk.Format == audio.FormatWAV {
		format = types.AudioFormatWav
	}
	return &bedrockruntime.ConverseInput{
		ModelId: aws.String(t.settings.ModelID()),
		Messages: []types.Message{{
			Role: types.ConversationRoleUser,
			Content: []types.ContentBlock{
				&types.ContentBlockMemberText{Value: prompt},
				&types.ContentBlockMemberAudio{Value: types.AudioBlock{
					Format: format,
					Source: &types.AudioSourceMemberBytes{Value: chunk.Audio},
				}},
			},
		}},
		InferenceConfig: &types.InferenceConfiguration{
			MaxTokens:   aws.Int32(outputTokens(chunk.Duration)),
			Temperature: aws.Float32(0),
		},
	}
}

func (t *transcriber) readTranscriptionStream(
	ctx context.Context, stream bedrockruntime.ConverseStreamOutputReader, onPartial func(string), duration float64,
) (result string, usage *types.TokenUsage, err error) {
	defer func() {
		finishClose := measurePhase(ctx, "stream_close_seconds")
		closeErr := stream.Close()
		finishClose()
		if err == nil && closeErr != nil {
			err = readableError(closeErr, t.settings.Profile)
		}
	}()
	var text strings.Builder
	hasText := false
	var stopReason types.StopReason
	for {
		select {
		case <-ctx.Done():
			return "", nil, readableError(ctx.Err(), t.settings.Profile)
		case event, open := <-stream.Events():
			if !open {
				if err := stream.Err(); err != nil {
					return "", nil, readableError(err, t.settings.Profile)
				}
				if err := ctx.Err(); err != nil {
					return "", nil, readableError(err, t.settings.Profile)
				}
				if !hasText || stopReason != types.StopReasonEndTurn && stopReason != types.StopReasonStopSequence {
					return "", usage, incompleteResponse(string(stopReason))
				}
				result := strings.TrimSpace(text.String())
				if result == "[SEM_FALA]" {
					result = ""
				}
				return result, usage, nil
			}
			switch event := event.(type) {
			case *types.ConverseStreamOutputMemberContentBlockDelta:
				if event == nil {
					continue
				}
				if delta, ok := event.Value.Delta.(*types.ContentBlockDeltaMemberText); ok && delta != nil {
					hasText = true
					text.WriteString(delta.Value)
					partial := strings.TrimSpace(text.String())
					if transcript.ImplausibleLiveText(partial, duration) {
						return "", usage, errLiveContinuation
					}
					if partial != "" && !strings.HasPrefix("[SEM_FALA]", partial) && onPartial != nil {
						if clean := transcript.LivePartialText(partial); clean != "" {
							observeFirstText(ctx, clean, "partial")
							onPartial(clean)
						}
					}
				}
			case *types.ConverseStreamOutputMemberMessageStop:
				if event != nil {
					stopReason = event.Value.StopReason
				}
			case *types.ConverseStreamOutputMemberMetadata:
				if event != nil {
					usage = event.Value.Usage
				}
			}
		}
	}
}

func outputTokens(duration float64) int32 {
	// Clamp before conversion to int32, including unusually long finite inputs.
	return int32(math.Max(256, math.Min(8192, math.Ceil(duration*32))))
}

func combineTokenUsage(first, next *types.TokenUsage) *types.TokenUsage {
	if first == nil {
		return next
	}
	if next == nil {
		return first
	}
	sum := func(a, b *int32) *int32 {
		if a == nil && b == nil {
			return nil
		}
		return aws.Int32(aws.ToInt32(a) + aws.ToInt32(b))
	}
	return &types.TokenUsage{
		InputTokens:           sum(first.InputTokens, next.InputTokens),
		OutputTokens:          sum(first.OutputTokens, next.OutputTokens),
		TotalTokens:           sum(first.TotalTokens, next.TotalTokens),
		CacheReadInputTokens:  sum(first.CacheReadInputTokens, next.CacheReadInputTokens),
		CacheWriteInputTokens: sum(first.CacheWriteInputTokens, next.CacheWriteInputTokens),
		CacheDetails:          append(append([]types.CacheDetail(nil), first.CacheDetails...), next.CacheDetails...),
	}
}

func tokenUsage(usage *types.TokenUsage) map[string]any {
	result := make(map[string]any)
	if usage == nil {
		return result
	}
	for name, count := range map[string]*int32{
		"inputTokens":           usage.InputTokens,
		"outputTokens":          usage.OutputTokens,
		"totalTokens":           usage.TotalTokens,
		"cacheReadInputTokens":  usage.CacheReadInputTokens,
		"cacheWriteInputTokens": usage.CacheWriteInputTokens,
	} {
		if count != nil {
			result[name] = *count
		}
	}
	if len(usage.CacheDetails) > 0 {
		details := make([]map[string]any, 0, len(usage.CacheDetails))
		for _, detail := range usage.CacheDetails {
			entry := map[string]any{"ttl": string(detail.Ttl)}
			if detail.InputTokens != nil {
				entry["inputTokens"] = *detail.InputTokens
			}
			details = append(details, entry)
		}
		result["cacheDetails"] = details
	}
	return result
}

func (t *transcriber) Close() error {
	t.closeOnce.Do(func() {
		if t.close != nil {
			if err := t.close(); err != nil {
				t.closeErr = fmt.Errorf("fechar o cliente Amazon Bedrock: %w", err)
			}
		}
	})
	return t.closeErr
}

// Verify checks the selected credentials with STS. It uses the same hidden
// credential_process chain and CA configuration as New.
func Verify(ctx context.Context, settings config.Settings) error {
	if err := settings.Validate(); err != nil {
		return fmt.Errorf("configurações AWS inválidas: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, verifyTimeout)
	defer cancel()
	cfg, closeHTTP, err := loadAWSConfig(ctx, settings, verifyTimeout)
	if err != nil {
		return readableError(err, settings.Profile)
	}
	defer closeHTTP()
	_, err = sts.NewFromConfig(cfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	return readableError(err, settings.Profile)
}
