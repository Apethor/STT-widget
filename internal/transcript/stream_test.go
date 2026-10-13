package transcript

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ai-talker/internal/audio"
	"ai-talker/internal/config"
)

type streamingCloud struct {
	fakeCloud
	stream func(context.Context, audio.Chunk, func(string)) (ChunkResult, error)
}

func (c *streamingCloud) TranscribeStream(ctx context.Context, chunk audio.Chunk, onPartial func(string)) (ChunkResult, error) {
	return c.stream(ctx, chunk, onPartial)
}

func TestStreamPartIsVisibleBeforeCompletionButOnlyFinalTextReachesCache(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	factory := func(context.Context, config.Settings) (Transcriber, error) {
		return &streamingCloud{stream: func(ctx context.Context, _ audio.Chunk, onPartial func(string)) (ChunkResult, error) {
			onPartial("Uma resposta provisória")
			select {
			case <-release:
				return ChunkResult{Text: "Uma resposta concluída."}, nil
			case <-ctx.Done():
				return ChunkResult{}, ctx.Err()
			}
		}}, nil
	}
	service, source, directory := prepareService(t, speechParts(1), factory)
	partial := make(chan Partial, 1)
	finished := make(chan error, 1)
	go func() {
		_, err := service.Transcribe(t.Context(), source, config.Default(), Request{OnPartial: func(text Partial) { partial <- text }})
		finished <- err
	}()
	select {
	case text := <-partial:
		if text.Index != 0 || text.Text != "Uma resposta provisória" {
			t.Fatalf("partial = %+v", text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("partial transcription waited for completion")
	}
	select {
	case <-finished:
		t.Fatal("provisional text became a completed result")
	default:
	}
	partFiles, err := filepath.Glob(filepath.Join(directory, "jobs-go-v1", "*", "part-*.json"))
	if err != nil || len(partFiles) != 0 {
		t.Fatalf("provisional text was cached: %v / %v", partFiles, err)
	}
	unblock()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("final text did not finish")
	}
	service.options.CloudFactory = func(context.Context, config.Settings) (Transcriber, error) {
		return nil, errors.New("completed part must be reused")
	}
	result, err := service.Transcribe(t.Context(), source, config.Default(), Request{
		OnPartial: func(Partial) { t.Error("a completed cache must not emit provisional text") },
	})
	if err != nil || result.Text != "Uma resposta concluída." || result.ReusedParts != 1 {
		t.Fatalf("cache kept provisional instead of final text: %+v / %v", result, err)
	}
}

func TestStreamCancellationRejectsLatePartialsAndPreservesOnlyCompletedCache(t *testing.T) {
	t.Parallel()
	entered, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	var callbacks atomic.Int32
	factory := func(context.Context, config.Settings) (Transcriber, error) {
		return &streamingCloud{stream: func(_ context.Context, _ audio.Chunk, onPartial func(string)) (ChunkResult, error) {
			onPartial("Primeiro texto.")
			close(entered)
			<-release
			onPartial("Texto após o cancelamento.")
			return ChunkResult{Text: "Resposta concluída após o cancelamento."}, nil
		}}, nil
	}
	service, source, directory := prepareService(t, speechParts(1), factory)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := service.Transcribe(ctx, source, config.Default(), Request{
			OnPartial: func(Partial) { callbacks.Add(1) }, PersistLatest: true,
		})
		finished <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not start")
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("cancellation waited for a sent stream request")
	}
	unblock()
	service.jobs.Wait()
	if callbacks.Load() != 1 {
		t.Fatal("cancelled job delivered late provisional text")
	}
	if _, err := os.Stat(filepath.Join(directory, "last-transcript.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cancelled stream replaced the latest result")
	}
	service.options.CloudFactory = func(context.Context, config.Settings) (Transcriber, error) {
		return nil, errors.New("completed background part must be cached")
	}
	result, err := service.Transcribe(t.Context(), source, config.Default(), Request{
		OnPartial: func(Partial) { t.Error("a completed cache must not emit provisional text") },
	})
	if err != nil || result.Text != "Resposta concluída após o cancelamento." || result.ReusedParts != 1 {
		t.Fatalf("completed cache was lost: %+v / %v", result, err)
	}
}

func TestStreamImmediateRetryWaitsForPreviousFinalCache(t *testing.T) {
	t.Parallel()
	entered, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	var factories, callbacks, retryCallbacks atomic.Int32
	factory := func(context.Context, config.Settings) (Transcriber, error) {
		if factories.Add(1) != 1 {
			return nil, errors.New("the previous stream's final result must be reused")
		}
		return &streamingCloud{stream: func(ctx context.Context, _ audio.Chunk, onPartial func(string)) (ChunkResult, error) {
			onPartial("Primeiro texto provisório.")
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return ChunkResult{}, ctx.Err()
			}
			onPartial("Texto provisório após cancelar.")
			return ChunkResult{Text: "Resposta final concluída após o cancelamento."}, nil
		}}, nil
	}
	service, source, directory := prepareService(t, speechParts(1), factory)
	service.options.Audio = nativeTestPreparer{fakePreparer{chunks: speechParts(1)}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	first := startCacheTranscription(ctx, service, source, Request{
		NativePCM: true, PersistLatest: true, OnPartial: func(Partial) { callbacks.Add(1) },
	})
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the native stream did not publish its first partial")
	}
	cancel()
	if outcome := receiveCacheTranscription(t, first, 250*time.Millisecond); !errors.Is(outcome.err, context.Canceled) {
		t.Fatalf("stream cancellation waited for the sent call: %v", outcome.err)
	}
	waiting := make(chan Progress, 1)
	retry := startCacheTranscription(t.Context(), service, source, Request{
		NativePCM: true, OnProgress: cacheWaitReporter(waiting),
		OnPartial: func(Partial) { retryCallbacks.Add(1) },
	})
	requireCacheWait(t, waiting, retry)
	files, err := filepath.Glob(filepath.Join(directory, "jobs-go-v1", "*", "part-*.json"))
	if err != nil || len(files) != 0 {
		t.Fatalf("provisional stream text was cached before the call finished: %v / %v", files, err)
	}
	requireCacheLocked(t, cacheLockPath(t, directory))
	unblock()
	outcome := receiveCacheTranscription(t, retry, 2*time.Second)
	if outcome.err != nil || outcome.result.Text != "Resposta final concluída após o cancelamento." ||
		outcome.result.ReusedParts != 1 || factories.Load() != 1 || callbacks.Load() != 1 || retryCallbacks.Load() != 0 {
		t.Fatalf("retry resent native audio, reused provisional text or published late callbacks: %+v / %v", outcome.result, outcome.err)
	}
	if _, err := os.Stat(filepath.Join(directory, "last-transcript.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the cancelled stream published a complete snapshot: %v", err)
	}
	requireSyntheticSourcePreserved(t, source)
}

type nativeTestPreparer struct {
	fakePreparer
}

type nativeContextTestPreparer struct {
	nativeTestPreparer
	contextSamples uint64
}

func (p *nativeContextTestPreparer) PrepareLiveWithContext(ctx context.Context, source string, samples uint64) (audio.Chunk, error) {
	p.contextSamples = samples
	return p.PrepareLive(ctx, source)
}

func TestNativeContextIsForwardedAndChangesTheCacheIdentity(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	factory := func(context.Context, config.Settings) (Transcriber, error) {
		return &fakeCloud{transcribe: func(context.Context, audio.Chunk) (ChunkResult, error) {
			calls.Add(1)
			return ChunkResult{Text: "Fala reconhecida."}, nil
		}}, nil
	}
	service, source, _ := prepareService(t, speechParts(1), factory)
	preparer := &nativeContextTestPreparer{nativeTestPreparer: nativeTestPreparer{fakePreparer{chunks: speechParts(1)}}}
	service.options.Audio = preparer
	first, err := service.Transcribe(t.Context(), source, config.Default(), Request{NativePCM: true, NativeContextSamples: 16000})
	if err != nil || preparer.contextSamples != 16000 {
		t.Fatalf("native context was ignored: %+v / %v", first, err)
	}
	second, err := service.Transcribe(t.Context(), source, config.Default(), Request{NativePCM: true, NativeContextSamples: 32000})
	if err != nil || second.JobID == first.JobID || second.ReusedParts != 0 || calls.Load() != 2 || preparer.contextSamples != 32000 {
		t.Fatalf("different new-audio boundaries reused the same speech decision: %+v / %v", second, err)
	}
}

func TestLiveTextRemovesOnlyRecognitionAnnotationsAndTranslationWrappers(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		input, want string
		unclear     bool
	}{
		{"Minha fala [inaudível] continua.", "Minha fala continua.", true},
		{"[trecho impossível de transcrever]", "", true},
		{"[Unable to transcribe this segment]", "", true},
		{"English translation: this was translated.", "", true},
		{"Tradução: minha fala traduzida.", "", true},
		{"Uso [colchetes] e repito, repito.", "Uso [colchetes] e repito, repito.", false},
		{"Falei a palavra inaudível no exemplo.", "Falei a palavra inaudível no exemplo.", false},
		{"[Locutor 1] Texto reconhecido.", "[Locutor 1] Texto reconhecido.", false},
	} {
		clean, unclear := CleanLiveText(tc.input)
		if clean != tc.want || unclear != tc.unclear {
			t.Errorf("%q became %q / unclear=%v", tc.input, clean, unclear)
		}
	}
	if got := LivePartialText("Texto recebido [inaud"); got != "Texto recebido" {
		t.Fatalf("an unfinished control annotation flashed in the editor: %q", got)
	}
}

func (p nativeTestPreparer) PrepareLive(context.Context, string) (audio.Chunk, error) {
	chunk := p.chunks[0]
	chunk.Audio, chunk.Format = []byte("native WAV"), audio.FormatWAV
	return chunk, nil
}

func TestNativeStreamUsesPCMPreparationAndSeparatesItsCacheFromGeneralCodecs(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	factory := func(context.Context, config.Settings) (Transcriber, error) {
		return &fakeCloud{transcribe: func(_ context.Context, chunk audio.Chunk) (ChunkResult, error) {
			calls.Add(1)
			if chunk.Format == audio.FormatWAV {
				return ChunkResult{Text: "Fala no WAV."}, nil
			}
			return ChunkResult{Text: "Fala no MP3."}, nil
		}}, nil
	}
	service, source, _ := prepareService(t, speechParts(1), factory)
	service.options.Audio = nativeTestPreparer{fakePreparer{chunks: speechParts(1)}}
	first, err := service.Transcribe(t.Context(), source, config.Default(), Request{NativePCM: true})
	if err != nil || first.Text != "Fala no WAV." {
		t.Fatalf("native preparation was ignored: %+v / %v", first, err)
	}
	second, err := service.Transcribe(t.Context(), source, config.Default(), Request{NativePCM: true})
	if err != nil || second.JobID != first.JobID || second.ReusedParts != 1 || calls.Load() != 1 {
		t.Fatal("native stream could not reuse its completed cache")
	}
	third, err := service.Transcribe(t.Context(), source, config.Default(), Request{})
	if err != nil || third.JobID == first.JobID || third.ReusedParts != 0 ||
		third.Text != "Fala no MP3." || calls.Load() != 2 {
		t.Fatal("native preparation contaminated the general codec cache")
	}
}

func TestStreamRejectsGrossContinuationWithoutPublishingOrCachingIt(t *testing.T) {
	t.Parallel()
	text := strings.Repeat("Texto inventado. ", 60)
	factory := func(context.Context, config.Settings) (Transcriber, error) {
		return &streamingCloud{stream: func(_ context.Context, _ audio.Chunk, onPartial func(string)) (ChunkResult, error) {
			onPartial(text)
			return ChunkResult{Text: text}, nil
		}}, nil
	}
	chunks := speechParts(1)
	chunks[0].Duration = 2
	service, source, directory := prepareService(t, chunks, factory)
	result, err := service.Transcribe(t.Context(), source, config.Default(), Request{OnPartial: func(Partial) {
		t.Error("implausible partial reached the UI")
	}})
	if err == nil || result.Text != "" {
		t.Fatal("implausible live response became a result")
	}
	files, err := filepath.Glob(filepath.Join(directory, "jobs-go-v1", "*", "part-*.json"))
	if err != nil || len(files) != 0 {
		t.Fatal("implausible live response reached the part cache")
	}
}
