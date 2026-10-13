package transcript

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"time"

	"ai-talker/internal/audio"
	"ai-talker/internal/config"
	"ai-talker/internal/store"
)

const (
	maxRecoveryDepth = 3
	maxRecoveryCalls = 12
	minRecoverySplit = 8
)

type recoveryPreparer interface {
	Subdivide(context.Context, audio.Chunk) ([]audio.Chunk, error)
	RecoveryWAV(context.Context, audio.Chunk) (audio.Chunk, error)
}

type recoveryBudget struct{ remaining int }

func (b *recoveryBudget) take() bool {
	if b.remaining <= 0 {
		return false
	}
	b.remaining--
	return true
}

type subdivision struct {
	Version  int     `json:"version"`
	Start    float64 `json:"start"`
	Duration float64 `json:"duration"`
}

func (j *job) fallbackEnabled() bool {
	return j.settings.VoxtralRecovery() && !j.request.Streaming && !j.settings.Recovery.WhisperDisabled
}

func (j *job) recognizePart(chunk audio.Chunk) (ChunkResult, error) {
	if !j.settings.SupportsSubdivision() || j.request.Streaming {
		return j.transcribeWithRetry(chunk, j.ctx, nil)
	}
	ctx, cancel := context.WithTimeout(j.ctx, 15*time.Minute)
	defer cancel()
	budget := &recoveryBudget{remaining: maxRecoveryCalls}
	return j.recoverPart(ctx, chunk, fmt.Sprintf("%05d", chunk.Index), 0, budget)
}

func (j *job) recoveryPath(path string) string {
	return filepath.Join(j.directory, "recovery-"+path+".json")
}

func (j *job) recoverPart(ctx context.Context, chunk audio.Chunk, path string, depth int, budget *recoveryBudget) (ChunkResult, error) {
	if err := ctx.Err(); err != nil {
		return ChunkResult{}, err
	}
	if depth > 0 {
		var cached Part
		if err := store.ReadJSON(j.recoveryPath(path), &cached); err == nil && validCached(cached, chunk) {
			return ChunkResult{Text: cached.Text, Usage: cached.Usage, Engine: cached.Engine,
				Segments: cached.Segments, Children: cached.Children, Recovery: cached.Recovery,
				ElapsedSeconds: cached.ElapsedSeconds}, nil
		}
	}
	response := ChunkResult{Engine: j.settings.ModelID()}
	var cloudErr error
	var plan subdivision
	split := store.ReadJSON(j.recoveryPath(path)+".split", &plan) == nil &&
		plan.Version == 1 && math.Abs(plan.Start-chunk.Start) < 1.0/audio.SampleRate &&
		math.Abs(plan.Duration-chunk.Duration) < 1.0/audio.SampleRate
	if !split {
		response, cloudErr = j.transcribeWithRetry(chunk, ctx, budget)
		response.Engine = j.settings.ModelID()
		split = errors.Is(cloudErr, ErrOutputLimit)
	}
	if split && depth < maxRecoveryDepth && chunk.Duration > minRecoverySplit {
		if preparer, ok := j.service.options.Audio.(recoveryPreparer); ok && ctx.Err() == nil {
			children, err := preparer.Subdivide(ctx, chunk)
			if err == nil && validChildren(chunk, children) {
				if err := store.WriteJSON(j.recoveryPath(path)+".split", subdivision{Version: 1, Start: chunk.Start, Duration: chunk.Duration}); err != nil {
					return response, err
				}
				j.recoveryProgress(fmt.Sprintf("Voxtral · parte %d: recuperando com trechos menores; partes aceitas preservadas.", chunk.Index+1))
				var parts []Part
				var failures []error
				for index, child := range children {
					if err := ctx.Err(); err != nil {
						return combineRecovery(parts), err
					}
					childPath := fmt.Sprintf("%s-%d", path, index)
					childResponse := ChunkResult{Engine: j.settings.ModelID()}
					var childErr error
					if child.HasSpeech {
						childResponse, childErr = j.recoverPart(ctx, child, childPath, depth+1, budget)
					}
					part := makePart(child, childResponse)
					if childErr != nil {
						part.NeedsReview, part.Error, part.ReviewReason = true, childErr.Error(), childErr.Error()
						failures = append(failures, childErr)
					} else if err := store.WriteJSON(j.recoveryPath(childPath), part); err != nil {
						part.NeedsReview, part.Error, part.ReviewReason = true, err.Error(), err.Error()
						failures = append(failures, err)
					}
					parts = append(parts, part)
				}
				return combineRecovery(parts), errors.Join(failures...)
			}
			if err != nil {
				cloudErr = errors.Join(cloudErr, err)
			}
		}
	}
	if split && cloudErr == nil {
		cloudErr = fmt.Errorf("não foi possível reduzir mais esta parte: %w", ErrOutputLimit)
	}
	review := makePart(chunk, response)
	unrecognized := cloudErr == nil && chunk.HasSpeech &&
		(strings.TrimSpace(response.Text) == "" || onlyUnintelligibleMarkers(response.Text))
	if j.fallbackEnabled() && ctx.Err() == nil && (unrecognized || recoverableFailure(cloudErr)) {
		recovered, localErr := j.recoverWhisper(ctx, chunk)
		if localErr == nil {
			return recovered, nil
		}
		response.Recovery = recovered.Recovery
		if ctx.Err() != nil {
			return response, ctx.Err()
		}
		if cloudErr != nil {
			return response, errors.Join(cloudErr, fmt.Errorf("recuperação local: %w", localErr))
		}
		status := "Whisper indisponível"
		if recovered.Recovery != nil && recovered.Recovery.Status == "failed" {
			status = "Whisper tentou recuperar, mas falhou"
		}
		response.ReviewReason = strings.TrimSpace(review.ReviewReason + " " + status + ": " + localErr.Error())
		j.recoveryProgress(status + " · " + localErr.Error())
	}
	return response, cloudErr
}

func validChildren(parent audio.Chunk, children []audio.Chunk) bool {
	if len(children) != 2 {
		return false
	}
	end := parent.Start
	for index, child := range children {
		if child.Index != index || child.Duration <= 0 || child.Duration >= parent.Duration ||
			math.IsNaN(child.Start+child.Duration) || math.IsInf(child.Start+child.Duration, 0) ||
			child.Start < parent.Start || child.Start > end+1.0/audio.SampleRate {
			return false
		}
		end = max(end, child.Start+child.Duration)
	}
	return math.Abs(end-(parent.Start+parent.Duration)) <= 1.0/audio.SampleRate
}

func recoverableFailure(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, ErrOutputLimit) {
		return true
	}
	var temporary retryableError
	return errors.As(err, &temporary) && temporary.Retryable()
}

func combineRecovery(parts []Part) ChunkResult {
	engines := make(map[string]bool)
	var review bool
	for _, part := range parts {
		if part.Text != "" && part.Engine != "" {
			engines[part.Engine] = true
		}
		review = review || part.NeedsReview || part.Error != ""
	}
	var names []string
	for name := range engines {
		names = append(names, name)
	}
	// Stable metadata is independent of completion order.
	if len(names) > 1 && names[0] > names[1] {
		names[0], names[1] = names[1], names[0]
	}
	result := ChunkResult{Text: Merge(parts, true), Children: parts, Engine: strings.Join(names, " + "),
		Usage: map[string]any{"subdivided": true, "parts": len(parts)}}
	if review {
		result.ReviewReason = "A recuperação desta parte ainda tem trechos pendentes."
	}
	return result
}

func (j *job) recoveryProgress(message string) {
	select {
	case j.retryUpdates <- message:
	case <-j.ctx.Done():
	}
}

func (j *job) recoverWhisper(ctx context.Context, chunk audio.Chunk) (response ChunkResult, err error) {
	attempt := &RecoveryAttempt{Engine: "whisper.cpp/" + j.settings.Whisper.Model, Status: "unavailable"}
	response.Recovery = attempt
	defer func() {
		response.Recovery = attempt
		if err != nil {
			attempt.Error = err.Error()
		}
	}()
	if j.service.options.LocalFactory == nil {
		return response, errors.New("configure o motor e um modelo Whisper em Configurações → Whisper")
	}
	preparer, ok := j.service.options.Audio.(recoveryPreparer)
	if !ok {
		return response, errors.New("o preparador não oferece recuperação em WAV")
	}
	wav, err := preparer.RecoveryWAV(ctx, chunk)
	if err != nil {
		return response, err
	}
	j.localMu.Lock()
	if j.local == nil {
		settings := j.settings
		settings.Provider = config.ProviderWhisper
		j.local, err = j.service.options.LocalFactory(ctx, settings)
		if j.local == nil && err == nil {
			err = errors.New("o motor local não foi inicializado")
		}
	}
	local := j.local
	j.localMu.Unlock()
	if err != nil {
		return response, err
	}
	if err := j.service.localSlot.acquire(ctx); err != nil {
		return response, err
	}
	defer j.service.localSlot.release()
	j.recoveryProgress(fmt.Sprintf("Recuperando parte %d pelo Whisper %s configurado…", chunk.Index+1, j.settings.Whisper.Model))
	localCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	attempt.Status = "failed"
	response, err = local.Transcribe(localCtx, wav)
	if err != nil {
		return response, err
	}
	if review := makePart(chunk, response); review.NeedsReview {
		return response, errors.New("o Whisper também não reconheceu esta parte com segurança")
	}
	response.Engine = "whisper.cpp/" + j.settings.Whisper.Model
	if response.Usage == nil {
		response.Usage = make(map[string]any)
	}
	response.Usage["fallback_from"] = j.settings.ModelID()
	attempt.Status = "recognized"
	return response, nil
}
