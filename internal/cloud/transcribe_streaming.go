package cloud

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"ai-talker/internal/audio"
	"ai-talker/internal/config"
	"ai-talker/internal/transcript"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/transcribestreaming"
	streamtypes "github.com/aws/aws-sdk-go-v2/service/transcribestreaming/types"
)

type transcribeAudioStream interface {
	Send(context.Context, streamtypes.AudioStream) error
	Events() <-chan streamtypes.TranscriptResultStream
	Err() error
	Close() error
}

type transcriptionStreamStart func(context.Context, *transcribestreaming.StartStreamTranscriptionInput) (transcribeAudioStream, error)

type transcribeStreaming struct {
	ownedCloudClient
	settings config.Settings
	start    transcriptionStreamStart
}

var _ transcript.Transcriber = (*transcribeStreaming)(nil)
var _ transcript.StreamingTranscriber = (*transcribeStreaming)(nil)

func newTranscribeStreaming(settings config.Settings, client *transcribestreaming.Client, closeHTTP func()) *transcribeStreaming {
	return &transcribeStreaming{
		ownedCloudClient: ownedCloudClient{closeHTTP: closeHTTP},
		settings:         settings,
		start: func(ctx context.Context, input *transcribestreaming.StartStreamTranscriptionInput) (transcribeAudioStream, error) {
			response, err := client.StartStreamTranscription(ctx, input)
			if err != nil {
				return nil, err
			}
			if response == nil || response.GetStream() == nil {
				return nil, incompleteSpeech("Amazon Transcribe")
			}
			return response.GetStream(), nil
		},
	}
}

func (t *transcribeStreaming) Transcribe(ctx context.Context, chunk audio.Chunk) (transcript.ChunkResult, error) {
	return t.TranscribeStream(ctx, chunk, nil)
}

func (t *transcribeStreaming) TranscribeStream(ctx context.Context, chunk audio.Chunk, onPartial func(string)) (result transcript.ChunkResult, err error) {
	if err := ctx.Err(); err != nil {
		return result, transcribeError(err, t.settings.Profile)
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
	input := &transcribestreaming.StartStreamTranscriptionInput{
		MediaEncoding: streamtypes.MediaEncodingPcm, MediaSampleRateHertz: aws.Int32(audio.SampleRate),
		EnablePartialResultsStabilization: onPartial != nil,
		ShowSpeakerLabel:                  t.settings.SpeakerLabels,
	}
	if onPartial != nil {
		input.PartialResultsStability = streamtypes.PartialResultsStabilityMedium
	}
	if language := t.settings.Cloud.TranscribeLanguage(); language == "auto" {
		input.IdentifyMultipleLanguages = true
		input.LanguageOptions = aws.String("pt-BR,en-US,es-US,fr-FR,de-DE")
	} else {
		input.LanguageCode = streamtypes.LanguageCode(language)
	}
	finishOpen := measurePhase(ctx, "request_open_seconds")
	stream, err := t.start(ctx, input)
	finishOpen()
	if err != nil {
		return result, transcribeError(err, t.settings.Profile)
	}
	if stream == nil {
		return result, incompleteSpeech("Amazon Transcribe")
	}
	senderDone := make(chan error, 1)
	go func() { senderDone <- sendTranscriptionAudio(ctx, stream, pcm) }()
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
			err = transcribeError(closeErr, t.settings.Profile)
		}
	}()
	type segment struct {
		id       string
		start    float64
		text     string
		partial  bool
		segments []transcript.SpeechSegment
	}
	segments := make(map[string]segment)
	text := func(finalOnly bool) string {
		ordered := make([]segment, 0, len(segments))
		for _, part := range segments {
			if !finalOnly || !part.partial {
				ordered = append(ordered, part)
			}
		}
		slices.SortFunc(ordered, func(a, b segment) int {
			if a.start < b.start {
				return -1
			}
			if a.start > b.start {
				return 1
			}
			return strings.Compare(a.id, b.id)
		})
		parts := make([]string, 0, len(ordered))
		for _, part := range ordered {
			if part.text != "" {
				parts = append(parts, part.text)
			}
		}
		return strings.Join(parts, " ")
	}
	var lastPartial string
	events := stream.Events()
	for {
		select {
		case <-ctx.Done():
			return result, transcribeError(ctx.Err(), t.settings.Profile)
		case sendErr := <-sent:
			senderFinished = true
			sent = nil
			if sendErr != nil {
				return result, transcribeError(sendErr, t.settings.Profile)
			}
		case event, open := <-events:
			if !open {
				if streamErr := stream.Err(); streamErr != nil {
					return result, transcribeError(streamErr, t.settings.Profile)
				}
				if !senderFinished {
					select {
					case sendErr := <-senderDone:
						senderFinished = true
						if sendErr != nil {
							return result, transcribeError(sendErr, t.settings.Profile)
						}
					case <-ctx.Done():
						return result, transcribeError(ctx.Err(), t.settings.Profile)
					}
				}
				for _, part := range segments {
					if part.partial && part.text != "" {
						return result, incompleteSpeech("Amazon Transcribe")
					}
				}
				result.Text = text(true)
				for _, part := range segments {
					if !part.partial {
						result.Segments = append(result.Segments, part.segments...)
					}
				}
				slices.SortStableFunc(result.Segments, func(a, b transcript.SpeechSegment) int {
					if a.Start < b.Start {
						return -1
					}
					if a.Start > b.Start {
						return 1
					}
					if a.End < b.End {
						return -1
					}
					if a.End > b.End {
						return 1
					}
					return 0
				})
				result.ElapsedSeconds = time.Since(started).Seconds()
				result.Usage = map[string]any{"audio_seconds": chunk.Duration, "sessions": 1}
				return result, nil
			}
			item, ok := event.(*streamtypes.TranscriptResultStreamMemberTranscriptEvent)
			if !ok || item == nil || item.Value.Transcript == nil {
				continue
			}
			for _, recognized := range item.Value.Transcript.Results {
				if len(recognized.Alternatives) == 0 {
					continue
				}
				id := aws.ToString(recognized.ResultId)
				if id == "" {
					id = fmt.Sprintf("%.6f", recognized.StartTime)
				}
				if previous, exists := segments[id]; exists && !previous.partial && recognized.IsPartial {
					continue
				}
				clean, _ := transcript.CleanLiveText(aws.ToString(recognized.Alternatives[0].Transcript))
				parts := transcribeStreamSegments(recognized, clean, chunk.Duration)
				segments[id] = segment{
					id: id, start: recognized.StartTime, text: clean,
					partial: recognized.IsPartial, segments: parts,
				}
				stage := "final"
				if recognized.IsPartial {
					stage = "partial"
				}
				firstText.observe(clean, stage)
			}
			if current := text(false); current != "" && current != lastPartial && onPartial != nil {
				lastPartial = current
				onPartial(current)
			}
		}
	}
}

func transcribeStreamSegments(result streamtypes.Result, text string, duration float64) []transcript.SpeechSegment {
	if len(result.Alternatives) == 0 {
		return nil
	}
	items := result.Alternatives[0].Items
	segments := make([]transcript.SpeechSegment, 0, len(items))
	for _, item := range items {
		content := strings.TrimSpace(aws.ToString(item.Content))
		if content == "" {
			continue
		}
		if strings.EqualFold(string(item.Type), "punctuation") {
			if len(segments) > 0 {
				segments[len(segments)-1].Text += content
			}
			continue
		}
		start, end := item.StartTime, item.EndTime
		if start < 0 || end <= start || start > duration+0.25 || end > duration+1 {
			continue
		}
		end = min(end, duration)
		if end <= start {
			continue
		}
		speaker := strings.TrimSpace(aws.ToString(item.Speaker))
		if len(speaker) > 64 || strings.ContainsAny(speaker, "\x00\r\n\t") {
			speaker = ""
		}
		segments = append(segments, transcript.SpeechSegment{
			Start: start, End: end, Speaker: speaker, Text: content,
		})
	}
	if len(segments) == 0 && strings.TrimSpace(text) != "" &&
		result.EndTime > result.StartTime && result.StartTime >= 0 &&
		result.EndTime <= duration+1 {
		segments = append(segments, transcript.SpeechSegment{
			Start: result.StartTime, End: min(result.EndTime, duration), Text: strings.TrimSpace(text),
		})
	}
	return segments
}

func sendTranscriptionAudio(ctx context.Context, stream transcribeAudioStream, pcm []byte) error {
	// Transcribe accepts prerecorded media too. Keep uniform 100 ms packets;
	// the source recorder has already accumulated the bounded clip.
	const frameBytes = audio.SampleRate * audio.SampleBytes / 10
	finishAudio := measurePhase(ctx, "audio_send_seconds")
	for len(pcm) > 0 {
		if err := ctx.Err(); err != nil {
			finishAudio()
			return err
		}
		frame := pcm[:min(frameBytes, len(pcm))]
		if err := stream.Send(ctx, &streamtypes.AudioStreamMemberAudioEvent{
			Value: streamtypes.AudioEvent{AudioChunk: frame},
		}); err != nil {
			finishAudio()
			return err
		}
		pcm = pcm[len(frame):]
	}
	finishAudio()
	// The empty audio event ends input while leaving the response reader open
	// to receive final revisions. Close belongs to the operation's owner.
	finishInput := measurePhase(ctx, "input_finalize_seconds")
	defer finishInput()
	return stream.Send(ctx, &streamtypes.AudioStreamMemberAudioEvent{Value: streamtypes.AudioEvent{}})
}
