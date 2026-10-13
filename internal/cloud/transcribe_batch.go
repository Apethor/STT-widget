package cloud

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"ai-talker/internal/audio"
	"ai-talker/internal/config"
	"ai-talker/internal/transcript"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/transcribe"
	jobtypes "github.com/aws/aws-sdk-go-v2/service/transcribe/types"
	"github.com/aws/smithy-go"
)

type transcriptionJobs interface {
	StartTranscriptionJob(context.Context, *transcribe.StartTranscriptionJobInput, ...func(*transcribe.Options)) (*transcribe.StartTranscriptionJobOutput, error)
	GetTranscriptionJob(context.Context, *transcribe.GetTranscriptionJobInput, ...func(*transcribe.Options)) (*transcribe.GetTranscriptionJobOutput, error)
	DeleteTranscriptionJob(context.Context, *transcribe.DeleteTranscriptionJobInput, ...func(*transcribe.Options)) (*transcribe.DeleteTranscriptionJobOutput, error)
}

type transcriptionObjects interface {
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	DeleteObject(context.Context, *s3.DeleteObjectInput, ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
}

type transcribeBatch struct {
	ownedCloudClient
	settings config.Settings
	jobs     transcriptionJobs
	objects  transcriptionObjects
	wait     func(context.Context, time.Duration) error
}

var _ transcript.Transcriber = (*transcribeBatch)(nil)

func (t *transcribeBatch) Transcribe(ctx context.Context, chunk audio.Chunk) (result transcript.ChunkResult, err error) {
	if err := ctx.Err(); err != nil {
		return result, transcribeError(err, t.settings.Profile)
	}
	if !chunk.HasSpeech {
		return transcript.ChunkResult{Usage: map[string]any{}}, nil
	}
	if err := t.settings.TranscriptionSetupError(); err != nil {
		return result, err
	}
	if _, err := speechPCM(chunk); err != nil {
		return result, err
	}
	started := time.Now()
	ctx, timings := withRequestTimings(ctx)
	firstText := &firstTextTimer{started: started}
	defer func() {
		result.Usage = firstText.record(timings.record(result.Usage))
		result.ElapsedSeconds = time.Since(started).Seconds()
	}()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return result, fmt.Errorf("preparar uma transcrição temporária: %w", err)
	}
	name := "aitalker-" + hex.EncodeToString(random[:])
	prefix := "ai-talker/transcribe/" + name + "/"
	inputKey, outputKey := prefix+"audio.wav", prefix+"transcript.json"
	bucket := t.settings.Cloud.S3Bucket
	// Only this generated namespace is ever removed. A canceled or lost reply
	// can still have created the object/job, so cleanup covers ambiguous sends.
	jobRequested := false
	jobTerminal := false
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cleanupCancel()
		finishCleanup := measurePhase(cleanupCtx, "cleanup_seconds")
		defer finishCleanup()
		var cleanupErr error
		if jobRequested {
			// AWS cannot cancel/delete an in-progress job. Wait briefly for a
			// terminal state before removing its input, so a late result cannot
			// recreate an object after cleanup. On timeout, keep the namespace
			// intact and report pending cleanup instead.
			jobAbsent := false
			if !jobTerminal {
				jobAbsent, cleanupErr = t.awaitCleanupJob(cleanupCtx, name)
				jobTerminal = cleanupErr == nil && !jobAbsent
			}
			if cleanupErr == nil && !jobAbsent {
				_, cleanupErr = t.jobs.DeleteTranscriptionJob(cleanupCtx,
					&transcribe.DeleteTranscriptionJobInput{TranscriptionJobName: aws.String(name)})
			}
			if cleanupErr != nil && !jobTerminal && !jobAbsent {
				err = errors.Join(err, &Error{Cause: cleanupErr,
					Message: "A limpeza temporária de " + prefix + " no bucket S3 ficou pendente; o job ainda pode estar processando."})
				return
			}
		}
		for _, key := range []string{inputKey, outputKey} {
			_, deleteErr := t.objects.DeleteObject(cleanupCtx,
				&s3.DeleteObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
			cleanupErr = errors.Join(cleanupErr, deleteErr)
		}
		if cleanupErr != nil {
			if err != nil {
				err = errors.Join(err, transcribeError(cleanupErr, t.settings.Profile))
			} else {
				result.ReviewReason = "A transcrição terminou, mas a AWS não confirmou a limpeza dos arquivos temporários no bucket S3."
				result.Usage["cleanup_pending"] = true
			}
		}
	}()
	finishUpload := measurePhase(ctx, "audio_upload_seconds")
	_, err = t.objects.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(inputKey),
		Body: bytes.NewReader(chunk.Audio), ContentType: aws.String("audio/wav"),
	})
	finishUpload()
	if err != nil {
		return result, transcribeError(err, t.settings.Profile)
	}
	input := &transcribe.StartTranscriptionJobInput{
		TranscriptionJobName: aws.String(name),
		Media:                &jobtypes.Media{MediaFileUri: aws.String("s3://" + bucket + "/" + inputKey)},
		MediaFormat:          jobtypes.MediaFormatWav, MediaSampleRateHertz: aws.Int32(audio.SampleRate),
		OutputBucketName: aws.String(bucket), OutputKey: aws.String(outputKey),
	}
	if language := t.settings.Cloud.TranscribeLanguage(); language == "auto" {
		input.IdentifyMultipleLanguages = aws.Bool(true)
		input.LanguageOptions = []jobtypes.LanguageCode{"pt-BR", "en-US", "es-US", "fr-FR", "de-DE"}
	} else {
		input.LanguageCode = jobtypes.LanguageCode(language)
	}
	if t.settings.SpeakerLabels {
		input.Settings = &jobtypes.Settings{ShowSpeakerLabels: aws.Bool(true), MaxSpeakerLabels: aws.Int32(10)}
	}
	jobRequested = true
	finishStart := measurePhase(ctx, "job_start_seconds")
	_, err = t.jobs.StartTranscriptionJob(ctx, input)
	finishStart()
	if err != nil {
		return result, transcribeError(err, t.settings.Profile)
	}
	finishWait := measurePhase(ctx, "job_wait_seconds")
	defer finishWait()
	for attempt := 0; ; attempt++ {
		finishPoll := measurePhase(ctx, "job_poll_seconds")
		countPhase(ctx, "job_polls")
		reply, err := t.jobs.GetTranscriptionJob(ctx,
			&transcribe.GetTranscriptionJobInput{TranscriptionJobName: aws.String(name)})
		finishPoll()
		if err != nil {
			return result, transcribeError(err, t.settings.Profile)
		}
		if reply == nil || reply.TranscriptionJob == nil {
			return result, incompleteSpeech("Amazon Transcribe")
		}
		switch reply.TranscriptionJob.TranscriptionJobStatus {
		case jobtypes.TranscriptionJobStatusCompleted:
			jobTerminal = true
			finishWait()
			text, segments, err := t.readTranscript(ctx, bucket, outputKey, chunk.Duration)
			if err != nil {
				return result, err
			}
			firstText.observe(text, "final")
			return transcript.ChunkResult{
				Text: text, Segments: segments, ElapsedSeconds: time.Since(started).Seconds(),
				Usage: map[string]any{"audio_seconds": chunk.Duration, "jobs": 1},
			}, nil
		case jobtypes.TranscriptionJobStatusFailed:
			jobTerminal = true
			finishWait()
			return result, &Error{Cause: errors.New("Amazon Transcribe retornou FAILED"),
				Message: "O Amazon Transcribe não concluiu o arquivo. Confira o idioma e o acesso ao bucket S3 na mesma região e tente novamente."}
		case jobtypes.TranscriptionJobStatusQueued, jobtypes.TranscriptionJobStatusInProgress:
		default:
			return result, incompleteSpeech("Amazon Transcribe")
		}
		if err := t.wait(ctx, time.Duration(min(5, 1+attempt))*time.Second); err != nil {
			return result, transcribeError(err, t.settings.Profile)
		}
	}
}

func (t *transcribeBatch) awaitCleanupJob(ctx context.Context, name string) (absent bool, err error) {
	for {
		finishPoll := measurePhase(ctx, "cleanup_job_poll_seconds")
		countPhase(ctx, "cleanup_job_polls")
		response, err := t.jobs.GetTranscriptionJob(ctx,
			&transcribe.GetTranscriptionJobInput{TranscriptionJobName: aws.String(name)})
		finishPoll()
		if err != nil {
			var api smithy.APIError
			if errors.As(err, &api) && api.ErrorCode() == "BadRequestException" {
				message := strings.ToLower(api.ErrorMessage())
				if strings.Contains(message, "not found") || strings.Contains(message, "doesn't exist") ||
					strings.Contains(message, "couldn't find") || strings.Contains(message, "does not exist") {
					return true, nil
				}
			}
			return false, err
		}
		if response == nil || response.TranscriptionJob == nil {
			return false, incompleteSpeech("Amazon Transcribe")
		}
		switch response.TranscriptionJob.TranscriptionJobStatus {
		case jobtypes.TranscriptionJobStatusCompleted, jobtypes.TranscriptionJobStatusFailed:
			return false, nil
		case jobtypes.TranscriptionJobStatusQueued, jobtypes.TranscriptionJobStatusInProgress:
		default:
			return false, incompleteSpeech("Amazon Transcribe")
		}
		if err := t.wait(ctx, time.Second); err != nil {
			return false, err
		}
	}
}

func (t *transcribeBatch) readTranscript(ctx context.Context, bucket, key string, duration float64) (text string, segments []transcript.SpeechSegment, err error) {
	finishDownload := measurePhase(ctx, "transcript_download_seconds")
	defer finishDownload()
	response, err := t.objects.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		return "", nil, transcribeError(err, t.settings.Profile)
	}
	if response == nil || response.Body == nil {
		return "", nil, incompleteSpeech("Amazon Transcribe")
	}
	defer func() {
		if closeErr := response.Body.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("fechar a transcrição temporária: %w", closeErr))
		}
	}()
	const limit = 8 * 1024 * 1024
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return "", nil, transcribeError(err, t.settings.Profile)
	}
	if len(data) > limit {
		return "", nil, errors.New("a resposta do Amazon Transcribe excedeu o limite seguro deste trecho")
	}
	var document struct {
		Results struct {
			Transcripts []struct {
				Transcript string `json:"transcript"`
			} `json:"transcripts"`
			Items []struct {
				StartTime    json.RawMessage `json:"start_time"`
				EndTime      json.RawMessage `json:"end_time"`
				Type         string          `json:"type"`
				Alternatives []struct {
					Content string `json:"content"`
				} `json:"alternatives"`
			} `json:"items"`
			SpeakerLabels struct {
				Segments []struct {
					StartTime    json.RawMessage `json:"start_time"`
					EndTime      json.RawMessage `json:"end_time"`
					SpeakerLabel string          `json:"speaker_label"`
				} `json:"segments"`
			} `json:"speaker_labels"`
		} `json:"results"`
	}
	if json.Unmarshal(data, &document) != nil || len(document.Results.Transcripts) == 0 {
		return "", nil, incompleteSpeech("Amazon Transcribe")
	}
	var paragraphs []string
	for _, item := range document.Results.Transcripts {
		if text := strings.TrimSpace(item.Transcript); text != "" {
			paragraphs = append(paragraphs, text)
		}
	}
	if len(document.Results.Items) > 100000 || len(document.Results.SpeakerLabels.Segments) > 20000 {
		return "", nil, errors.New("a resposta do Amazon Transcribe contém segmentos demais para esta parte")
	}
	segments = make([]transcript.SpeechSegment, 0, len(document.Results.Items))
	for _, item := range document.Results.Items {
		content := ""
		if len(item.Alternatives) > 0 {
			content = strings.TrimSpace(item.Alternatives[0].Content)
		}
		if content == "" {
			continue
		}
		if strings.EqualFold(item.Type, "punctuation") {
			if len(segments) > 0 {
				segments[len(segments)-1].Text += content
			}
			continue
		}
		start, startOK := transcribeSeconds(item.StartTime)
		end, endOK := transcribeSeconds(item.EndTime)
		if !startOK || !endOK || start < 0 || end <= start ||
			math.IsNaN(start+end) || math.IsInf(start+end, 0) ||
			start > duration+0.25 || end > duration+1 {
			continue
		}
		end = min(end, duration)
		if end <= start {
			continue
		}
		segments = append(segments, transcript.SpeechSegment{
			Start: start, End: end,
			Speaker: transcribeSpeaker(document.Results.SpeakerLabels.Segments, start, end),
			Text:    content,
		})
	}
	return strings.Join(paragraphs, "\n\n"), segments, nil
}

func transcribeSeconds(raw json.RawMessage) (float64, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, false
	}
	var text string
	if json.Unmarshal(raw, &text) != nil {
		text = string(raw)
	}
	value, err := strconv.ParseFloat(text, 64)
	return value, err == nil && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func transcribeSpeaker(spans []struct {
	StartTime    json.RawMessage `json:"start_time"`
	EndTime      json.RawMessage `json:"end_time"`
	SpeakerLabel string          `json:"speaker_label"`
}, start, end float64) string {
	bestOverlap, speaker := 0.0, ""
	for _, span := range spans {
		spanStart, startOK := transcribeSeconds(span.StartTime)
		spanEnd, endOK := transcribeSeconds(span.EndTime)
		if !startOK || !endOK || spanEnd <= spanStart {
			continue
		}
		overlap := min(end, spanEnd) - max(start, spanStart)
		if overlap > bestOverlap {
			bestOverlap, speaker = overlap, strings.TrimSpace(span.SpeakerLabel)
		}
	}
	if bestOverlap <= 0 || len(speaker) > 64 || strings.ContainsAny(speaker, "\x00\r\n\t") {
		return ""
	}
	return speaker
}
