package transcript

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ai-talker/internal/audio"
	"ai-talker/internal/config"
	"ai-talker/internal/platform"
	"ai-talker/internal/store"
)

type fakePreparer struct {
	chunks []audio.Chunk
}

func (a fakePreparer) Duration(context.Context, string) (float64, error) {
	var duration float64
	for _, part := range a.chunks {
		duration = max(duration, part.Start+part.Duration)
	}
	return duration, nil
}

func (a fakePreparer) Split(ctx context.Context, _ string, _ audio.ChunkOptions, yield func(audio.Chunk) error) error {
	for _, part := range a.chunks {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := yield(part); err != nil {
			return err
		}
	}
	return nil
}

type fakeCloud struct {
	transcribe func(context.Context, audio.Chunk) (ChunkResult, error)
	close      func() error
}

func (c *fakeCloud) Transcribe(ctx context.Context, chunk audio.Chunk) (ChunkResult, error) {
	return c.transcribe(ctx, chunk)
}

func (c *fakeCloud) Close() error {
	if c.close != nil {
		return c.close()
	}
	return nil
}

func prepareService(t *testing.T, chunks []audio.Chunk, factory Factory) (*Service, string, string) {
	t.Helper()
	directory := t.TempDir()
	source := filepath.Join(directory, "audio.wav")
	if err := os.WriteFile(source, []byte("synthetic input identity"), 0o600); err != nil {
		t.Fatal(err)
	}
	service, err := New(t.Context(), Options{
		DataDir: directory, Audio: fakePreparer{chunks: chunks}, CloudFactory: factory,
		RequestTimeout: 3 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Error(err)
		}
	})
	return service, source, directory
}

func speechParts(count int) []audio.Chunk {
	chunks := make([]audio.Chunk, count)
	for index := range chunks {
		chunks[index] = audio.Chunk{
			Index: index, Start: float64(index * 10), Duration: 10,
			HasSpeech: true, SpeechSeconds: 7, Audio: []byte("mp3"),
		}
	}
	return chunks
}

func TestSilenceReplacesOldSnapshotWithoutAWSOrExports(t *testing.T) {
	var calls atomic.Int32
	factory := func(context.Context, config.Settings) (Transcriber, error) {
		calls.Add(1)
		return nil, errors.New("silence must not reach AWS")
	}
	service, source, directory := prepareService(t, []audio.Chunk{{Index: 0, Duration: 3}}, factory)
	latest := filepath.Join(directory, "last-transcript.json")
	if err := store.WriteJSON(latest, Result{Text: "Resultado antigo."}); err != nil {
		t.Fatal(err)
	}
	result, err := service.Transcribe(t.Context(), source, config.Default(), Request{PersistLatest: true})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 || result.Text != "" || result.Exports != nil || result.DurationSeconds != 3 {
		t.Fatalf("silence produced stale text, AWS or export: %+v calls=%d", result, calls.Load())
	}
	var saved Result
	if err := store.ReadJSON(latest, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Text != "" || saved.Source != source {
		t.Fatalf("silence restored an old snapshot: %+v", saved)
	}
}

func TestFailureResumesOnlyCompletedPartsAndPreservesExports(t *testing.T) {
	var mu sync.Mutex
	calls := make(map[int]int)
	fail := true
	factory := func(context.Context, config.Settings) (Transcriber, error) {
		return &fakeCloud{transcribe: func(_ context.Context, chunk audio.Chunk) (ChunkResult, error) {
			mu.Lock()
			defer mu.Unlock()
			calls[chunk.Index]++
			if chunk.Index == 1 && fail {
				return ChunkResult{}, errors.New("simulated AWS failure")
			}
			return ChunkResult{Text: fmt.Sprintf("Parte %d completa.", chunk.Index+1)}, nil
		}}, nil
	}
	service, source, directory := prepareService(t, speechParts(3), factory)
	first, err := service.Transcribe(t.Context(), source, config.Default(), Request{Workers: 1})
	var failure *Error
	if !errors.As(err, &failure) || failure.PartialText != "Parte 1 completa." {
		t.Fatalf("failure did not identify contiguous partial text: result=%+v err=%v", first, err)
	}
	if _, err := os.Stat(filepath.Join(directory, "last-transcript.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failure persisted itself as complete: %v", err)
	}
	mu.Lock()
	fail = false
	mu.Unlock()
	result, err := service.Transcribe(t.Context(), source, config.Default(), Request{Workers: 1, PersistLatest: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.ReusedParts != 1 || len(result.Parts) != 3 || result.DurationSeconds != 30 {
		t.Fatalf("resume lost parts or reused the failed part: %+v", result)
	}
	mu.Lock()
	if calls[0] != 1 || calls[1] != 2 || calls[2] != 1 {
		t.Fatalf("completed parts were resent: %v", calls)
	}
	mu.Unlock()
	markdown, err := os.ReadFile(result.Exports["md"])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(markdown), "Parte 3 completa.") {
		t.Fatalf("Markdown lost the final part: %s", markdown)
	}
}

func TestEmptyResponseWithDetectedSpeechIsRetriedAndMarkedForReview(t *testing.T) {
	var calls atomic.Int32
	factory := func(context.Context, config.Settings) (Transcriber, error) {
		return &fakeCloud{transcribe: func(context.Context, audio.Chunk) (ChunkResult, error) {
			if calls.Add(1) == 1 {
				return ChunkResult{}, nil
			}
			return ChunkResult{Text: "Texto recuperado."}, nil
		}}, nil
	}
	service, source, _ := prepareService(t, speechParts(1), factory)
	first, err := service.Transcribe(t.Context(), source, config.Default(), Request{})
	if err != nil {
		t.Fatal(err)
	}
	if first.Text != "" || !first.NeedsReview || first.Exports != nil {
		t.Fatalf("speech without recognized text was hidden: %+v", first)
	}
	second, err := service.Transcribe(t.Context(), source, config.Default(), Request{})
	if err != nil {
		t.Fatal(err)
	}
	if second.Text != "Texto recuperado." || second.NeedsReview || calls.Load() != 2 || second.ReusedParts != 0 {
		t.Fatalf("empty model response was wrongly reused: %+v calls=%d", second, calls.Load())
	}
}

func TestRecoveredTextKeepsReviewWarning(t *testing.T) {
	factory := func(context.Context, config.Settings) (Transcriber, error) {
		return &fakeCloud{transcribe: func(context.Context, audio.Chunk) (ChunkResult, error) {
			return ChunkResult{
				Text: "Oi.", ReviewReason: "O modelo indicou ausência de fala na primeira tentativa.",
			}, nil
		}}, nil
	}
	service, source, _ := prepareService(t, speechParts(1), factory)
	result, err := service.Transcribe(t.Context(), source, config.Default(), Request{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "Oi." || !result.NeedsReview || len(result.Parts) != 1 ||
		!result.Parts[0].NeedsReview || result.Parts[0].ReviewReason == "" {
		t.Fatalf("recovered text lost its review warning: %+v", result)
	}
}

func TestRequestTimeoutPreservesCompletedPartialText(t *testing.T) {
	factory := func(context.Context, config.Settings) (Transcriber, error) {
		return &fakeCloud{transcribe: func(_ context.Context, chunk audio.Chunk) (ChunkResult, error) {
			if chunk.Index == 1 {
				return ChunkResult{}, fmt.Errorf("AWS request timed out: %w", context.DeadlineExceeded)
			}
			return ChunkResult{Text: "Primeira parte preservada."}, nil
		}}, nil
	}
	service, source, directory := prepareService(t, speechParts(2), factory)
	_, err := service.Transcribe(t.Context(), source, config.Default(), Request{Workers: 1, PersistLatest: true})
	var failure *Error
	if !errors.As(err, &failure) || !errors.Is(err, context.DeadlineExceeded) ||
		failure.PartialText != "Primeira parte preservada." {
		t.Fatalf("a network timeout lost the completed text: %v", err)
	}
	partial, err := os.ReadFile(filepath.Join(directory, "jobs-go-v1", failure.JobID, "partial.txt"))
	if err != nil || strings.TrimSpace(string(partial)) != failure.PartialText {
		t.Fatalf("the partial text was not recoverable on disk: %s %v", partial, err)
	}
	if _, err := os.Stat(filepath.Join(directory, "last-transcript.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("timed out audio persisted a complete snapshot: %v", err)
	}
}

func TestImplausibleResponseIsMarkedAndRetriedWithoutReusingIt(t *testing.T) {
	var calls atomic.Int32
	factory := func(context.Context, config.Settings) (Transcriber, error) {
		return &fakeCloud{transcribe: func(context.Context, audio.Chunk) (ChunkResult, error) {
			if calls.Add(1) == 1 {
				return ChunkResult{Text: strings.Repeat("conteúdo acrescentado ", 60)}, nil
			}
			return ChunkResult{Text: "Hello. This is a short recording."}, nil
		}}, nil
	}
	chunks := []audio.Chunk{{Index: 0, Duration: 3.5, HasSpeech: true, SpeechSeconds: 1.7, Audio: []byte("mp3")}}
	service, source, _ := prepareService(t, chunks, factory)
	first, err := service.Transcribe(t.Context(), source, config.Default(), Request{})
	if err != nil {
		t.Fatal(err)
	}
	if !first.NeedsReview || !first.Parts[0].NeedsReview || first.Parts[0].ReviewReason == "" {
		t.Fatal("a grossly oversized model continuation was presented as complete")
	}
	markdown, err := os.ReadFile(first.Exports["md"])
	if err != nil || !strings.Contains(string(markdown), "Transcrição para conferência") {
		t.Fatalf("the export omitted the review warning: %s %v", markdown, err)
	}
	second, err := service.Transcribe(t.Context(), source, config.Default(), Request{})
	if err != nil {
		t.Fatal(err)
	}
	if second.NeedsReview || second.ReusedParts != 0 || calls.Load() != 2 ||
		second.Text != "Hello. This is a short recording." {
		t.Fatalf("the reviewed response was reused or a normal short response was rejected: %+v", second)
	}
}

func TestCancellationReturnsBeforeSentCallAndKeepsItsCache(t *testing.T) {
	entered, release, closed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var released sync.Once
	unblock := func() { released.Do(func() { close(release) }) }
	defer unblock()
	factory := func(context.Context, config.Settings) (Transcriber, error) {
		return &fakeCloud{
			transcribe: func(context.Context, audio.Chunk) (ChunkResult, error) {
				close(entered)
				<-release
				return ChunkResult{Text: "Fala concluída depois do cancelamento."}, nil
			},
			close: func() error { close(closed); return nil },
		}, nil
	}
	service, source, directory := prepareService(t, speechParts(1), factory)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	returned := make(chan error, 1)
	go func() {
		_, err := service.Transcribe(ctx, source, config.Default(), Request{PersistLatest: true})
		returned <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("simulated request did not start")
	}
	cancel()
	select {
	case err := <-returned:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation = %v", err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("foreground cancellation waited for the sent AWS call")
	}
	if _, err := os.Stat(filepath.Join(directory, "last-transcript.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled job persisted latest result: %v", err)
	}
	lockFiles, err := filepath.Glob(filepath.Join(directory, "jobs-go-v1", "*", ".lock"))
	if err != nil || len(lockFiles) != 1 {
		t.Fatalf("job cache lock missing: %v %v", lockFiles, err)
	}
	if lock, err := platform.TryLock(lockFiles[0]); !errors.Is(err, platform.ErrLocked) {
		if lock != nil {
			lock.Close()
		}
		t.Fatalf("job lock released before the in-flight cache write: %v", err)
	}
	unblock()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight client did not finish")
	}
	service.jobs.Wait()
	service.options.CloudFactory = func(context.Context, config.Settings) (Transcriber, error) {
		return nil, errors.New("completed cached part must not reach AWS again")
	}
	result, err := service.Transcribe(t.Context(), source, config.Default(), Request{})
	if err != nil {
		t.Fatal(err)
	}
	if result.ReusedParts != 1 || !strings.Contains(result.Text, "cancelamento") {
		t.Fatalf("completed in-flight part was not cached: %+v", result)
	}
}

func TestOneFailureReturnsWhileAnotherCallStillWaits(t *testing.T) {
	secondStarted, release, closed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var released sync.Once
	defer released.Do(func() { close(release) })
	factory := func(context.Context, config.Settings) (Transcriber, error) {
		return &fakeCloud{
			transcribe: func(_ context.Context, chunk audio.Chunk) (ChunkResult, error) {
				if chunk.Index == 0 {
					<-secondStarted
					return ChunkResult{}, errors.New("first part failed")
				}
				close(secondStarted)
				<-release
				return ChunkResult{Text: "Segunda parte concluída."}, nil
			},
			close: func() error { close(closed); return nil },
		}, nil
	}
	service, source, _ := prepareService(t, speechParts(2), factory)
	returned := make(chan error, 1)
	go func() {
		_, err := service.Transcribe(t.Context(), source, config.Default(), Request{})
		returned <- err
	}()
	select {
	case <-secondStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("second simulated request did not start")
	}
	select {
	case err := <-returned:
		if !strings.Contains(err.Error(), "first part failed") {
			t.Fatalf("wrong failure: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("failure waited for the other AWS call")
	}
	released.Do(func() { close(release) })
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("remaining request or client leaked")
	}
}

func TestCacheIdentityUsesFileContentAndTranscriptionSettings(t *testing.T) {
	base, err := fingerprint("first-content", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	changedSettings := []struct {
		name   string
		change func(*config.Settings)
	}{
		{name: "model", change: func(s *config.Settings) { s.Quality = "fast" }},
		{name: "region", change: func(s *config.Settings) { s.Region = "us-west-2" }},
		{name: "chunk duration", change: func(s *config.Settings) { s.ChunkSeconds = 60 }},
		{name: "overlap", change: func(s *config.Settings) { s.OverlapSeconds = 0 }},
		{name: "glossary", change: func(s *config.Settings) { s.Glossary += ", new term" }},
		{name: "speaker labels", change: func(s *config.Settings) { s.SpeakerLabels = true }},
	}
	for _, test := range changedSettings {
		t.Run(test.name, func(t *testing.T) {
			settings := config.Default()
			test.change(&settings)
			got, err := fingerprint("first-content", settings)
			if err != nil {
				t.Fatal(err)
			}
			if got == base {
				t.Fatal("cache ignored a transcription setting")
			}
		})
	}
	changed, err := fingerprint("second-content", config.Default())
	if err != nil || changed == base {
		t.Fatalf("cache ignored source content: %s %v", changed, err)
	}
	settings := config.Default()
	settings.Profile = "another-login"
	same, err := fingerprint("first-content", settings)
	if err != nil || same != base {
		t.Fatal("credential selection should not invalidate an identical transcription")
	}
}

func TestGapInDecodedAudioCannotBeExportedAsComplete(t *testing.T) {
	chunks := []audio.Chunk{
		{Index: 0, Start: 0, Duration: 10},
		{Index: 1, Start: 11, Duration: 10},
	}
	factory := func(context.Context, config.Settings) (Transcriber, error) {
		return nil, errors.New("silent test must not reach AWS")
	}
	service, source, directory := prepareService(t, chunks, factory)
	_, err := service.Transcribe(t.Context(), source, config.Default(), Request{PersistLatest: true})
	if err == nil || !strings.Contains(err.Error(), "sem cobertura") {
		t.Fatalf("audio gap was accepted as complete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, "last-transcript.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("audio with a gap persisted a complete snapshot: %v", err)
	}
}
