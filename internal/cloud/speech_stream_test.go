package cloud

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ai-talker/internal/audio"
	"ai-talker/internal/config"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/aws/aws-sdk-go-v2/service/transcribestreaming"
	streamtypes "github.com/aws/aws-sdk-go-v2/service/transcribestreaming/types"
)

func pcmSpeech(t *testing.T, seconds int) audio.Chunk {
	t.Helper()
	pcm := make([]byte, seconds*audio.SampleRate*audio.SampleBytes)
	for index := range pcm {
		pcm[index] = byte(index%123 + 1)
	}
	wav, err := audio.EncodeWAV(t.Context(), pcm)
	if err != nil {
		t.Fatal(err)
	}
	return audio.Chunk{Audio: wav, Format: audio.FormatWAV, Duration: float64(seconds), HasSpeech: true}
}

type fakeTranscribeAudioStream struct {
	events chan streamtypes.TranscriptResultStream
	closed atomic.Int32
	err    error
	send   func(context.Context, streamtypes.AudioStream) error
	mu     sync.Mutex
	pcm    []byte
	frames []int
	ended  bool
}

func (s *fakeTranscribeAudioStream) Events() <-chan streamtypes.TranscriptResultStream {
	return s.events
}
func (s *fakeTranscribeAudioStream) Err() error { return s.err }
func (s *fakeTranscribeAudioStream) Close() error {
	s.closed.Add(1)
	return nil
}
func (s *fakeTranscribeAudioStream) Send(ctx context.Context, event streamtypes.AudioStream) error {
	if s.send != nil {
		return s.send(ctx, event)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	item, ok := event.(*streamtypes.AudioStreamMemberAudioEvent)
	if !ok {
		return errors.New("evento sem amostras PCM")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(item.Value.AudioChunk) == 0 {
		s.ended = true
	} else {
		s.pcm = append(s.pcm, item.Value.AudioChunk...)
		s.frames = append(s.frames, len(item.Value.AudioChunk))
	}
	return nil
}

func transcribeEvent(id, text string, start float64, partial bool) streamtypes.TranscriptResultStream {
	return &streamtypes.TranscriptResultStreamMemberTranscriptEvent{Value: streamtypes.TranscriptEvent{
		Transcript: &streamtypes.Transcript{Results: []streamtypes.Result{{
			ResultId: aws.String(id), StartTime: start, IsPartial: partial,
			Alternatives: []streamtypes.Alternative{{Transcript: aws.String(text)}},
		}}},
	}}
}

func transcribeEventWithWords(id, text string, start, end float64, partial bool, speaker string) streamtypes.TranscriptResultStream {
	return &streamtypes.TranscriptResultStreamMemberTranscriptEvent{Value: streamtypes.TranscriptEvent{
		Transcript: &streamtypes.Transcript{Results: []streamtypes.Result{{
			ResultId: aws.String(id), StartTime: start, EndTime: end, IsPartial: partial,
			Alternatives: []streamtypes.Alternative{{Transcript: aws.String(text), Items: []streamtypes.Item{
				{StartTime: start, EndTime: start + 0.2, Type: streamtypes.ItemTypePronunciation, Speaker: aws.String(speaker), Content: aws.String("Olá")},
				{Type: streamtypes.ItemTypePunctuation, Content: aws.String(",")},
				{StartTime: start + 0.2, EndTime: end, Type: streamtypes.ItemTypePronunciation, Speaker: aws.String(speaker), Content: aws.String("mundo")},
				{Type: streamtypes.ItemTypePunctuation, Content: aws.String(".")},
			}}},
		}}},
	}}
}

func TestTranscribeStreamingRevisesPartialsAndSendsHeaderlessUniformPCM(t *testing.T) {
	t.Parallel()
	chunk := pcmSpeech(t, 3)
	stream := &fakeTranscribeAudioStream{events: make(chan streamtypes.TranscriptResultStream, 8)}
	stream.events <- transcribeEvent("first", "Olá mund", 0, true)
	stream.events <- transcribeEvent("second", "Segunda frase.", 2, false)
	stream.events <- transcribeEvent("first", "Olá mundo.", 0, false)
	stream.events <- transcribeEvent("first", "Revisão antiga.", 0, true)
	close(stream.events)
	var partials []string
	engine := &transcribeStreaming{settings: config.Default(),
		start: func(ctx context.Context, input *transcribestreaming.StartStreamTranscriptionInput) (transcribeAudioStream, error) {
			if input.LanguageCode != "pt-BR" || input.IdentifyLanguage || input.IdentifyMultipleLanguages ||
				input.MediaEncoding != streamtypes.MediaEncodingPcm || aws.ToInt32(input.MediaSampleRateHertz) != 16000 ||
				!input.EnablePartialResultsStabilization || input.PartialResultsStability != streamtypes.PartialResultsStabilityMedium {
				t.Error("a conexão não solicitou reconhecimento PCM em português", input)
			}
			if _, ok := ctx.Deadline(); !ok {
				t.Error("o reconhecimento não recebeu um prazo")
			}
			return stream, nil
		}}
	result, err := engine.TranscribeStream(t.Context(), chunk, func(text string) { partials = append(partials, text) })
	if err != nil || result.Text != "Olá mundo. Segunda frase." || stream.closed.Load() != 1 ||
		!slices.Equal(partials, []string{"Olá mund", "Olá mund Segunda frase.", result.Text}) {
		t.Fatal("as revisões duplicaram ou perderam texto", result, partials, err)
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if !stream.ended || !bytes.Equal(stream.pcm, chunk.Audio[44:]) || len(stream.frames) != 30 {
		t.Fatal("a conexão perdeu amostras ou enviou o cabeçalho WAV", len(stream.pcm), stream.frames)
	}
	for _, frame := range stream.frames {
		if frame != 3200 {
			t.Fatal("os blocos PCM não têm 100 ms", frame)
		}
	}
}

func TestTranscribeStreamingOrdinaryTranscriptionUsesFinalResultsAndAutoLanguage(t *testing.T) {
	t.Parallel()
	settings := config.Default()
	settings.Cloud.Language = "auto"
	stream := &fakeTranscribeAudioStream{events: make(chan streamtypes.TranscriptResultStream, 2)}
	stream.events <- transcribeEvent("one", "Provisório", 0, true)
	stream.events <- transcribeEvent("one", "Texto final.", 0, false)
	close(stream.events)
	engine := &transcribeStreaming{settings: settings,
		start: func(_ context.Context, input *transcribestreaming.StartStreamTranscriptionInput) (transcribeAudioStream, error) {
			if !input.IdentifyMultipleLanguages || input.LanguageCode != "" ||
				aws.ToString(input.LanguageOptions) != "pt-BR,en-US,es-US,fr-FR,de-DE" ||
				input.EnablePartialResultsStabilization || input.PartialResultsStability != "" {
				t.Error("a transcrição completa configurou idioma/estabilização incompatível")
			}
			return stream, nil
		}}
	result, err := engine.Transcribe(t.Context(), pcmSpeech(t, 1))
	if err != nil || result.Text != "Texto final." || result.Usage["audio_seconds"] != float64(1) {
		t.Fatal("um resultado provisório virou resultado final", result, err)
	}
}

func TestTranscribeStreamingReturnsFinalTimedSpeakerItems(t *testing.T) {
	t.Parallel()
	settings := config.Default()
	settings.SpeakerLabels = true
	stream := &fakeTranscribeAudioStream{events: make(chan streamtypes.TranscriptResultStream, 2)}
	stream.events <- transcribeEventWithWords("one", "Olá, mundo.", 0.1, 0.7, false, "spk_0")
	close(stream.events)
	engine := &transcribeStreaming{settings: settings,
		start: func(_ context.Context, input *transcribestreaming.StartStreamTranscriptionInput) (transcribeAudioStream, error) {
			if !input.ShowSpeakerLabel {
				t.Error("o stream não habilitou diarização")
			}
			return stream, nil
		}}
	result, err := engine.Transcribe(t.Context(), pcmSpeech(t, 1))
	if err != nil || result.Text != "Olá, mundo." || len(result.Segments) != 2 ||
		result.Segments[0].Speaker != "spk_0" || result.Segments[0].Text != "Olá," ||
		result.Segments[1].Speaker != "spk_0" || result.Segments[1].Text != "mundo." {
		t.Fatalf("Transcribe Streaming perdeu timestamps, locutor ou pontuação: %+v %v", result, err)
	}
}

func TestTranscribeStreamingRejectsUnfinishedResponseAndPropagatesSendFailure(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"partial", "reader", "sender"} {
		t.Run(stage, func(t *testing.T) {
			stream := &fakeTranscribeAudioStream{events: make(chan streamtypes.TranscriptResultStream, 1)}
			failure := errors.New("conexão interrompida")
			switch stage {
			case "partial":
				stream.events <- transcribeEvent("one", "Ainda provisório.", 0, true)
			case "reader":
				stream.err = failure
			case "sender":
				stream.send = func(context.Context, streamtypes.AudioStream) error { return failure }
			}
			close(stream.events)
			engine := &transcribeStreaming{settings: config.Default(),
				start: func(context.Context, *transcribestreaming.StartStreamTranscriptionInput) (transcribeAudioStream, error) {
					return stream, nil
				}}
			result, err := engine.Transcribe(t.Context(), pcmSpeech(t, 1))
			if err == nil || result.Text != "" || stream.closed.Load() != 1 {
				t.Fatal("um stream incompleto foi publicado ou não foi liberado", result, err)
			}
			if stage == "partial" && !errors.Is(err, ErrIncompleteResponse) ||
				stage != "partial" && !errors.Is(err, failure) {
				t.Fatal("a causa da falha foi perdida", err)
			}
		})
	}
}

func TestTranscribeStreamingCancellationStopsSenderAndClosesConnection(t *testing.T) {
	t.Parallel()
	started, stopped := make(chan struct{}), make(chan struct{})
	stream := &fakeTranscribeAudioStream{events: make(chan streamtypes.TranscriptResultStream)}
	stream.send = func(ctx context.Context, _ streamtypes.AudioStream) error {
		close(started)
		<-ctx.Done()
		close(stopped)
		return ctx.Err()
	}
	engine := &transcribeStreaming{settings: config.Default(),
		start: func(context.Context, *transcribestreaming.StartStreamTranscriptionInput) (transcribeAudioStream, error) {
			return stream, nil
		}}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	chunk := pcmSpeech(t, 1)
	go func() { _, err := engine.Transcribe(ctx, chunk); done <- err }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("o envio não começou")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || stream.closed.Load() != 1 {
			t.Fatal("cancelar não encerrou a conexão", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("o envio ficou preso após cancelar")
	}
	select {
	case <-stopped:
	default:
		t.Fatal("o reconhecimento retornou com uma goroutine de envio ativa")
	}
}

type fakeSonicStream struct {
	events chan types.InvokeModelWithBidirectionalStreamOutput
	closed atomic.Int32
	err    error
	mu     sync.Mutex
	inputs []map[string]json.RawMessage
}

func (s *fakeSonicStream) Events() <-chan types.InvokeModelWithBidirectionalStreamOutput {
	return s.events
}
func (s *fakeSonicStream) Err() error { return s.err }
func (s *fakeSonicStream) Close() error {
	s.closed.Add(1)
	return nil
}
func (s *fakeSonicStream) Send(ctx context.Context, input types.InvokeModelWithBidirectionalStreamInput) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	chunk, ok := input.(*types.InvokeModelWithBidirectionalStreamInputMemberChunk)
	if !ok {
		return errors.New("evento de entrada inválido")
	}
	var event struct {
		Event map[string]json.RawMessage `json:"event"`
	}
	if err := json.Unmarshal(chunk.Value.Bytes, &event); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inputs = append(s.inputs, event.Event)
	return nil
}

func sonicEvent(t *testing.T, name string, body any) types.InvokeModelWithBidirectionalStreamOutput {
	t.Helper()
	data, err := json.Marshal(map[string]any{"event": map[string]any{name: body}})
	if err != nil {
		t.Fatal(err)
	}
	return &types.InvokeModelWithBidirectionalStreamOutputMemberChunk{
		Value: types.BidirectionalOutputPayloadPart{Bytes: data},
	}
}

func sonicText(t *testing.T, stream *fakeSonicStream, id, role, stage, text string) {
	t.Helper()
	stream.events <- sonicEvent(t, "contentStart", map[string]string{
		"contentId": id, "role": role, "type": "TEXT", "additionalModelFields": `{"generationStage":"` + stage + `"}`,
	})
	stream.events <- sonicEvent(t, "textOutput", map[string]string{"contentId": id, "content": text})
	stream.events <- sonicEvent(t, "contentEnd", map[string]string{"contentId": id})
}

func TestSonicRecognizesOnlyFinalUserSpeechAndSendsPacedAudioProtocol(t *testing.T) {
	t.Parallel()
	settings := config.Default()
	settings.Cloud.ModelID = config.NovaSonic
	stream := &fakeSonicStream{events: make(chan types.InvokeModelWithBidirectionalStreamOutput, 20)}
	sonicText(t, stream, "speculative", "USER", "SPECULATIVE", "Hipótese.")
	sonicText(t, stream, "assistant", "ASSISTANT", "FINAL", "Translation and reply.")
	sonicText(t, stream, "actual", "USER", "FINAL", "Esta é a fala original.")
	stream.events <- sonicEvent(t, "usageEvent", map[string]any{"totalInputTokens": 15, "totalOutputTokens": 7, "totalTokens": 22})
	close(stream.events)
	var partials []string
	var cadence time.Duration
	engine := &sonicTranscriber{settings: settings,
		start: func(_ context.Context, model string) (sonicStream, error) {
			if model != config.NovaSonic {
				t.Error("a conexão recebeu o modelo errado", model)
			}
			return stream, nil
		},
		wait: func(ctx context.Context, duration time.Duration) error {
			cadence += duration
			return ctx.Err()
		}}
	chunk := pcmSpeech(t, 1)
	result, err := engine.TranscribeStream(t.Context(), chunk, func(text string) { partials = append(partials, text) })
	if err != nil || result.Text != "Esta é a fala original." ||
		!slices.Equal(partials, []string{result.Text}) || result.Usage["total_tokens"] != float64(22) ||
		cadence != 3*time.Second || stream.closed.Load() != 1 {
		t.Fatal("respostas/traduções entraram na transcrição ou a conexão não terminou", result, partials, cadence, err)
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	var pcm []byte
	var names []string
	for _, input := range stream.inputs {
		for name, data := range input {
			names = append(names, name)
			if name == "audioInput" {
				var frame struct {
					Content string `json:"content"`
				}
				if err := json.Unmarshal(data, &frame); err != nil {
					t.Fatal(err)
				}
				decoded, err := base64.StdEncoding.DecodeString(frame.Content)
				if err != nil || len(decoded) > 1024 || len(decoded)%2 != 0 {
					t.Fatal("bloco de áudio inválido", err)
				}
				pcm = append(pcm, decoded...)
			}
		}
	}
	if len(pcm) != 3*audio.SampleRate*audio.SampleBytes || !bytes.Equal(pcm[:len(chunk.Audio)-44], chunk.Audio[44:]) ||
		!bytes.Equal(pcm[len(chunk.Audio)-44:], make([]byte, 2*audio.SampleRate*audio.SampleBytes)) ||
		!slices.Equal(names[:6], []string{"sessionStart", "promptStart", "contentStart", "textInput", "contentEnd", "contentStart"}) ||
		!slices.Equal(names[len(names)-3:], []string{"contentEnd", "promptEnd", "sessionEnd"}) {
		t.Fatal("o protocolo perdeu amostras, a cauda silenciosa ou o fechamento", names)
	}
}

func TestSonicUnfinishedUserContentNeverBecomesFinalTranscript(t *testing.T) {
	t.Parallel()
	stream := &fakeSonicStream{events: make(chan types.InvokeModelWithBidirectionalStreamOutput, 2)}
	stream.events <- sonicEvent(t, "contentStart", map[string]string{
		"contentId": "actual", "role": "USER", "type": "TEXT", "additionalModelFields": `{"generationStage":"FINAL"}`,
	})
	stream.events <- sonicEvent(t, "textOutput", map[string]string{"contentId": "actual", "content": "Fala interrompida."})
	close(stream.events)
	engine := &sonicTranscriber{settings: config.Default(),
		start: func(context.Context, string) (sonicStream, error) { return stream, nil },
		wait:  func(context.Context, time.Duration) error { return nil }}
	result, err := engine.Transcribe(t.Context(), pcmSpeech(t, 1))
	if !errors.Is(err, ErrIncompleteResponse) || result.Text != "" || stream.closed.Load() != 1 {
		t.Fatal("o conteúdo sem fechamento virou transcrição completa", result, err)
	}
}

func TestSonicCancellationStopsPacingAndReleasesStream(t *testing.T) {
	t.Parallel()
	stream := &fakeSonicStream{events: make(chan types.InvokeModelWithBidirectionalStreamOutput)}
	pacing := make(chan struct{})
	engine := &sonicTranscriber{settings: config.Default(),
		start: func(context.Context, string) (sonicStream, error) { return stream, nil },
		wait: func(ctx context.Context, _ time.Duration) error {
			close(pacing)
			<-ctx.Done()
			return ctx.Err()
		}}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	result := make(chan error, 1)
	chunk := pcmSpeech(t, 1)
	go func() { _, err := engine.Transcribe(ctx, chunk); result <- err }()
	select {
	case <-pacing:
	case <-time.After(2 * time.Second):
		t.Fatal("a conexão não começou a enviar áudio")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) || stream.closed.Load() != 1 {
			t.Fatal("cancelar não encerrou a cadência/conexão", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a conexão permaneceu aberta após cancelar")
	}
}

func TestSpeechPCMRejectsInvalidHeadersAndDurationBeforeOpeningConnection(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"format", "rate", "duration", "truncated", "stereo"} {
		t.Run(stage, func(t *testing.T) {
			chunk := pcmSpeech(t, 1)
			switch stage {
			case "format":
				chunk.Format = audio.FormatMP3
			case "rate":
				chunk.Audio[24] = 0
			case "duration":
				chunk.Duration = 2
			case "truncated":
				chunk.Audio = chunk.Audio[:20]
			case "stereo":
				chunk.Audio[22] = 2
			}
			if _, err := speechPCM(chunk); err == nil {
				t.Fatal("áudio incompatível foi aceito", stage)
			}
		})
	}
	if _, err := speechPCM(pcmSpeech(t, 1)); err != nil {
		t.Fatal(err)
	}
}
