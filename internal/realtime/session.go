package realtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

const (
	promptName       = "live-conversation"
	inputName        = "live-microphone"
	maxPCMBytes      = 64 * 1024
	maxEventBytes    = 1024 * 1024
	maxResponseBytes = 32 * 1024 * 1024
)

type command struct {
	pcm   []byte
	close bool
	ack   chan error
}

// Session owns its stream, cancellation and two joined transport goroutines.
// Callbacks must not call Close: Close waits for the callback's goroutine.
type Session struct {
	ctx           context.Context
	cancel        context.CancelFunc
	options       Options
	stream        Stream
	callback      func(Event)
	commands      chan command
	interrupt     chan string
	done          chan struct{}
	senderDone    chan error
	senderStopped chan struct{}

	mu           sync.Mutex
	closing      bool
	err          error
	responses    []Response
	suppressed   map[string]bool
	current      string
	wireActive   map[string]string
	wireSequence map[string]int
}

// New begins one continuous audio container. The caller supplies audio at its
// capture cadence, including silence, so the API can detect turns and barge-in.
// Ownership of stream transfers to the returned session.
func New(ctx context.Context, options Options, stream Stream, onEvent func(Event)) (*Session, error) {
	if ctx == nil {
		return nil, errors.New("informe o contexto da conversa ao vivo")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	normalized, err := normalizeOptions(options)
	if err != nil {
		return nil, err
	}
	if stream == nil || reflect.ValueOf(stream).Kind() == reflect.Pointer && reflect.ValueOf(stream).IsNil() {
		return nil, errors.New("informe a conexão bidirecional do Nova Sonic")
	}
	ctx, cancel := context.WithCancel(ctx)
	s := &Session{
		ctx: ctx, cancel: cancel, options: normalized, stream: stream, callback: onEvent,
		commands: make(chan command), interrupt: make(chan string, 1),
		done: make(chan struct{}), senderDone: make(chan error, 1),
		senderStopped: make(chan struct{}),
		suppressed:    make(map[string]bool),
		wireActive:    make(map[string]string), wireSequence: make(map[string]int),
	}
	if err := s.setup(ctx); err != nil {
		cancel()
		_ = stream.Close()
		return nil, fmt.Errorf("preparar conversa Nova Sonic: %w", err)
	}
	go s.send()
	go s.receive()
	return s, nil
}

func (s *Session) setup(ctx context.Context) error {
	if err := s.sendEvent(ctx, "sessionStart", map[string]any{
		"inferenceConfiguration":     map[string]any{"maxTokens": s.options.MaxTokens, "topP": 0.9, "temperature": 0.7},
		"turnDetectionConfiguration": map[string]any{"endpointingSensitivity": "MEDIUM"},
	}); err != nil {
		return err
	}
	if err := s.sendEvent(ctx, "promptStart", map[string]any{
		"promptName": promptName, "textOutputConfiguration": map[string]any{"mediaType": "text/plain"},
		"audioOutputConfiguration": map[string]any{
			"mediaType": "audio/lpcm", "sampleRateHertz": OutputSampleRate, "sampleSizeBits": 16,
			"channelCount": 1, "voiceId": s.options.VoiceID, "encoding": "base64", "audioType": "SPEECH",
		},
	}); err != nil {
		return err
	}
	if err := s.sendText(ctx, "system", "SYSTEM", s.options.SystemPrompt); err != nil {
		return err
	}
	for i, message := range s.options.History {
		if err := s.sendText(ctx, fmt.Sprintf("history-%d", i), message.Role, message.Text); err != nil {
			return err
		}
	}
	return s.sendEvent(ctx, "contentStart", map[string]any{
		"promptName": promptName, "contentName": inputName, "type": "AUDIO", "interactive": true, "role": "USER",
		"audioInputConfiguration": map[string]any{
			"mediaType": "audio/lpcm", "sampleRateHertz": s.options.InputSampleRate, "sampleSizeBits": 16,
			"channelCount": 1, "audioType": "SPEECH", "encoding": "base64",
		},
	})
}

func (s *Session) sendText(ctx context.Context, name, role, text string) error {
	for _, item := range []struct {
		name string
		body any
	}{
		{"contentStart", map[string]any{
			"promptName": promptName, "contentName": name, "type": "TEXT", "interactive": false, "role": role,
			"textInputConfiguration": map[string]any{"mediaType": "text/plain"},
		}},
		{"textInput", map[string]any{"promptName": promptName, "contentName": name, "content": text}},
		{"contentEnd", map[string]any{"promptName": promptName, "contentName": name}},
	} {
		if err := s.sendEvent(ctx, item.name, item.body); err != nil {
			return err
		}
	}
	return nil
}

func (s *Session) sendEvent(ctx context.Context, name string, body any) error {
	payload, err := json.Marshal(map[string]any{"event": map[string]any{name: body}})
	if err != nil {
		return err
	}
	return s.stream.Send(ctx, &types.InvokeModelWithBidirectionalStreamInputMemberChunk{
		Value: types.BidirectionalInputPayloadPart{Bytes: payload},
	})
}

// SendAudio copies one mono PCM16 block. It never drops capture silently.
// Backpressure or transport failure is returned to the caller.
func (s *Session) SendAudio(ctx context.Context, pcm []byte) error {
	if ctx == nil {
		return errors.New("informe o contexto de envio do áudio")
	}
	if len(pcm)%2 != 0 || len(pcm) > maxPCMBytes {
		return errors.New("o bloco de PCM deve conter amostras completas e até 64 KiB")
	}
	if len(pcm) == 0 {
		return ctx.Err()
	}
	s.mu.Lock()
	closing := s.closing
	s.mu.Unlock()
	if closing {
		return ErrClosed
	}
	return s.submit(ctx, command{pcm: slices.Clone(pcm), ack: make(chan error, 1)})
}

func (s *Session) submit(ctx context.Context, cmd command) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.ctx.Done():
		return ErrClosed
	case <-s.senderStopped:
		return ErrClosed
	case s.commands <- cmd:
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-cmd.ack:
		return err
	case <-s.ctx.Done():
		return ErrClosed
	}
}

func (s *Session) send() {
	var err error
	defer func() {
		close(s.senderStopped)
		s.senderDone <- err
		if err != nil {
			s.mu.Lock()
			if s.err == nil {
				s.err = err
			}
			s.mu.Unlock()
			s.cancel()
		}
	}()
	for {
		select {
		case <-s.ctx.Done():
			return
		case cmd := <-s.commands:
			if cmd.close {
				for _, item := range []struct {
					name string
					body any
				}{
					{"contentEnd", map[string]any{"promptName": promptName, "contentName": inputName}},
					{"promptEnd", map[string]any{"promptName": promptName}},
					{"sessionEnd", map[string]any{}},
				} {
					if err = s.sendEvent(s.ctx, item.name, item.body); err != nil {
						break
					}
				}
				cmd.ack <- err
				return
			}
			// Approximately 32 ms at the configured rate, matching Nova's
			// recommended frame size. Cadence belongs to the capture source.
			frameBytes := s.options.InputSampleRate * 2 * 32 / 1000
			for len(cmd.pcm) > 0 {
				frame := cmd.pcm[:min(frameBytes, len(cmd.pcm))]
				err = s.sendEvent(s.ctx, "audioInput", map[string]any{
					"promptName": promptName, "contentName": inputName,
					"content": base64.StdEncoding.EncodeToString(frame),
				})
				if err != nil {
					break
				}
				cmd.pcm = cmd.pcm[len(frame):]
			}
			cmd.ack <- err
			if err != nil {
				return
			}
		}
	}
}

// Interrupt immediately stops delivering the current response for playback.
// Nova's server interrupts generation when the next speech arrives through the
// continuous audio stream. Cancel terminates transport immediately if needed.
func (s *Session) Interrupt() {
	s.mu.Lock()
	current := s.current
	complete := false
	if current != "" {
		complete = s.responses[s.responseIndex(current)].Complete
	}
	s.mu.Unlock()
	if current == "" || complete {
		return
	}
	select {
	case s.interrupt <- current:
	case <-s.done:
	default:
	}
}

func (s *Session) Cancel()               { s.cancel() }
func (s *Session) Done() <-chan struct{} { return s.done }

func (s *Session) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *Session) Snapshot() []Response {
	s.mu.Lock()
	defer s.mu.Unlock()
	responses := slices.Clone(s.responses)
	for i := range responses {
		responses[i].PCM = slices.Clone(responses[i].PCM)
	}
	return responses
}

// Close ends the open audio, prompt and session in protocol order, then waits
// for the provider to finish. A canceled close preserves already received data.
func (s *Session) Close(ctx context.Context) error {
	if ctx == nil {
		return errors.New("informe o contexto de encerramento da conversa")
	}
	s.mu.Lock()
	first := !s.closing
	s.closing = true
	s.mu.Unlock()
	if first {
		if err := s.submit(ctx, command{close: true, ack: make(chan error, 1)}); err != nil && !errors.Is(err, ErrClosed) {
			s.cancel()
			<-s.done
			return err
		}
	}
	select {
	case <-ctx.Done():
		s.cancel()
		<-s.done
		return ctx.Err()
	case <-s.done:
		return s.Err()
	}
}

func (s *Session) emit(event Event) {
	if s.callback != nil {
		s.callback(event)
	}
}

type content struct {
	id, completion, role, kind, stage string
	text                              strings.Builder
	closed                            bool
}

func (s *Session) responseIndex(id string) int {
	for i := range s.responses {
		if s.responses[i].CompletionID == id {
			return i
		}
	}
	s.responses = append(s.responses, Response{CompletionID: id, SampleRate: OutputSampleRate})
	return len(s.responses) - 1
}

func (s *Session) interruptResponse(id string) {
	if id == "" {
		return
	}
	s.mu.Lock()
	i := s.responseIndex(id)
	already := s.responses[i].Interrupted
	s.responses[i].Interrupted, s.responses[i].Complete = true, false
	s.suppressed[id] = true
	s.mu.Unlock()
	if !already {
		s.emit(Event{Kind: Interrupted, CompletionID: id, Interrupted: true})
	}
}

func (s *Session) receive() {
	var err error
	defer func() {
		s.cancel()
		closeErr := s.stream.Close()
		sendErr := <-s.senderDone
		s.mu.Lock()
		if s.err == nil {
			switch {
			case err != nil:
				s.err = err
			case sendErr != nil:
				s.err = sendErr
			case closeErr != nil:
				s.err = closeErr
			}
		}
		for i := range s.responses {
			if !s.responses[i].Complete {
				s.responses[i].Interrupted = true
			}
		}
		sessionErr := s.err
		s.mu.Unlock()
		s.emit(Event{Kind: SessionEnd, Err: sessionErr})
		close(s.done)
	}()
	blocks := make(map[string]*content)
	var order []string
	events := s.stream.Events()
	for {
		if err = s.ctx.Err(); err != nil {
			return
		}
		select {
		case <-s.ctx.Done():
			err = s.ctx.Err()
			return
		case current := <-s.interrupt:
			s.interruptResponse(current)
		case item, open := <-events:
			if !open {
				err = s.stream.Err()
				s.mu.Lock()
				closing := s.closing
				s.mu.Unlock()
				if err == nil && !closing {
					err = errors.New("o serviço encerrou a conversa ao vivo; os conteúdos recebidos foram preservados")
				}
				return
			}
			chunk, ok := item.(*types.InvokeModelWithBidirectionalStreamOutputMemberChunk)
			if !ok || chunk == nil {
				continue
			}
			if len(chunk.Value.Bytes) > maxEventBytes {
				err = errors.New("evento Nova Sonic excedeu o limite de tamanho")
				return
			}
			var envelope struct {
				Event map[string]json.RawMessage `json:"event"`
			}
			if json.Unmarshal(chunk.Value.Bytes, &envelope) != nil || len(envelope.Event) != 1 {
				err = errors.New("o serviço enviou um evento Nova Sonic inválido")
				return
			}
			for name, body := range envelope.Event {
				if err = s.accept(name, body, blocks, &order); err != nil {
					return
				}
			}
		}
	}
}
