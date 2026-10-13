package cloud

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"ai-talker/internal/audio"
	"ai-talker/internal/config"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/aws/smithy-go"
)

type converseFunc func(context.Context, *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error)

func (f converseFunc) Converse(
	ctx context.Context,
	input *bedrockruntime.ConverseInput,
	_ ...func(*bedrockruntime.Options),
) (*bedrockruntime.ConverseOutput, error) {
	return f(ctx, input)
}

func completeResponse(text string, reason types.StopReason) *bedrockruntime.ConverseOutput {
	return &bedrockruntime.ConverseOutput{
		StopReason: reason,
		Output: &types.ConverseOutputMemberMessage{Value: types.Message{
			Role:    types.ConversationRoleAssistant,
			Content: []types.ContentBlock{&types.ContentBlockMemberText{Value: text}},
		}},
	}
}

func spokenChunk(duration float64) audio.Chunk {
	return audio.Chunk{Index: 3, Start: 80, Duration: duration, Audio: []byte("mock MP3"), HasSpeech: true}
}

func TestNew(t *testing.T) {
	settings := config.Default()
	command, record := prepareCredentialHelper(t)
	isolateAWS(t, "[profile desktop-test]\ncredential_process = "+command+"\n")
	settings.Profile = "desktop-test"

	transcriber, err := New(t.Context(), settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := transcriber.Close(); err != nil {
		t.Fatal(err)
	}
	if calls := helperRecords(t, record); len(calls) != 0 {
		t.Fatalf("creating and closing a client ran the credential helper %d times", len(calls))
	}
}

func TestNewWithClient(t *testing.T) {
	t.Parallel()
	client := converseFunc(func(context.Context, *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
		t.Fatal("constructor must not invoke Converse")
		return nil, nil
	})
	t.Run("invalid settings", func(t *testing.T) {
		settings := config.Default()
		settings.Region = ""
		if _, err := NewWithClient(settings, client); err == nil {
			t.Fatal("accepted invalid settings")
		}
	})
	t.Run("missing client", func(t *testing.T) {
		if _, err := NewWithClient(config.Default(), nil); err == nil {
			t.Fatal("accepted a missing client")
		}
	})
	t.Run("valid settings", func(t *testing.T) {
		transcriber, err := NewWithClient(config.Default(), client)
		if err != nil {
			t.Fatal(err)
		}
		if err := transcriber.Close(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestTranscribeRequest(t *testing.T) {
	t.Parallel()
	settings := config.Default()
	settings.Quality = "fast"
	settings.Glossary = "  Bedrock, SDK  "
	chunk := spokenChunk(9.125)
	calls := 0
	client := converseFunc(func(ctx context.Context, input *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
		calls++
		if aws.ToString(input.ModelId) != settings.ModelID() {
			t.Errorf("model = %q", aws.ToString(input.ModelId))
		}
		if len(input.Messages) != 1 || input.Messages[0].Role != types.ConversationRoleUser {
			t.Fatalf("unexpected messages: %#v", input.Messages)
		}
		content := input.Messages[0].Content
		if len(content) != 2 {
			t.Fatalf("content blocks = %d, want 2", len(content))
		}
		prompt, ok := content[0].(*types.ContentBlockMemberText)
		if !ok || !strings.Contains(prompt.Value, "Transcribe this audio verbatim") ||
			!strings.Contains(prompt.Value, "Bedrock, SDK.") ||
			!strings.Contains(prompt.Value, "Never insert these words when they are not audible") {
			t.Error("the request lost the transcription and optional spelling instructions")
		}
		audioBlock, ok := content[1].(*types.ContentBlockMemberAudio)
		if !ok || audioBlock.Value.Format != types.AudioFormatMp3 {
			t.Fatal("MP3 audio block was not sent")
		}
		source, ok := audioBlock.Value.Source.(*types.AudioSourceMemberBytes)
		if !ok || !slices.Equal(source.Value, chunk.Audio) {
			t.Fatal("audio bytes were changed")
		}
		if aws.ToInt32(input.InferenceConfig.MaxTokens) != 292 || aws.ToFloat32(input.InferenceConfig.Temperature) != 0 {
			t.Errorf("unexpected inference configuration: %#v", input.InferenceConfig)
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > callTimeout {
			t.Fatal("Converse has no bounded deadline")
		}
		response := completeResponse("  Olá. Uso a API do Bedrock e TypeScript.  ", types.StopReasonEndTurn)
		response.Usage = &types.TokenUsage{
			InputTokens:           aws.Int32(17),
			OutputTokens:          aws.Int32(9),
			TotalTokens:           aws.Int32(26),
			CacheReadInputTokens:  aws.Int32(0),
			CacheWriteInputTokens: aws.Int32(2),
			CacheDetails: []types.CacheDetail{
				{InputTokens: aws.Int32(2), Ttl: types.CacheTTL("5m")},
			},
		}
		return response, nil
	})
	transcriber, err := NewWithClient(settings, client)
	if err != nil {
		t.Fatal(err)
	}
	result, err := transcriber.Transcribe(t.Context(), chunk)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || result.Text != "Olá. Uso a API do Bedrock e TypeScript." {
		t.Fatalf("calls = %d, text = %q", calls, result.Text)
	}
	wantUsage := map[string]any{
		"inputTokens":           int32(17),
		"outputTokens":          int32(9),
		"totalTokens":           int32(26),
		"cacheReadInputTokens":  int32(0),
		"cacheWriteInputTokens": int32(2),
		"cacheDetails":          []map[string]any{{"ttl": "5m", "inputTokens": int32(2)}},
	}
	actualUsage := make(map[string]any)
	for name, value := range result.Usage {
		switch name {
		case "first_text_seconds", "first_text_stage", "response_finalize_seconds", "model_request_seconds", "model_requests":
		default:
			actualUsage[name] = value
		}
	}
	if !reflect.DeepEqual(actualUsage, wantUsage) {
		t.Errorf("usage = %#v, want %#v plus the measured phases", result.Usage, wantUsage)
	}
	if result.ElapsedSeconds < 0 {
		t.Errorf("elapsed seconds = %v", result.ElapsedSeconds)
	}
}

func TestTranscribeTokenBudget(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		duration float64
		tokens   int32
	}{
		{name: "short audio", duration: 2, tokens: 256},
		{name: "lower boundary", duration: 8, tokens: 256},
		{name: "round upward", duration: 8.01, tokens: 257},
		{name: "thirty seconds", duration: 30, tokens: 960},
		{name: "upper boundary", duration: 256, tokens: 8192},
		{name: "five minutes", duration: 300, tokens: 8192},
		{name: "large finite duration", duration: math.MaxFloat64, tokens: 8192},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			client := converseFunc(func(_ context.Context, input *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
				if got := aws.ToInt32(input.InferenceConfig.MaxTokens); got != tc.tokens {
					t.Errorf("max tokens = %d, want %d", got, tc.tokens)
				}
				return completeResponse("[SEM_FALA]", types.StopReasonEndTurn), nil
			})
			transcriber, err := NewWithClient(config.Default(), client)
			if err != nil {
				t.Fatal(err)
			}
			result, err := transcriber.Transcribe(t.Context(), spokenChunk(tc.duration))
			if err != nil {
				t.Fatal(err)
			}
			if result.Text != "" {
				t.Errorf("no-speech text = %q", result.Text)
			}
		})
	}
}

func TestShortSpeechRetriesEmptyAnswerWithReview(t *testing.T) {
	settings := config.Default()
	settings.Glossary = "Voxtral, Bedrock"
	calls := 0
	client := converseFunc(func(_ context.Context, input *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
		calls++
		prompt := input.Messages[0].Content[0].(*types.ContentBlockMemberText).Value
		if calls == 1 && !strings.Contains(prompt, "[SEM_FALA]") {
			t.Error("the first request lost the normal no-speech instruction")
		}
		if calls == 2 && (strings.Contains(prompt, "[SEM_FALA]") ||
			!strings.Contains(prompt, "Voxtral, Bedrock")) {
			t.Error("the retry kept the sentinel or lost the glossary")
		}
		response := completeResponse("[SEM_FALA]", types.StopReasonEndTurn)
		response.Usage = &types.TokenUsage{
			InputTokens: aws.Int32(100), OutputTokens: aws.Int32(7), TotalTokens: aws.Int32(107),
		}
		if calls == 2 {
			response = completeResponse("Oi.", types.StopReasonEndTurn)
			response.Usage = &types.TokenUsage{
				InputTokens: aws.Int32(90), OutputTokens: aws.Int32(3), TotalTokens: aws.Int32(93),
			}
		}
		return response, nil
	})
	transcriber, err := NewWithClient(settings, client)
	if err != nil {
		t.Fatal(err)
	}
	result, err := transcriber.Transcribe(t.Context(), spokenChunk(4.3))
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || result.Text != "Oi." || result.ReviewReason == "" ||
		result.Usage["inputTokens"] != int64(190) ||
		result.Usage["outputTokens"] != int64(10) ||
		result.Usage["totalTokens"] != int64(200) {
		t.Fatalf("short speech retry = %+v, calls=%d", result, calls)
	}
}

func TestShortSpeechKeepsFirstRecognizedText(t *testing.T) {
	calls := 0
	client := converseFunc(func(context.Context, *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
		calls++
		return completeResponse("Olá.", types.StopReasonEndTurn), nil
	})
	transcriber, err := NewWithClient(config.Default(), client)
	if err != nil {
		t.Fatal(err)
	}
	result, err := transcriber.Transcribe(t.Context(), spokenChunk(2))
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || result.Text != "Olá." || result.ReviewReason != "" {
		t.Fatalf("recognized short speech was retried: %+v calls=%d", result, calls)
	}
}

func TestShortSpeechRetryFailureIsReported(t *testing.T) {
	cause := errors.New("retry failed")
	calls := 0
	client := converseFunc(func(context.Context, *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
		calls++
		if calls == 1 {
			return completeResponse("[SEM_FALA]", types.StopReasonEndTurn), nil
		}
		return nil, cause
	})
	transcriber, err := NewWithClient(config.Default(), client)
	if err != nil {
		t.Fatal(err)
	}
	result, err := transcriber.Transcribe(t.Context(), spokenChunk(2))
	if calls != 2 || !errors.Is(err, cause) || result.Text != "" {
		t.Fatalf("retry failure was hidden: result=%+v err=%v calls=%d", result, err, calls)
	}
}

func TestShortSpeechCancellationSkipsRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := 0
	client := converseFunc(func(context.Context, *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
		calls++
		cancel()
		return completeResponse("[SEM_FALA]", types.StopReasonEndTurn), nil
	})
	transcriber, err := NewWithClient(config.Default(), client)
	if err != nil {
		t.Fatal(err)
	}
	_, err = transcriber.Transcribe(ctx, spokenChunk(2))
	if calls != 1 || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled short speech started a retry: calls=%d err=%v", calls, err)
	}
}

func TestLongSpeechKeepsEmptyAnswerForReview(t *testing.T) {
	calls := 0
	client := converseFunc(func(context.Context, *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
		calls++
		return completeResponse("[SEM_FALA]", types.StopReasonEndTurn), nil
	})
	transcriber, err := NewWithClient(config.Default(), client)
	if err != nil {
		t.Fatal(err)
	}
	result, err := transcriber.Transcribe(t.Context(), spokenChunk(10))
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || result.Text != "" || result.ReviewReason != "" {
		t.Fatalf("long speech changed retry policy: %+v calls=%d", result, calls)
	}
}

func TestTranscribeSilence(t *testing.T) {
	t.Parallel()
	client := converseFunc(func(context.Context, *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
		t.Fatal("silence reached AWS")
		return nil, nil
	})
	transcriber, err := NewWithClient(config.Default(), client)
	if err != nil {
		t.Fatal(err)
	}
	result, err := transcriber.Transcribe(t.Context(), audio.Chunk{Duration: 30})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "" || len(result.Usage) != 0 || result.ElapsedSeconds != 0 {
		t.Errorf("silence result = %#v", result)
	}
}

func TestTranscribeInvalidChunk(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		chunk audio.Chunk
	}{
		{name: "missing audio", chunk: audio.Chunk{HasSpeech: true, Duration: 30}},
		{name: "zero duration", chunk: spokenChunk(0)},
		{name: "negative duration", chunk: spokenChunk(-1)},
		{name: "NaN duration", chunk: spokenChunk(math.NaN())},
		{name: "infinite duration", chunk: spokenChunk(math.Inf(1))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			client := converseFunc(func(context.Context, *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
				t.Fatal("invalid chunk reached AWS")
				return nil, nil
			})
			transcriber, err := NewWithClient(config.Default(), client)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := transcriber.Transcribe(t.Context(), tc.chunk); err == nil {
				t.Fatal("accepted an invalid spoken chunk")
			}
		})
	}
}

func TestTranscribeResponse(t *testing.T) {
	t.Parallel()
	var nilMessage *types.ConverseOutputMemberMessage
	var nilText *types.ContentBlockMemberText
	cases := []struct {
		name     string
		response *bedrockruntime.ConverseOutput
		wantText string
		wantErr  bool
	}{
		{name: "end turn", response: completeResponse("  Olá.  ", types.StopReasonEndTurn), wantText: "Olá."},
		{name: "stop sequence", response: completeResponse("Olá.", types.StopReasonStopSequence), wantText: "Olá."},
		{name: "trim no speech", response: completeResponse(" \n[SEM_FALA]\t ", types.StopReasonEndTurn)},
		{name: "sentinel in actual text", response: completeResponse("Disse [SEM_FALA] no exemplo.", types.StopReasonEndTurn), wantText: "Disse [SEM_FALA] no exemplo."},
		{name: "truncated", response: completeResponse("Um texto incompleto.", types.StopReasonMaxTokens), wantErr: true},
		{name: "filtered", response: completeResponse("Texto.", types.StopReason("content_filtered")), wantErr: true},
		{name: "guardrail", response: completeResponse("Texto.", types.StopReason("guardrail_intervened")), wantErr: true},
		{name: "tool request", response: completeResponse("Texto.", types.StopReason("tool_use")), wantErr: true},
		{name: "unknown reason", response: completeResponse("Texto.", types.StopReason("new_reason")), wantErr: true},
		{name: "missing reason", response: completeResponse("Texto.", ""), wantErr: true},
		{name: "nil response", wantErr: true},
		{name: "missing output", response: &bedrockruntime.ConverseOutput{StopReason: types.StopReasonEndTurn}, wantErr: true},
		{name: "nil message", response: &bedrockruntime.ConverseOutput{StopReason: types.StopReasonEndTurn, Output: nilMessage}, wantErr: true},
		{name: "valid empty text", response: completeResponse("", types.StopReasonEndTurn)},
		{name: "valid whitespace text", response: completeResponse(" \n\t", types.StopReasonEndTurn)},
		{
			name: "nontext output",
			response: &bedrockruntime.ConverseOutput{
				StopReason: types.StopReasonEndTurn,
				Output: &types.ConverseOutputMemberMessage{Value: types.Message{
					Content: []types.ContentBlock{&types.ContentBlockMemberAudio{}, nil, nilText},
				}},
			},
			wantErr: true,
		},
		{
			name: "missing content",
			response: &bedrockruntime.ConverseOutput{
				StopReason: types.StopReasonEndTurn,
				Output:     &types.ConverseOutputMemberMessage{Value: types.Message{}},
			},
			wantErr: true,
		},
		{
			name: "join text without rewriting",
			response: &bedrockruntime.ConverseOutput{
				StopReason: types.StopReasonEndTurn,
				Output: &types.ConverseOutputMemberMessage{Value: types.Message{
					Content: []types.ContentBlock{
						&types.ContentBlockMemberText{Value: "  Eu uso AWS. "},
						&types.ContentBlockMemberAudio{},
						nilText,
						&types.ContentBlockMemberText{Value: "\n\nUse TypeScript.  "},
					},
				}},
			},
			wantText: "Eu uso AWS. \n\nUse TypeScript.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			client := converseFunc(func(context.Context, *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
				return tc.response, nil
			})
			transcriber, err := NewWithClient(config.Default(), client)
			if err != nil {
				t.Fatal(err)
			}
			result, err := transcriber.Transcribe(t.Context(), spokenChunk(30))
			if tc.wantErr {
				if !errors.Is(err, ErrIncompleteResponse) {
					t.Fatalf("error = %v, want ErrIncompleteResponse", err)
				}
				if result.Text != "" {
					t.Errorf("incomplete response returned successful text %q", result.Text)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if result.Text != tc.wantText {
				t.Errorf("text = %q, want %q", result.Text, tc.wantText)
			}
			if result.Usage == nil {
				t.Error("missing usage should produce an empty map")
			}
		})
	}
}

func TestTranscribeClientError(t *testing.T) {
	t.Parallel()
	cause := &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "private diagnostic"}
	client := converseFunc(func(context.Context, *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
		return nil, cause
	})
	transcriber, err := NewWithClient(config.Default(), client)
	if err != nil {
		t.Fatal(err)
	}
	result, err := transcriber.Transcribe(t.Context(), spokenChunk(30))
	var apiError smithy.APIError
	if !errors.Is(err, cause) || !errors.As(err, &apiError) {
		t.Fatalf("SDK cause was not preserved: %v", err)
	}
	if !strings.Contains(err.Error(), "bedrock:InvokeModel") || strings.Contains(err.Error(), "private diagnostic") {
		t.Errorf("message = %q", err.Error())
	}
	if result.Text != "" {
		t.Errorf("AWS error returned successful text %q", result.Text)
	}
}

func TestTranscribeCancellation(t *testing.T) {
	t.Parallel()
	t.Run("already cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		client := converseFunc(func(context.Context, *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
			t.Fatal("cancelled transcription reached AWS")
			return nil, nil
		})
		transcriber, err := NewWithClient(config.Default(), client)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := transcriber.Transcribe(ctx, spokenChunk(30)); !errors.Is(err, context.Canceled) {
			t.Errorf("error = %v, want cancellation", err)
		}
	})
	t.Run("cancelled during successful response", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		client := converseFunc(func(context.Context, *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
			cancel()
			return completeResponse("Resposta antiga.", types.StopReasonEndTurn), nil
		})
		transcriber, err := NewWithClient(config.Default(), client)
		if err != nil {
			t.Fatal(err)
		}
		result, err := transcriber.Transcribe(ctx, spokenChunk(30))
		if !errors.Is(err, context.Canceled) || result.Text != "" {
			t.Errorf("cancelled result = %#v, error = %v", result, err)
		}
	})
	t.Run("earlier caller deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		wantDeadline, _ := ctx.Deadline()
		client := converseFunc(func(ctx context.Context, _ *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
			gotDeadline, _ := ctx.Deadline()
			if !gotDeadline.Equal(wantDeadline) {
				t.Errorf("deadline = %s, want %s", gotDeadline, wantDeadline)
			}
			return completeResponse("Olá.", types.StopReasonEndTurn), nil
		})
		transcriber, err := NewWithClient(config.Default(), client)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := transcriber.Transcribe(ctx, spokenChunk(30)); err != nil {
			t.Fatal(err)
		}
	})
}

func TestTranscribeTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client := converseFunc(func(ctx context.Context, _ *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		})
		transcriber, err := NewWithClient(config.Default(), client)
		if err != nil {
			t.Fatal(err)
		}
		started := time.Now()
		if _, err := transcriber.Transcribe(t.Context(), spokenChunk(30)); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("error = %v, want deadline", err)
		}
		if elapsed := time.Since(started); elapsed != callTimeout {
			t.Errorf("timeout = %s, want %s", elapsed, callTimeout)
		}
	})
}

type closeClient struct {
	count atomic.Int32
	err   error
}

func (c *closeClient) Converse(context.Context, *bedrockruntime.ConverseInput, ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error) {
	return completeResponse("Olá.", types.StopReasonEndTurn), nil
}

func (c *closeClient) Close() error {
	c.count.Add(1)
	return c.err
}

func TestClose(t *testing.T) {
	t.Parallel()
	cause := errors.New("close failed")
	client := &closeClient{err: cause}
	transcriber, err := NewWithClient(config.Default(), client)
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for range 12 {
		group.Go(func() {
			if err := transcriber.Close(); !errors.Is(err, cause) {
				t.Errorf("close error = %v", err)
			}
		})
	}
	group.Wait()
	if got := client.count.Load(); got != 1 {
		t.Errorf("client closed %d times, want 1", got)
	}
}

func TestVerifyCancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := Verify(ctx, config.Default()); !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want cancellation", err)
	}
}

func TestVerify(t *testing.T) {
	cases := []struct {
		name, body, wantMessage string
		status                  int
	}{
		{
			name:   "valid mock identity",
			status: http.StatusOK,
			body: `<GetCallerIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/">` +
				`<GetCallerIdentityResult><Arn>arn:aws:iam::111111111111:role/TestRole</Arn>` +
				`<UserId>CLOUD-TEST</UserId><Account>111111111111</Account></GetCallerIdentityResult>` +
				`<ResponseMetadata><RequestId>cloud-test</RequestId></ResponseMetadata></GetCallerIdentityResponse>`,
		},
		{
			name:   "expired mock identity",
			status: http.StatusForbidden,
			body: `<ErrorResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><Error>` +
				`<Type>Sender</Type><Code>ExpiredToken</Code><Message>private-mock-diagnostic</Message>` +
				`</Error><RequestId>cloud-test</RequestId></ErrorResponse>`,
			wantMessage: "login AWS expirou",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateAWS(t, "[profile verify-test]\naws_access_key_id = CLOUD-VERIFY-TEST-KEY\n"+
				"aws_secret_access_key = cloud-verify-test-secret\n")
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if err := r.ParseForm(); err != nil {
					t.Error(err)
				}
				if r.Form.Get("Action") != "GetCallerIdentity" {
					t.Errorf("unexpected mock STS action: %s", r.Form.Get("Action"))
				}
				if !strings.Contains(r.Header.Get("Authorization"), "Credential=CLOUD-VERIFY-TEST-KEY/") {
					t.Error("Verify did not use the explicitly selected mock profile")
				}
				w.Header().Set("Content-Type", "text/xml")
				w.WriteHeader(tc.status)
				if _, err := io.WriteString(w, tc.body); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			t.Setenv("AWS_ENDPOINT_URL_STS", server.URL)
			settings := config.Default()
			settings.Profile = "verify-test"
			err := Verify(t.Context(), settings)
			if tc.wantMessage == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				var apiError smithy.APIError
				if err == nil || !strings.Contains(err.Error(), tc.wantMessage) || !errors.As(err, &apiError) {
					t.Fatalf("mock Verify error = %v", err)
				}
				if strings.Contains(err.Error(), "private-mock-diagnostic") {
					t.Error("Verify exposed the raw mock AWS diagnostic")
				}
			}
			if got := calls.Load(); got != 1 {
				t.Errorf("mock STS calls = %d, want 1", got)
			}
		})
	}
}
