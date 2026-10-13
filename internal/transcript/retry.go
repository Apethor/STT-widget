package transcript

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"time"

	"ai-talker/internal/audio"
	"ai-talker/internal/config"
)

const maxCloudAttempts = 3

// BedrockPartTimeout bounds one attempt, allowing more time for longer audio.
// Callers with an earlier deadline still keep that deadline.
func BedrockPartTimeout(seconds float64) time.Duration {
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return 120 * time.Second
	}
	return time.Duration(max(120, min(360, seconds*2+30)) * float64(time.Second))
}

type retryableError interface {
	Retryable() bool
}

func (j *job) requestContext(chunk audio.Chunk) (context.Context, context.CancelFunc) {
	return j.callContext(chunk, j.ctx)
}

func (j *job) callContext(chunk audio.Chunk, foreground context.Context) (context.Context, context.CancelFunc) {
	lifetime, timeout := j.service.ctx, j.service.options.RequestTimeout
	if j.settings.TranscriptionProvider() == config.ProviderWhisper {
		lifetime, timeout = foreground, 30*time.Minute
	} else if j.settings.TranscriptionProvider() == config.ProviderAPI {
		lifetime = foreground
	} else if model, _ := config.CloudModelByID(j.settings.CloudModelID()); model.Backend == config.CloudTranscribe {
		lifetime, timeout = foreground, 10*time.Minute
	} else if j.settings.DirectCloudAudio() {
		lifetime = foreground
		timeout = max(timeout, time.Duration((chunk.Duration+45)*float64(time.Second)))
	} else if j.service.automaticTimeout {
		timeout = max(timeout, BedrockPartTimeout(chunk.Duration))
	}
	if deadline, ok := foreground.Deadline(); ok {
		timeout = min(timeout, time.Until(deadline))
	}
	return context.WithTimeout(lifetime, timeout)
}

func (j *job) transcribeWithRetry(chunk audio.Chunk, foreground context.Context, budget *recoveryBudget) (ChunkResult, error) {
	started := time.Now()
	for attempt := 1; ; attempt++ {
		// A sent Converse call may finish and reach the cache after Cancel.
		// Starting another attempt always requires the foreground job.
		if err := foreground.Err(); err != nil {
			return ChunkResult{}, err
		}
		if budget != nil && !budget.take() {
			return ChunkResult{}, fmt.Errorf("a recuperação atingiu seu limite de chamadas: %w", ErrOutputLimit)
		}
		slots := j.requestSlots()
		if err := slots.acquire(foreground); err != nil {
			return ChunkResult{}, err
		}
		if err := foreground.Err(); err != nil {
			slots.release()
			return ChunkResult{}, err
		}
		ctx, cancel := j.callContext(chunk, foreground)
		ctx = withAdmission(ctx, foreground, budget)
		var response ChunkResult
		var err error
		if streaming, ok := j.cloud.(StreamingTranscriber); ok && j.request.OnPartial != nil {
			response, err = streaming.TranscribeStream(ctx, chunk, func(text string) {
				if j.ctx.Err() == nil && !ImplausibleLiveText(text, chunk.Duration) {
					j.request.OnPartial(Partial{Index: chunk.Index, Text: text})
				}
			})
		} else {
			response, err = j.cloud.Transcribe(ctx, chunk)
		}
		cancel()
		slots.release()
		if err == nil {
			if attempt > 1 {
				if response.Usage == nil {
					response.Usage = make(map[string]any)
				}
				response.Usage["attempts"] = attempt
				response.ElapsedSeconds = time.Since(started).Seconds()
			}
			return response, nil
		}
		if !j.shouldRetry(err) || foreground.Err() != nil || attempt >= maxCloudAttempts {
			if attempt > 1 {
				err = fmt.Errorf("parte %d, após %d tentativas: %w", chunk.Index+1, attempt, err)
			}
			return ChunkResult{}, err
		}
		if j.request.OnPartial != nil && j.ctx.Err() == nil {
			j.request.OnPartial(Partial{Index: chunk.Index, Text: ""})
		}
		message := fmt.Sprintf("AWS · parte %d · nova tentativa %d de %d; progresso salvo.",
			chunk.Index+1, attempt+1, maxCloudAttempts)
		select {
		case j.retryUpdates <- message:
		case <-foreground.Done():
			return ChunkResult{}, foreground.Err()
		}
		slog.Info("retentativa de transcrição AWS", "part", chunk.Index+1,
			"attempt", attempt+1, "max_attempts", maxCloudAttempts, "audio_seconds", chunk.Duration)
		// Equal jitter avoids simultaneous retries from the two Team tracks.
		delay := time.Second << (attempt - 1)
		delay = delay/2 + time.Duration(rand.Int64N(int64(delay/2)))
		timer := time.NewTimer(delay)
		select {
		case <-foreground.Done():
			timer.Stop()
			return ChunkResult{}, foreground.Err()
		case <-timer.C:
		}
	}
}

func (j *job) shouldRetry(err error) bool {
	if j.ctx.Err() != nil || j.service.ctx.Err() != nil || errors.Is(err, context.Canceled) {
		return false
	}
	model, _ := config.CloudModelByID(j.settings.CloudModelID())
	if j.settings.TranscriptionProvider() != config.ProviderBedrock || model.Backend != config.CloudConverse {
		return false
	}
	var temporary retryableError
	return errors.As(err, &temporary) && temporary.Retryable()
}
