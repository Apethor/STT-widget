package realtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

func TestVoiceAvailabilityIsSpecificToModel(t *testing.T) {
	if voices := Voices(Nova25); len(voices) != 2 || voices[0].ID != "carolina" || voices[1].ID != "leo" {
		t.Fatalf("Nova 2.5 exposed an unverified voice: %+v", voices)
	}
	if len(Voices(Nova2)) != 16 || len(Voices("unknown")) != 0 {
		t.Fatal("documented Nova 2 voice catalog changed")
	}
	if err := ValidateOptions(Options{ModelID: Nova25, VoiceID: "tiffany"}); err == nil {
		t.Fatal("Nova 2 voice silently accepted for Nova 2.5")
	}
	for _, id := range []string{"carolina", "leo"} {
		if err := ValidateOptions(Options{ModelID: Nova25, VoiceID: id}); err != nil {
			t.Fatalf("proven Nova 2.5 voice %s rejected: %v", id, err)
		}
	}
}

func TestManualInterruptRetainsItsTargetWhileAnotherUserTurnArrives(t *testing.T) {
	stream := newFakeStream()
	events := make(chan Event, 64)
	paused, resume := make(chan struct{}), make(chan struct{})
	session, err := New(t.Context(), Options{}, stream, func(event Event) {
		if event.Kind == AssistantText && event.Text == "Partial first reply" {
			select {
			case <-paused:
			default:
				close(paused)
				<-resume
			}
		}
		events <- event
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Cancel()
	emitText(t, stream, "wire", "first-user", "USER", "FINAL", "First question")
	emitText(t, stream, "wire", "first-reply", "ASSISTANT", "SPECULATIVE", "Partial first reply")
	<-paused
	session.Interrupt()
	emitText(t, stream, "wire", "next-user", "USER", "FINAL", "Next question")
	emitText(t, stream, "wire", "next-reply", "ASSISTANT", "SPECULATIVE", "Next reply")
	emitAudio(t, stream, "wire", "next-audio", []byte{1, 0, 2, 0})
	stream.push(t, "contentEnd", map[string]any{"contentId": "next-audio", "stopReason": "END_TURN"})
	close(resume)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := session.Close(ctx); err != nil {
		t.Fatal(err)
	}
	responses := session.Snapshot()
	if len(responses) != 2 || responses[0].CompletionID != "wire" || !responses[0].Interrupted ||
		responses[1].UserText != "Next question" || !responses[1].Complete || len(responses[1].PCM) != 4 {
		t.Fatalf("manual interruption switched to the newer user turn: %+v", responses)
	}
}

type fakeStream struct {
	mu         sync.Mutex
	events     chan types.InvokeModelWithBidirectionalStreamOutput
	sent       []map[string]json.RawMessage
	closeOnce  sync.Once
	closeCount int
	err        error
	blockAudio chan struct{}
	closingEOF bool
}

func newFakeStream() *fakeStream {
	return &fakeStream{events: make(chan types.InvokeModelWithBidirectionalStreamOutput, 64), closingEOF: true}
}

func (f *fakeStream) Send(ctx context.Context, input types.InvokeModelWithBidirectionalStreamInput) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var message struct {
		Event map[string]json.RawMessage `json:"event"`
	}
	if err := json.Unmarshal(input.(*types.InvokeModelWithBidirectionalStreamInputMemberChunk).Value.Bytes, &message); err != nil {
		return err
	}
	f.mu.Lock()
	f.sent = append(f.sent, message.Event)
	block := f.blockAudio
	closeSession := f.closingEOF && message.Event["sessionEnd"] != nil
	f.mu.Unlock()
	if message.Event["audioInput"] != nil && block != nil {
		close(block)
		<-ctx.Done()
		return ctx.Err()
	}
	if closeSession {
		f.finish()
	}
	return nil
}

func (f *fakeStream) Events() <-chan types.InvokeModelWithBidirectionalStreamOutput { return f.events }
func (f *fakeStream) Err() error                                                    { return f.err }
func (f *fakeStream) finish()                                                       { f.closeOnce.Do(func() { close(f.events) }) }
func (f *fakeStream) Close() error {
	f.mu.Lock()
	f.closeCount++
	f.mu.Unlock()
	f.finish()
	return nil
}

func (f *fakeStream) push(t *testing.T, name string, body any) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"event": map[string]any{name: body}})
	if err != nil {
		t.Fatal(err)
	}
	f.events <- &types.InvokeModelWithBidirectionalStreamOutputMemberChunk{
		Value: types.BidirectionalOutputPayloadPart{Bytes: payload},
	}
}

func emitText(t *testing.T, stream *fakeStream, completion, id, role, stage, text string) {
	t.Helper()
	stream.push(t, "contentStart", map[string]any{
		"completionId": completion, "contentId": id, "type": "TEXT", "role": role,
		"additionalModelFields": `{"generationStage":"` + stage + `"}`,
	})
	stream.push(t, "textOutput", map[string]any{"completionId": completion, "contentId": id, "content": text})
	stream.push(t, "contentEnd", map[string]any{"completionId": completion, "contentId": id, "type": "TEXT", "stopReason": "END_TURN"})
}

func emitAudio(t *testing.T, stream *fakeStream, completion, id string, pcm []byte) {
	t.Helper()
	stream.push(t, "contentStart", map[string]any{
		"completionId": completion, "contentId": id, "type": "AUDIO", "role": "ASSISTANT",
		"audioOutputConfiguration": map[string]any{
			"mediaType": "audio/lpcm", "sampleRateHertz": 24000, "sampleSizeBits": 16, "channelCount": 1,
		},
	})
	stream.push(t, "audioOutput", map[string]any{
		"completionId": completion, "contentId": id, "content": base64.StdEncoding.EncodeToString(pcm),
	})
}

func awaitEvent(t *testing.T, events <-chan Event, kind Kind, completion string) Event {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event := <-events:
			if event.Kind == kind && (completion == "" || event.CompletionID == completion) {
				return event
			}
		case <-timer.C:
			t.Fatalf("event %s / %s did not arrive", kind, completion)
			return Event{}
		}
	}
}

func TestContinuousConversationKeepsOneInputAndTwoResponses(t *testing.T) {
	stream := newFakeStream()
	events := make(chan Event, 64)
	session, err := New(t.Context(), Options{History: []Message{{"user", "Previous question"}, {"assistant", "Previous reply"}}},
		stream, func(event Event) { events <- event })
	if err != nil {
		t.Fatal(err)
	}
	defer session.Cancel()
	for _, pcm := range [][]byte{{1, 0, 2, 0}, {3, 0, 4, 0}} {
		if err := session.SendAudio(t.Context(), pcm); err != nil {
			t.Fatal(err)
		}
	}
	for _, completion := range []string{"turn-1", "turn-2"} {
		stream.push(t, "completionStart", map[string]any{"completionId": completion})
		emitText(t, stream, completion, completion+"-user-preview", "USER", "SPECULATIVE", "must not become dictation")
		emitText(t, stream, completion, completion+"-user", "USER", "FINAL", "Hello")
		emitText(t, stream, completion, completion+"-preview", "ASSISTANT", "SPECULATIVE", "Unspoken preview")
		emitAudio(t, stream, completion, completion+"-audio", []byte{5, 0, 6, 0})
		emitText(t, stream, completion, completion+"-spoken", "ASSISTANT", "FINAL", "Spoken reply")
		stream.push(t, "contentEnd", map[string]any{"contentId": completion + "-audio", "stopReason": "END_TURN"})
		stream.push(t, "completionEnd", map[string]any{"completionId": completion, "stopReason": "END_TURN"})
		if event := awaitEvent(t, events, ResponseEnd, completion); !event.Final || event.Text != "Spoken reply" {
			t.Fatalf("response end = %+v", event)
		}
	}
	snapshot := session.Snapshot()
	if len(snapshot) != 2 {
		t.Fatalf("responses = %+v", snapshot)
	}
	for _, response := range snapshot {
		if response.UserText != "Hello" || response.Text != "Spoken reply" || !response.Complete ||
			!slices.Equal(response.PCM, []byte{5, 0, 6, 0}) || response.SampleRate != 24000 {
			t.Fatalf("response = %+v", response)
		}
	}
	snapshot[0].PCM[0] = 99
	if session.Snapshot()[0].PCM[0] == 99 {
		t.Fatal("snapshot shares mutable audio with session")
	}
	if err := session.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	var audioStarts int
	var names []string
	var historyBeforeAudio bool
	for _, event := range stream.sent {
		for name, raw := range event {
			names = append(names, name)
			if name == "contentStart" {
				var value struct {
					Type        string `json:"type"`
					ContentName string `json:"contentName"`
				}
				_ = json.Unmarshal(raw, &value)
				if value.Type == "AUDIO" {
					audioStarts++
					historyBeforeAudio = slices.Contains(names, "textInput")
				}
			}
		}
	}
	if audioStarts != 1 || !historyBeforeAudio || !slices.Equal(names[len(names)-3:], []string{"contentEnd", "promptEnd", "sessionEnd"}) {
		t.Fatalf("input sequence = %v, audioStarts=%d", names, audioStarts)
	}
}

func TestInterruptionPreservesPartialAudioAndContinuesWithNextTurn(t *testing.T) {
	stream := newFakeStream()
	events := make(chan Event, 64)
	session, err := New(t.Context(), Options{}, stream, func(event Event) { events <- event })
	if err != nil {
		t.Fatal(err)
	}
	defer session.Cancel()
	stream.push(t, "completionStart", map[string]any{"completionId": "first"})
	emitText(t, stream, "first", "first-text", "ASSISTANT", "SPECULATIVE", "Partial reply")
	emitAudio(t, stream, "first", "first-audio", []byte{7, 0, 8, 0})
	audioEvent := awaitEvent(t, events, AssistantAudio, "first")
	audioEvent.PCM[0] = 99
	session.Interrupt()
	awaitEvent(t, events, Interrupted, "first")
	stream.push(t, "audioOutput", map[string]any{"contentId": "first-audio", "content": base64.StdEncoding.EncodeToString([]byte{9, 0})})
	stream.push(t, "completionEnd", map[string]any{"completionId": "first", "stopReason": "INTERRUPTED"})
	awaitEvent(t, events, ResponseEnd, "first")
	stream.push(t, "completionStart", map[string]any{"completionId": "second"})
	emitAudio(t, stream, "second", "second-audio", []byte{10, 0})
	awaitEvent(t, events, AssistantAudio, "second")
	snapshot := session.Snapshot()
	if !snapshot[0].Interrupted || snapshot[0].Complete || !slices.Equal(snapshot[0].PCM, []byte{7, 0, 8, 0}) ||
		snapshot[0].Text != "Partial reply" || !slices.Equal(snapshot[1].PCM, []byte{10, 0}) {
		t.Fatalf("interrupted responses = %+v", snapshot)
	}
}

func TestRepeatedWireCompletionIDCreatesSeparateSpokenTurns(t *testing.T) {
	stream := newFakeStream()
	events := make(chan Event, 64)
	session, err := New(t.Context(), Options{}, stream, func(event Event) { events <- event })
	if err != nil {
		t.Fatal(err)
	}
	defer session.Cancel()
	wireID := "same-wire-completion"
	stream.push(t, "completionStart", map[string]any{"completionId": wireID})
	for i, logicalID := range []string{wireID, wireID + "/2"} {
		suffix := string(rune('1' + i))
		emitText(t, stream, wireID, "user-"+suffix, "USER", "FINAL", "Question "+suffix)
		emitText(t, stream, wireID, "preview-"+suffix, "ASSISTANT", "SPECULATIVE", "Preview "+suffix)
		emitAudio(t, stream, wireID, "audio-"+suffix, []byte{byte(i + 1), 0})
		stream.push(t, "contentEnd", map[string]any{"contentId": "audio-" + suffix, "stopReason": "END_TURN"})
		awaitEvent(t, events, ResponseEnd, logicalID)
		// Actual spoken text may arrive after the audio response ends.
		emitText(t, stream, wireID, "final-"+suffix, "ASSISTANT", "FINAL", "Spoken "+suffix)
		awaitEvent(t, events, AssistantText, logicalID)
	}
	stream.push(t, "completionEnd", map[string]any{"completionId": wireID, "stopReason": "END_TURN"})
	if err := session.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	responses := session.Snapshot()
	if len(responses) != 2 {
		t.Fatalf("logical responses = %+v", responses)
	}
	for i, response := range responses {
		suffix := string(rune('1' + i))
		if response.CompletionID != []string{wireID, wireID + "/2"}[i] || response.UserText != "Question "+suffix ||
			response.Text != "Spoken "+suffix || !response.Complete || !slices.Equal(response.PCM, []byte{byte(i + 1), 0}) {
			t.Fatalf("logical turn %d = %+v", i, response)
		}
	}
}

func TestServerBargeInFlushesOutput(t *testing.T) {
	stream := newFakeStream()
	events := make(chan Event, 64)
	session, err := New(t.Context(), Options{}, stream, func(event Event) { events <- event })
	if err != nil {
		t.Fatal(err)
	}
	defer session.Cancel()
	emitAudio(t, stream, "first", "audio", []byte{7, 0})
	awaitEvent(t, events, AssistantAudio, "first")
	emitText(t, stream, "first", "interrupt", "ASSISTANT", "SPECULATIVE", `{"interrupted":true}`)
	awaitEvent(t, events, Interrupted, "first")
	if snapshot := session.Snapshot(); len(snapshot) != 1 || snapshot[0].Text != "" || !snapshot[0].Interrupted {
		t.Fatalf("barge-in = %+v", snapshot)
	}
}

func TestManualInterruptThenServerSignalKeepsNextTurnPlayable(t *testing.T) {
	stream := newFakeStream()
	events := make(chan Event, 64)
	session, err := New(t.Context(), Options{}, stream, func(event Event) { events <- event })
	if err != nil {
		t.Fatal(err)
	}
	defer session.Cancel()
	wireID := "shared"
	emitText(t, stream, wireID, "user-1", "USER", "FINAL", "First question")
	emitAudio(t, stream, wireID, "audio-1", []byte{1, 0})
	awaitEvent(t, events, AssistantAudio, wireID)
	session.Interrupt()
	awaitEvent(t, events, Interrupted, wireID)
	emitText(t, stream, wireID, "service-signal", "ASSISTANT", "SPECULATIVE", `{"interrupted":true}`)
	emitText(t, stream, wireID, "user-2", "USER", "FINAL", "Next question")
	emitAudio(t, stream, wireID, "audio-2", []byte{2, 0})
	awaitEvent(t, events, AssistantAudio, wireID+"/2")
	responses := session.Snapshot()
	if len(responses) != 2 || !responses[0].Interrupted || responses[1].Interrupted ||
		responses[1].UserText != "Next question" || !slices.Equal(responses[1].PCM, []byte{2, 0}) {
		t.Fatalf("next turn after interruption = %+v", responses)
	}
}

func TestCancellationUnblocksTransportAndJoinsSession(t *testing.T) {
	stream := newFakeStream()
	stream.blockAudio = make(chan struct{})
	session, err := New(t.Context(), Options{}, stream, nil)
	if err != nil {
		t.Fatal(err)
	}
	sendResult := make(chan error, 1)
	go func() { sendResult <- session.SendAudio(t.Context(), []byte{1, 0}) }()
	select {
	case <-stream.blockAudio:
	case <-time.After(3 * time.Second):
		t.Fatal("audio transport did not start")
	}
	session.Cancel()
	select {
	case <-session.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("canceled session leaked its goroutines")
	}
	if err := <-sendResult; !errors.Is(err, ErrClosed) && !errors.Is(err, context.Canceled) {
		t.Fatalf("send result = %v", err)
	}
	if !errors.Is(session.Err(), context.Canceled) {
		t.Fatalf("session result = %v", session.Err())
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if stream.closeCount != 1 {
		t.Fatalf("stream closes = %d", stream.closeCount)
	}
}

func TestInvalidEventFailsWithoutDiscardingAlreadyReceivedAudio(t *testing.T) {
	stream := newFakeStream()
	events := make(chan Event, 64)
	session, err := New(t.Context(), Options{}, stream, func(event Event) { events <- event })
	if err != nil {
		t.Fatal(err)
	}
	emitAudio(t, stream, "first", "audio", []byte{1, 0})
	awaitEvent(t, events, AssistantAudio, "first")
	stream.push(t, "audioOutput", map[string]any{"contentId": "audio", "content": "not base64"})
	event := awaitEvent(t, events, SessionEnd, "")
	<-session.Done()
	if event.Err == nil || !slices.Equal(session.Snapshot()[0].PCM, []byte{1, 0}) || !session.Snapshot()[0].Interrupted {
		t.Fatalf("failed session = %v / %+v", event.Err, session.Snapshot())
	}
}

func TestConversationValidationAndClosedInput(t *testing.T) {
	for _, options := range []Options{
		{ModelID: "text-model"}, {VoiceID: "invented"}, {InputSampleRate: 48000},
		{MaxTokens: 8192}, {History: []Message{{Role: "TOOL", Text: "wrong role"}}},
	} {
		if err := ValidateOptions(options); err == nil {
			t.Errorf("accepted invalid options %+v", options)
		}
	}
	var typedNil *fakeStream
	if _, err := New(t.Context(), Options{}, typedNil, nil); err == nil {
		t.Fatal("accepted typed nil stream")
	}
	stream := newFakeStream()
	session, err := New(t.Context(), Options{}, stream, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.SendAudio(t.Context(), []byte{1}); err == nil {
		t.Fatal("accepted incomplete PCM sample")
	}
	if err := session.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := session.SendAudio(t.Context(), []byte{1, 0}); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed input = %v", err)
	}
}
