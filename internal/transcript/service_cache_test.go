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
	"testing/synctest"
	"time"

	"ai-talker/internal/audio"
	"ai-talker/internal/config"
	"ai-talker/internal/platform"
	"ai-talker/internal/store"
)

func TestImmediateRetryAfterCancelWaitsForPreviousCache(t *testing.T) {
	t.Parallel()
	entered, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	var factories, calls atomic.Int32
	factory := func(context.Context, config.Settings) (Transcriber, error) {
		if factories.Add(1) != 1 {
			return nil, errors.New("the previous call's completed part must be reused")
		}
		return &fakeCloud{transcribe: func(ctx context.Context, _ audio.Chunk) (ChunkResult, error) {
			calls.Add(1)
			close(entered)
			select {
			case <-release:
				return ChunkResult{Text: "Fala concluída depois do cancelamento."}, nil
			case <-ctx.Done():
				return ChunkResult{}, ctx.Err()
			}
		}}, nil
	}
	service, source, directory := prepareService(t, speechParts(1), factory)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	first := startCacheTranscription(ctx, service, source, Request{PersistLatest: true})
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the first simulated call did not start")
	}
	cancel()
	if outcome := receiveCacheTranscription(t, first, 250*time.Millisecond); !errors.Is(outcome.err, context.Canceled) {
		t.Fatalf("foreground cancellation = %v", outcome.err)
	}
	lockPath := cacheLockPath(t, directory)
	waiting := make(chan Progress, 1)
	retry := startCacheTranscription(t.Context(), service, source, Request{OnProgress: cacheWaitReporter(waiting)})
	requireCacheWait(t, waiting, retry)
	requireCacheLocked(t, lockPath)
	if factories.Load() != 1 || calls.Load() != 1 {
		t.Fatal("retry sent another request while the previous cache was locked")
	}
	if _, err := os.Stat(filepath.Join(directory, "last-transcript.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the cancelled attempt published a complete result: %v", err)
	}

	unblock()
	outcome := receiveCacheTranscription(t, retry, 2*time.Second)
	if outcome.err != nil || outcome.result.ReusedParts != 1 || outcome.result.DurationSeconds != 10 ||
		outcome.result.Text != "Fala concluída depois do cancelamento." || factories.Load() != 1 || calls.Load() != 1 {
		t.Fatalf("immediate retry lost or resent the previous completed part: %+v / %v", outcome.result, outcome.err)
	}
	requireSyntheticSourcePreserved(t, source)
}

func TestImmediateRetryAfterPartFailureWaitsAndReusesRemainingCalls(t *testing.T) {
	t.Parallel()
	secondStarted, thirdStarted := make(chan struct{}), make(chan struct{})
	releaseCalls, cacheSaved, releaseClose := make(chan struct{}), make(chan struct{}), make(chan struct{})
	unblockCalls := sync.OnceFunc(func() { close(releaseCalls) })
	unblockClose := sync.OnceFunc(func() { close(releaseClose) })
	defer unblockCalls()
	defer unblockClose()
	firstFailure := errors.New("first part failed")
	var factories atomic.Int32
	var calls [4]atomic.Int32
	factory := func(context.Context, config.Settings) (Transcriber, error) {
		first := factories.Add(1) == 1
		client := &fakeCloud{transcribe: func(ctx context.Context, chunk audio.Chunk) (ChunkResult, error) {
			calls[chunk.Index].Add(1)
			if first {
				switch chunk.Index {
				case 0:
					for _, started := range []<-chan struct{}{secondStarted, thirdStarted} {
						select {
						case <-started:
						case <-ctx.Done():
							return ChunkResult{}, ctx.Err()
						}
					}
					return ChunkResult{}, firstFailure
				case 1:
					close(secondStarted)
				case 2:
					close(thirdStarted)
				default:
					return ChunkResult{}, errors.New("the failed attempt must leave later audio pending")
				}
				select {
				case <-releaseCalls:
				case <-ctx.Done():
					return ChunkResult{}, ctx.Err()
				}
			}
			return ChunkResult{Text: fmt.Sprintf("Parte %d completa.", chunk.Index+1)}, nil
		}}
		if first {
			client.close = func() error {
				close(cacheSaved)
				<-releaseClose
				return nil
			}
		}
		return client, nil
	}
	service, source, directory := prepareService(t, speechParts(4), factory)
	first := startCacheTranscription(t.Context(), service, source, Request{Workers: 3, PersistLatest: true})
	failed := receiveCacheTranscription(t, first, 2*time.Second)
	var failure *Error
	if !errors.As(failed.err, &failure) || !errors.Is(failed.err, firstFailure) || failure.PartialText != "" {
		t.Fatalf("the first failed part lost its cause or became completed text: %v", failed.err)
	}
	waiting := make(chan Progress, 1)
	retry := startCacheTranscription(t.Context(), service, source, Request{Workers: 3, OnProgress: cacheWaitReporter(waiting)})
	requireCacheWait(t, waiting, retry)
	lockPath := filepath.Join(directory, "jobs-go-v1", failure.JobID, ".lock")
	requireCacheLocked(t, lockPath)
	if factories.Load() != 1 {
		t.Fatal("retry created a competing transcriber while other parts were in flight")
	}

	unblockCalls()
	select {
	case <-cacheSaved:
	case <-time.After(2 * time.Second):
		t.Fatal("the remaining calls did not save their completed parts")
	}
	for _, index := range []int{1, 2} {
		var part Part
		path := filepath.Join(filepath.Dir(lockPath), fmt.Sprintf("part-%05d.json", index))
		if err := store.ReadJSON(path, &part); err != nil || part.Text != fmt.Sprintf("Parte %d completa.", index+1) {
			t.Fatalf("background completion was not saved before unlocking: %+v / %v", part, err)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(lockPath), "part-00000.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the failed part was cached as complete: %v", err)
	}
	requireCacheLocked(t, lockPath)
	select {
	case outcome := <-retry:
		t.Fatalf("retry finished before the previous owner released its cache: %+v / %v", outcome.result, outcome.err)
	default:
	}
	unblockClose()
	outcome := receiveCacheTranscription(t, retry, 2*time.Second)
	if outcome.err != nil || outcome.result.ReusedParts != 2 || len(outcome.result.Parts) != 4 ||
		outcome.result.DurationSeconds != 40 || !strings.HasSuffix(outcome.result.Text, "Parte 4 completa.") {
		t.Fatalf("retry lost successful in-flight parts or pending audio: %+v / %v", outcome.result, outcome.err)
	}
	for index, want := range []int32{2, 1, 1, 1} {
		if got := calls[index].Load(); got != want {
			t.Errorf("part %d calls = %d, want %d", index+1, got, want)
		}
	}
	requireSyntheticSourcePreserved(t, source)
}

func TestCacheWaiterStopsOnCancelAndServiceClose(t *testing.T) {
	for _, action := range []string{"cancel waiter", "close service"} {
		t.Run(action, func(t *testing.T) {
			t.Parallel()
			entered, stopped := make(chan struct{}), make(chan struct{})
			var factories, calls, closed atomic.Int32
			factory := func(context.Context, config.Settings) (Transcriber, error) {
				if factories.Add(1) != 1 {
					return nil, errors.New("a cache waiter must not create another transcriber")
				}
				return &fakeCloud{
					transcribe: func(ctx context.Context, _ audio.Chunk) (ChunkResult, error) {
						calls.Add(1)
						close(entered)
						<-ctx.Done()
						close(stopped)
						return ChunkResult{}, ctx.Err()
					},
					close: func() error { closed.Add(1); return nil },
				}, nil
			}
			service, source, directory := prepareService(t, speechParts(1), factory)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			first := startCacheTranscription(ctx, service, source, Request{})
			select {
			case <-entered:
			case <-time.After(2 * time.Second):
				t.Fatal("the first simulated call did not start")
			}
			cancel()
			if outcome := receiveCacheTranscription(t, first, 250*time.Millisecond); !errors.Is(outcome.err, context.Canceled) {
				t.Fatalf("foreground cancellation = %v", outcome.err)
			}
			waitCtx, cancelWait := context.WithCancel(t.Context())
			defer cancelWait()
			waiting := make(chan Progress, 1)
			waiter := startCacheTranscription(waitCtx, service, source, Request{OnProgress: cacheWaitReporter(waiting)})
			requireCacheWait(t, waiting, waiter)
			lockPath := cacheLockPath(t, directory)
			if action == "cancel waiter" {
				cancelWait()
				if outcome := receiveCacheTranscription(t, waiter, 250*time.Millisecond); !errors.Is(outcome.err, context.Canceled) {
					t.Fatalf("waiter cancellation = %v", outcome.err)
				}
				requireCacheLocked(t, lockPath)
				select {
				case <-stopped:
					t.Fatal("cancelling the waiter stopped the previous owner's call")
				default:
				}
			}
			serviceClosed := make(chan error, 1)
			go func() { serviceClosed <- service.Close() }()
			if action == "close service" {
				if outcome := receiveCacheTranscription(t, waiter, 250*time.Millisecond); !errors.Is(outcome.err, context.Canceled) {
					t.Fatalf("closing the service did not cancel its cache waiter: %v", outcome.err)
				}
			}
			select {
			case err := <-serviceClosed:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(250 * time.Millisecond):
				t.Fatal("closing the service leaked a call or cache waiter")
			}
			if factories.Load() != 1 || calls.Load() != 1 || closed.Load() != 1 {
				t.Fatalf("waiting created duplicate calls or leaked the transcriber: %d / %d / %d",
					factories.Load(), calls.Load(), closed.Load())
			}
			lock, err := platform.TryLock(lockPath)
			if err != nil {
				t.Fatalf("closing the service kept the previous cache locked: %v", err)
			}
			if err := lock.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := service.Transcribe(t.Context(), source, config.Default(), Request{}); err == nil ||
				!strings.Contains(err.Error(), "encerrado") {
				t.Fatalf("a closed service accepted another waiter: %v", err)
			}
			requireSyntheticSourcePreserved(t, source)
		})
	}
}

func TestCacheWaitHonorsCallerDeadlineAndPreservesExternalLock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var factories atomic.Int32
		service, source, directory := prepareService(t, speechParts(1), func(context.Context, config.Settings) (Transcriber, error) {
			factories.Add(1)
			return nil, errors.New("a locked cache must not send any requests")
		})
		hash, err := sourceHash(t.Context(), source)
		if err != nil {
			t.Fatal(err)
		}
		id, err := fingerprint(hash, config.Default())
		if err != nil {
			t.Fatal(err)
		}
		lockPath := filepath.Join(directory, "jobs-go-v1", id, ".lock")
		if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(lockPath, []byte("external owner"), 0o600); err != nil {
			t.Fatal(err)
		}
		holder, err := platform.TryLock(lockPath)
		if err != nil {
			t.Fatal(err)
		}
		defer holder.Close()
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		var updates []Progress
		result, err := service.Transcribe(ctx, source, config.Default(), Request{
			OnProgress: func(progress Progress) { updates = append(updates, progress) },
		})
		if !errors.Is(err, context.DeadlineExceeded) || result.Text != "" || factories.Load() != 0 {
			t.Fatalf("an external cache wait ignored its deadline or sent a request: %+v / %v", result, err)
		}
		var waits int
		for _, progress := range updates {
			if progress.Stage == "wait" {
				waits++
				requireWaitingProgress(t, progress)
			}
		}
		if waits != 1 {
			t.Fatalf("waiting progress = %d updates, want one stable status", waits)
		}
		requireCacheLocked(t, lockPath)
		if err := holder.Close(); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(lockPath)
		if err != nil || string(data) != "external owner" {
			t.Fatalf("waiting replaced or removed the external lock file: %q / %v", data, err)
		}
	})
}

type cacheTranscriptionOutcome struct {
	result Result
	err    error
}

func startCacheTranscription(ctx context.Context, service *Service, source string, request Request) <-chan cacheTranscriptionOutcome {
	done := make(chan cacheTranscriptionOutcome, 1)
	go func() {
		result, err := service.Transcribe(ctx, source, config.Default(), request)
		done <- cacheTranscriptionOutcome{result: result, err: err}
	}()
	return done
}

func receiveCacheTranscription(t *testing.T, done <-chan cacheTranscriptionOutcome, timeout time.Duration) cacheTranscriptionOutcome {
	t.Helper()
	select {
	case outcome := <-done:
		return outcome
	case <-time.After(timeout):
		t.Fatal("transcription did not return within its expected lifetime")
		return cacheTranscriptionOutcome{}
	}
}

func cacheWaitReporter(waiting chan<- Progress) func(Progress) {
	return func(progress Progress) {
		if progress.Stage == "wait" {
			select {
			case waiting <- progress:
			default:
			}
		}
	}
}

func requireCacheWait(t *testing.T, waiting <-chan Progress, done <-chan cacheTranscriptionOutcome) {
	t.Helper()
	select {
	case progress := <-waiting:
		requireWaitingProgress(t, progress)
	case outcome := <-done:
		t.Fatalf("retry returned instead of waiting for the previous cache: %+v / %v", outcome.result, outcome.err)
	case <-time.After(2 * time.Second):
		t.Fatal("retry did not report that the previous attempt still owns the cache")
	}
}

func requireWaitingProgress(t *testing.T, progress Progress) {
	t.Helper()
	if progress.Stage != "wait" || !strings.Contains(progress.Message, "tentativa anterior") ||
		progress.Fraction != 0 || progress.Completed != 0 || progress.Total != 0 {
		t.Fatalf("cache wait pretended to complete audio or omitted its status: %+v", progress)
	}
}

func cacheLockPath(t *testing.T, directory string) string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(directory, "jobs-go-v1", "*", ".lock"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("expected one cache lock: %v / %v", paths, err)
	}
	return paths[0]
}

func requireCacheLocked(t *testing.T, path string) {
	t.Helper()
	lock, err := platform.TryLock(path)
	if lock != nil {
		if closeErr := lock.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}
	if !errors.Is(err, platform.ErrLocked) {
		t.Fatalf("the previous owner released its cache prematurely: %v", err)
	}
}

func requireSyntheticSourcePreserved(t *testing.T, source string) {
	t.Helper()
	data, err := os.ReadFile(source)
	if err != nil || string(data) != "synthetic input identity" {
		t.Fatalf("waiting, cancelling or retrying modified the source audio: %q / %v", data, err)
	}
}
