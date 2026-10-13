package transcript

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"ai-talker/internal/audio"
	"ai-talker/internal/config"
)

const maxSharedStreamCloudEngines = 8

type sharedStreamCloudEngine struct {
	ready  chan struct{}
	engine Transcriber
	err    error
}

type streamCloudKey struct {
	Profile       string `json:"profile"`
	Region        string `json:"region"`
	Model         string `json:"model"`
	Glossary      string `json:"glossary"`
	S3Bucket      string `json:"s3_bucket,omitempty"`
	Language      string `json:"language,omitempty"`
	SpeakerLabels bool   `json:"speaker_labels,omitempty"`
}

func sharedStreamKey(settings config.Settings) string {
	payload, _ := json.Marshal(streamCloudKey{
		Profile: settings.Profile, Region: settings.Region, Model: settings.ModelID(),
		Glossary: settings.Glossary, S3Bucket: settings.Cloud.S3Bucket,
		Language: settings.Cloud.TranscribeLanguage(), SpeakerLabels: settings.SpeakerLabels,
	})
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func (s *Service) cloudEngine(
	ctx context.Context,
	settings config.Settings,
	factory Factory,
	reuseForStream bool,
) (Transcriber, float64, error) {
	if !reuseForStream || settings.TranscriptionProvider() != config.ProviderBedrock {
		started := time.Now()
		engine, err := factory(ctx, settings)
		return engine, time.Since(started).Seconds(), err
	}

	key := sharedStreamKey(settings)
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return nil, 0, errors.New("o serviço de transcrição foi encerrado")
	}
	if entry := s.streamCloud[key]; entry != nil {
		ready := entry.ready
		s.mu.Unlock()
		select {
		case <-ready:
			if entry.err != nil {
				return nil, 0, entry.err
			}
			return retainedCloudEngine(entry.engine), 0, nil
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		}
	}
	if len(s.streamCloud) >= maxSharedStreamCloudEngines {
		s.mu.Unlock()
		started := time.Now()
		engine, err := factory(ctx, settings)
		return engine, time.Since(started).Seconds(), err
	}
	entry := &sharedStreamCloudEngine{ready: make(chan struct{})}
	s.streamCloud[key] = entry
	s.mu.Unlock()

	started := time.Now()
	engine, err := factory(ctx, settings)
	setupSeconds := time.Since(started).Seconds()
	if err == nil && engine == nil {
		err = errors.New("o transcritor não foi inicializado")
	}

	s.mu.Lock()
	if err != nil {
		delete(s.streamCloud, key)
		entry.err = err
		close(entry.ready)
		s.mu.Unlock()
		if engine != nil {
			if closeErr := engine.Close(); closeErr != nil {
				slog.Warn("falha ao fechar um transcritor Cloud não inicializado", "error", closeErr)
			}
		}
		return nil, setupSeconds, err
	}
	if s.closing {
		delete(s.streamCloud, key)
		entry.err = errors.New("o serviço de transcrição foi encerrado")
		close(entry.ready)
		s.mu.Unlock()
		if closeErr := engine.Close(); closeErr != nil {
			slog.Warn("falha ao fechar um transcritor Cloud após o encerramento", "error", closeErr)
		}
		return nil, setupSeconds, entry.err
	}
	entry.engine = engine
	close(entry.ready)
	s.mu.Unlock()
	return retainedCloudEngine(engine), setupSeconds, nil
}

func retainedCloudEngine(engine Transcriber) Transcriber {
	base := &retainedTranscriber{engine: engine}
	if streaming, ok := engine.(StreamingTranscriber); ok {
		return &retainedStreamingTranscriber{retainedTranscriber: base, streaming: streaming}
	}
	return base
}

type retainedTranscriber struct {
	engine Transcriber
}

func (t *retainedTranscriber) Transcribe(ctx context.Context, chunk audio.Chunk) (ChunkResult, error) {
	return t.engine.Transcribe(ctx, chunk)
}

func (*retainedTranscriber) Close() error { return nil }

type retainedStreamingTranscriber struct {
	*retainedTranscriber
	streaming StreamingTranscriber
}

func (t *retainedStreamingTranscriber) TranscribeStream(
	ctx context.Context,
	chunk audio.Chunk,
	onPartial func(string),
) (ChunkResult, error) {
	return t.streaming.TranscribeStream(ctx, chunk, onPartial)
}
