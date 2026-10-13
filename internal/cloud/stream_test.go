package cloud

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ai-talker/internal/config"
	"ai-talker/internal/transcript"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

type transcriptionStream struct {
	events chan types.ConverseStreamOutput
	err    error
	closed atomic.Int32
}

type rejectedStreamClient struct {
	ConverseClient
	streamCalls int
}

func (c *rejectedStreamClient) ConverseStream(
	context.Context, *bedrockruntime.ConverseStreamInput, ...func(*bedrockruntime.Options),
) (*bedrockruntime.ConverseStreamOutput, error) {
	c.streamCalls++
	return nil, ErrOutputLimit
}

func (s *transcriptionStream) Events() <-chan types.ConverseStreamOutput { return s.events }
func (s *transcriptionStream) Err() error                                { return s.err }
func (s *transcriptionStream) Close() error {
	s.closed.Add(1)
	return nil
}

func textDelta(text string) types.ConverseStreamOutput {
	return &types.ConverseStreamOutputMemberContentBlockDelta{Value: types.ContentBlockDeltaEvent{
		Delta: &types.ContentBlockDeltaMemberText{Value: text},
	}}
}

func TestTranscriptionStreamReportsAccumulatedTextBeforeCompletionAndCloses(t *testing.T) {
	t.Parallel()
	stream := &transcriptionStream{events: make(chan types.ConverseStreamOutput, 4)}
	stream.events <- textDelta("Olá")
	partial := make(chan string, 4)
	finished := make(chan struct{}, 1)
	var text string
	var usage *types.TokenUsage
	var err error
	transcriber := &transcriber{settings: config.Default()}
	go func() {
		text, usage, err = transcriber.readTranscriptionStream(t.Context(), stream, func(text string) { partial <- text }, 2)
		finished <- struct{}{}
	}()
	select {
	case first := <-partial:
		if first != "Olá" {
			t.Fatalf("first partial = %q", first)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("partial text waited for the full response")
	}
	select {
	case <-finished:
		t.Fatal("unfinished stream was reported as complete")
	default:
	}
	stream.events <- textDelta(", mundo.")
	stream.events <- &types.ConverseStreamOutputMemberMessageStop{Value: types.MessageStopEvent{StopReason: types.StopReasonEndTurn}}
	stream.events <- &types.ConverseStreamOutputMemberMetadata{Value: types.ConverseStreamMetadataEvent{
		Usage: &types.TokenUsage{InputTokens: aws.Int32(20), OutputTokens: aws.Int32(5), TotalTokens: aws.Int32(25)},
	}}
	close(stream.events)
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not terminate")
	}
	if err != nil || text != "Olá, mundo." || aws.ToInt32(usage.TotalTokens) != 25 ||
		<-partial != text || stream.closed.Load() != 1 {
		t.Fatalf("text=%q usage=%v closed=%d error=%v", text, usage, stream.closed.Load(), err)
	}
}

func TestFirstTextTimerRecordsOnlyTheFirstPartial(t *testing.T) {
	t.Parallel()
	timer := &firstTextTimer{started: time.Now()}
	var received []string
	partial := timer.wrap(func(text string) { received = append(received, text) })
	time.Sleep(5 * time.Millisecond)
	partial("Primeiro texto.")
	first := timer.elapsed
	time.Sleep(5 * time.Millisecond)
	partial("Texto final.")
	usage := timer.record(nil)
	if len(received) != 2 || first <= 0 || usage["first_text_seconds"] != first {
		t.Fatalf("a métrica não preservou o primeiro parcial: %v / %+v", received, usage)
	}
}

func TestTranscriptionStreamRejectsTruncationAndReaderFailures(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		reason types.StopReason
		err    error
	}{
		{name: "no terminal event"},
		{name: "token limit", reason: types.StopReasonMaxTokens},
		{name: "connection lost", reason: types.StopReasonEndTurn, err: errors.New("conexão interrompida")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			stream := &transcriptionStream{events: make(chan types.ConverseStreamOutput, 2), err: tc.err}
			stream.events <- textDelta("Texto ainda provisório.")
			if tc.reason != "" {
				stream.events <- &types.ConverseStreamOutputMemberMessageStop{Value: types.MessageStopEvent{StopReason: tc.reason}}
			}
			close(stream.events)
			var partial []string
			transcriber := &transcriber{settings: config.Default()}
			text, _, err := transcriber.readTranscriptionStream(t.Context(), stream, func(text string) { partial = append(partial, text) }, 2)
			if err == nil || text != "" || !slices.Equal(partial, []string{"Texto ainda provisório."}) || stream.closed.Load() != 1 {
				t.Fatalf("incomplete stream became a final result: %q / %v", text, err)
			}
		})
	}
}

func TestTranscriptionStreamHidesNoSpeechSentinelAndStopsOnCancellation(t *testing.T) {
	t.Parallel()
	stream := &transcriptionStream{events: make(chan types.ConverseStreamOutput, 4)}
	for _, delta := range []string{"[", "SEM_", "FALA]"} {
		stream.events <- textDelta(delta)
	}
	stream.events <- &types.ConverseStreamOutputMemberMessageStop{Value: types.MessageStopEvent{StopReason: types.StopReasonEndTurn}}
	close(stream.events)
	transcriber := &transcriber{settings: config.Default()}
	text, _, err := transcriber.readTranscriptionStream(t.Context(), stream, func(text string) {
		t.Errorf("a no-speech control token reached the editor: %q", text)
	}, 2)
	if err != nil || text != "" || stream.closed.Load() != 1 {
		t.Fatalf("no-speech stream = %q / %v", text, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	stream = &transcriptionStream{events: make(chan types.ConverseStreamOutput)}
	_, _, err = transcriber.readTranscriptionStream(ctx, stream, nil, 2)
	if err == nil || !strings.Contains(err.Error(), "cancel") || stream.closed.Load() != 1 {
		t.Fatal("cancellation did not release the response stream")
	}
}

func TestTranscriptionStreamRejectsGrossContinuationBeforePublishingIt(t *testing.T) {
	t.Parallel()
	stream := &transcriptionStream{events: make(chan types.ConverseStreamOutput, 1)}
	stream.events <- textDelta(strings.Repeat("Inventado. ", 60))
	transcriber := &transcriber{settings: config.Default()}
	text, _, err := transcriber.readTranscriptionStream(t.Context(), stream, func(text string) {
		t.Errorf("implausible continuation reached the editor: %q", text)
	}, 2)
	if err == nil || text != "" || stream.closed.Load() != 1 {
		t.Fatal("a gross continuation became a usable short transcript")
	}
}

func TestStreamRequestUsesABoundedPromptAndOutputBudget(t *testing.T) {
	t.Parallel()
	settings := config.Default()
	settings.Glossary = "This custom spelling hint must not prime live speech."
	client := converseFunc(func(_ context.Context, input *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
		if _, ok := input.Messages[0].Content[0].(*types.ContentBlockMemberAudio); !ok {
			t.Fatal("the live audio must precede its transcription instruction")
		}
		prompt := input.Messages[0].Content[1].(*types.ContentBlockMemberText).Value
		if strings.Contains(prompt, settings.Glossary) || len(prompt) > 640 || prompt != streamTranscriptionPrompt() ||
			aws.ToInt32(input.InferenceConfig.MaxTokens) != 256 {
			t.Error("short stream used the batch prompt or an unbounded output budget")
		}
		return completeResponse("Hello. This is a short.", types.StopReasonEndTurn), nil
	})
	clientTranscriber, err := NewWithClient(settings, client)
	if err != nil {
		t.Fatal(err)
	}
	result, err := clientTranscriber.(transcript.StreamingTranscriber).TranscribeStream(t.Context(), spokenChunk(2), func(string) {})
	if err != nil || result.Text != "Hello. This is a short." {
		t.Fatalf("bounded stream changed the recognized speech: %+v / %v", result, err)
	}
}

func TestStreamGrossContinuationRetriesOnceWithoutExpandingItsBudget(t *testing.T) {
	t.Parallel()
	calls := 0
	client := converseFunc(func(_ context.Context, input *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
		calls++
		if aws.ToInt32(input.InferenceConfig.MaxTokens) != 256 {
			t.Error("retry increased the budget for invented words")
		}
		if calls == 1 {
			return completeResponse(strings.Repeat("Continuação inventada. ", 60), types.StopReasonEndTurn), nil
		}
		return completeResponse("Minha fala correta.", types.StopReasonEndTurn), nil
	})
	engine, err := NewWithClient(config.Default(), client)
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.(transcript.StreamingTranscriber).TranscribeStream(t.Context(), spokenChunk(2), func(string) {})
	if err != nil || result.Text != "Minha fala correta." || calls != 2 {
		t.Fatalf("live recognition did not recover from an invented continuation: %+v / %v / %d", result, err, calls)
	}
}

func TestStreamOutputLimitRetriesOnceWithMoreRoomAndKeepsOnlyCompleteText(t *testing.T) {
	t.Parallel()
	var budgets []int32
	client := converseFunc(func(_ context.Context, input *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
		budgets = append(budgets, aws.ToInt32(input.InferenceConfig.MaxTokens))
		response := completeResponse("Olá, esta frase ficou completa.", types.StopReasonEndTurn)
		if len(budgets) == 1 {
			response = completeResponse("Olá, esta frase ficou", types.StopReasonMaxTokens)
		}
		response.Usage = &types.TokenUsage{InputTokens: aws.Int32(20), OutputTokens: aws.Int32(10), TotalTokens: aws.Int32(30)}
		return response, nil
	})
	clientTranscriber, err := NewWithClient(config.Default(), client)
	if err != nil {
		t.Fatal(err)
	}
	result, err := clientTranscriber.(transcript.StreamingTranscriber).TranscribeStream(t.Context(), spokenChunk(2), func(string) {})
	if err != nil || result.Text != "Olá, esta frase ficou completa." ||
		!slices.Equal(budgets, []int32{256, 1024}) || result.Usage["totalTokens"] != int32(60) {
		t.Fatalf("output-limit retry lost text, limits or usage: %+v / %v / %v", result, budgets, err)
	}
}

func TestStreamOutputLimitRecoversThroughACompleteConverseResponse(t *testing.T) {
	t.Parallel()
	batchCalls := 0
	client := &rejectedStreamClient{ConverseClient: converseFunc(func(
		_ context.Context, input *bedrockruntime.ConverseInput,
	) (*bedrockruntime.ConverseOutput, error) {
		batchCalls++
		if aws.ToInt32(input.InferenceConfig.MaxTokens) != 1024 {
			t.Error("recovery did not expand the exhausted output budget")
		}
		return completeResponse("Fala completa.", types.StopReasonEndTurn), nil
	})}
	engine, err := NewWithClient(config.Default(), client)
	if err != nil {
		t.Fatal(err)
	}
	var partials []string
	result, err := engine.(transcript.StreamingTranscriber).TranscribeStream(
		t.Context(), spokenChunk(2), func(text string) { partials = append(partials, text) },
	)
	if err != nil || result.Text != "Fala completa." || client.streamCalls != 1 || batchCalls != 1 ||
		!slices.Equal(partials, []string{result.Text}) {
		t.Fatalf("Stream recovery exposed an incomplete response or retried indefinitely: %+v / %v / %v",
			result, partials, err)
	}
}

func TestTranscriptionOutputLimitRetryIsBoundedAndPreservesItsCause(t *testing.T) {
	t.Parallel()
	for _, duration := range []float64{2, 120, 300} {
		t.Run((time.Duration(duration) * time.Second).String(), func(t *testing.T) {
			t.Parallel()
			calls := 0
			client := converseFunc(func(_ context.Context, input *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
				calls++
				if budget := aws.ToInt32(input.InferenceConfig.MaxTokens); budget < 256 || budget > 8192 {
					t.Fatalf("invalid output budget: %d", budget)
				}
				return completeResponse("Texto incompleto.", types.StopReasonMaxTokens), nil
			})
			transcriber, err := NewWithClient(config.Default(), client)
			if err != nil {
				t.Fatal(err)
			}
			result, err := transcriber.Transcribe(t.Context(), spokenChunk(duration))
			expectedCalls := 2
			if duration == 300 {
				expectedCalls = 1
			}
			if calls != expectedCalls || result.Text != "" ||
				!errors.Is(err, ErrOutputLimit) || !errors.Is(err, ErrIncompleteResponse) ||
				strings.Contains(err.Error(), "max_tokens") {
				t.Fatalf("unbounded retry or incomplete transcript: calls=%d result=%+v error=%v", calls, result, err)
			}
		})
	}
}

func TestStreamUnclearAnnotationsStayOutsideRecognizedSpeech(t *testing.T) {
	t.Parallel()
	stream := &transcriptionStream{events: make(chan types.ConverseStreamOutput, 5)}
	for _, text := range []string{"Minha fala [inaud", "ível]", ". Continuo falando."} {
		stream.events <- textDelta(text)
	}
	stream.events <- &types.ConverseStreamOutputMemberMessageStop{Value: types.MessageStopEvent{StopReason: types.StopReasonEndTurn}}
	close(stream.events)
	transcriber := &transcriber{settings: config.Default()}
	_, _, err := transcriber.readTranscriptionStream(t.Context(), stream, func(text string) {
		if strings.Contains(text, "[") || strings.Contains(text, "inaud") {
			t.Errorf("recognizer annotation reached the editor: %q", text)
		}
	}, 4)
	if err != nil {
		t.Fatal(err)
	}
	client := converseFunc(func(context.Context, *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
		return completeResponse("Minha fala [inaudível]. Continuo falando.", types.StopReasonEndTurn), nil
	})
	final, err := NewWithClient(config.Default(), client)
	if err != nil {
		t.Fatal(err)
	}
	result, err := final.(transcript.StreamingTranscriber).TranscribeStream(t.Context(), spokenChunk(4), func(string) {})
	if err != nil || strings.Contains(result.Text, "[") || result.ReviewReason == "" ||
		!strings.Contains(result.Text, "Continuo falando.") {
		t.Fatalf("uncertain speech was copied or lost its review flag: %+v / %v", result, err)
	}
}
