package transcript

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"ai-talker/internal/audio"
	"ai-talker/internal/config"
)

type temporaryCloudFailure struct{ cause error }

func (e temporaryCloudFailure) Error() string   { return "falha temporária simulada" }
func (e temporaryCloudFailure) Unwrap() error   { return e.cause }
func (e temporaryCloudFailure) Retryable() bool { return true }

func TestCloudTimeoutRetriesWithFreshDeadlinesAndReportsProgress(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := make(map[int]int)
		var deadlines []time.Time
		var updates []Progress
		factory := func(context.Context, config.Settings) (Transcriber, error) {
			return &fakeCloud{transcribe: func(ctx context.Context, chunk audio.Chunk) (ChunkResult, error) {
				calls[chunk.Index]++
				if chunk.Index == 1 {
					deadline, _ := ctx.Deadline()
					deadlines = append(deadlines, deadline)
					if calls[chunk.Index] < 3 {
						<-ctx.Done()
						return ChunkResult{}, temporaryCloudFailure{cause: ctx.Err()}
					}
				}
				return ChunkResult{Text: fmt.Sprintf("Parte %d.", chunk.Index+1)}, nil
			}}, nil
		}
		service, source, _ := prepareService(t, speechParts(3), factory)
		result, err := service.Transcribe(t.Context(), source, config.Default(), Request{
			Workers: 1, OnProgress: func(progress Progress) { updates = append(updates, progress) },
		})
		if err != nil || result.NeedsReview || result.Text != "Parte 1.\n\nParte 2.\n\nParte 3." ||
			calls[0] != 1 || calls[1] != 3 || calls[2] != 1 {
			t.Fatalf("temporary timeout lost text or repeated completed parts: %+v, %v, calls=%v", result, err, calls)
		}
		if len(deadlines) != 3 || !deadlines[1].After(deadlines[0]) || !deadlines[2].After(deadlines[1]) {
			t.Fatalf("a retry inherited an exhausted deadline: %v", deadlines)
		}
		retries := slices.DeleteFunc(updates, func(progress Progress) bool { return progress.Stage != "retry" })
		if len(retries) != 2 || retries[0].Completed != 1 || retries[1].Completed != 1 ||
			!strings.Contains(retries[0].Message, "2 de 3") || !strings.Contains(retries[1].Message, "3 de 3") ||
			result.Parts[1].Usage["attempts"] != 3 {
			t.Fatalf("retry feedback or accounting was lost: %+v / %+v", retries, result.Parts[1])
		}
	})
}

func TestExhaustedCloudTimeoutKeepsCacheAndReopenResumesOnlyMissingParts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := make(map[int]int)
		fail := true
		factory := func(context.Context, config.Settings) (Transcriber, error) {
			return &fakeCloud{transcribe: func(ctx context.Context, chunk audio.Chunk) (ChunkResult, error) {
				calls[chunk.Index]++
				if chunk.Index == 1 && fail {
					<-ctx.Done()
					return ChunkResult{}, temporaryCloudFailure{cause: ctx.Err()}
				}
				return ChunkResult{Text: fmt.Sprintf("Parte %d.", chunk.Index+1)}, nil
			}}, nil
		}
		service, source, directory := prepareService(t, speechParts(3), factory)
		_, err := service.Transcribe(t.Context(), source, config.Default(), Request{Workers: 1})
		var failure *Error
		if !errors.As(err, &failure) || !errors.Is(err, context.DeadlineExceeded) ||
			failure.PartialText != "Parte 1." || calls[1] != maxCloudAttempts ||
			!strings.Contains(err.Error(), "após 3 tentativas") {
			t.Fatalf("exhausted retries lost their cause, limit or partial: %v / %v", err, calls)
		}
		if _, err := os.Stat(filepath.Join(directory, "jobs-go-v1", failure.JobID, "part-00001.json")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("a failed timeout was cached as completed: %v", err)
		}
		if err := service.Close(); err != nil {
			t.Fatal(err)
		}
		fail = false
		reopened, err := New(t.Context(), Options{
			DataDir: directory, Audio: fakePreparer{chunks: speechParts(3)}, CloudFactory: factory,
		})
		if err != nil {
			t.Fatal(err)
		}
		defer reopened.Close()
		result, err := reopened.Transcribe(t.Context(), source, config.Default(), Request{Workers: 1})
		if err != nil || result.ReusedParts != 1 || result.DurationSeconds != 30 || len(result.Parts) != 3 ||
			calls[0] != 1 || calls[1] != maxCloudAttempts+1 || calls[2] != 1 {
			t.Fatalf("reopening lost completed work or final coverage: %+v / %v / %v", result, err, calls)
		}
	})
}

func TestCancelDuringCloudBackoffStartsNoNewAttempt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		factory := func(context.Context, config.Settings) (Transcriber, error) {
			return &fakeCloud{transcribe: func(context.Context, audio.Chunk) (ChunkResult, error) {
				calls.Add(1)
				return ChunkResult{}, temporaryCloudFailure{cause: errors.New("temporary service outage")}
			}}, nil
		}
		service, source, _ := prepareService(t, speechParts(1), factory)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		_, err := service.Transcribe(ctx, source, config.Default(), Request{
			Workers: 1, OnProgress: func(progress Progress) {
				if progress.Stage == "retry" {
					cancel()
				}
			},
		})
		service.jobs.Wait()
		if !errors.Is(err, context.Canceled) || calls.Load() != 1 {
			t.Fatalf("Cancel waited for backoff or allowed another request: %v / %d", err, calls.Load())
		}
	})
}

func TestCancelLetsSentCloudCallFinishButPreventsItsRetry(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	var calls atomic.Int32
	factory := func(context.Context, config.Settings) (Transcriber, error) {
		return &fakeCloud{transcribe: func(context.Context, audio.Chunk) (ChunkResult, error) {
			calls.Add(1)
			close(entered)
			<-release
			return ChunkResult{}, temporaryCloudFailure{cause: context.DeadlineExceeded}
		}}, nil
	}
	service, source, _ := prepareService(t, speechParts(1), factory)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	returned := make(chan error, 1)
	go func() {
		_, err := service.Transcribe(ctx, source, config.Default(), Request{Workers: 1})
		returned <- err
	}()
	<-entered
	cancel()
	if err := <-returned; !errors.Is(err, context.Canceled) {
		t.Fatalf("foreground cancellation = %v", err)
	}
	unblock()
	service.jobs.Wait()
	if calls.Load() != 1 {
		t.Fatal("a detached, failed call started new attempts after Cancel")
	}
}

func TestCloudRetryLeavesPermanentErrorsAndOtherEnginesToTheirOwner(t *testing.T) {
	cases := []struct {
		name     string
		settings config.Settings
		err      error
	}{
		{name: "permanent failure", settings: config.Default(), err: errors.New("permission denied")},
		{name: "caller cancelled", settings: config.Default(), err: temporaryCloudFailure{cause: context.Canceled}},
		{name: "local engine", settings: localSettings(), err: temporaryCloudFailure{cause: context.DeadlineExceeded}},
		{name: "Transcribe owns protocol retries", settings: config.Default(), err: temporaryCloudFailure{cause: context.DeadlineExceeded}},
	}
	cases[3].settings.Cloud.ModelID = config.TranscribeStreaming
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			factory := func(context.Context, config.Settings) (Transcriber, error) {
				return &fakeCloud{transcribe: func(context.Context, audio.Chunk) (ChunkResult, error) {
					calls.Add(1)
					return ChunkResult{}, tc.err
				}}, nil
			}
			service, source, _ := prepareService(t, speechParts(1), factory)
			service.options.LocalFactory = factory
			_, err := service.Transcribe(t.Context(), source, tc.settings, Request{Workers: 1})
			if !errors.Is(err, tc.err) || calls.Load() != 1 {
				t.Fatalf("permanent error or another engine was retried: %v / %d", err, calls.Load())
			}
		})
	}
}

func TestAutomaticCloudDeadlineFitsLongPartsAndExplicitDeadlineStillWins(t *testing.T) {
	for _, override := range []time.Duration{0, 5 * time.Second} {
		t.Run(override.String(), func(t *testing.T) {
			chunks := []audio.Chunk{
				{Index: 0, Duration: 120, HasSpeech: true, Audio: []byte("mock")},
				{Index: 1, Start: 120, Duration: 300, HasSpeech: true, Audio: []byte("mock")},
			}
			factory := func(context.Context, config.Settings) (Transcriber, error) {
				return &fakeCloud{transcribe: func(ctx context.Context, chunk audio.Chunk) (ChunkResult, error) {
					want := BedrockPartTimeout(chunk.Duration)
					if override > 0 {
						want = override
					}
					deadline, ok := ctx.Deadline()
					if left := time.Until(deadline); !ok || left > want || left < want-time.Second {
						t.Errorf("duration=%v deadline=%v, want %v", chunk.Duration, left, want)
					}
					return ChunkResult{Text: "Texto completo."}, nil
				}}, nil
			}
			directory := t.TempDir()
			source := filepath.Join(directory, "long.wav")
			if err := os.WriteFile(source, []byte("synthetic"), 0o600); err != nil {
				t.Fatal(err)
			}
			service, err := New(t.Context(), Options{
				DataDir: directory, Audio: fakePreparer{chunks: chunks}, CloudFactory: factory,
				RequestTimeout: override,
			})
			if err != nil {
				t.Fatal(err)
			}
			defer service.Close()
			if _, err := service.Transcribe(t.Context(), source, config.Default(), Request{Workers: 1}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
