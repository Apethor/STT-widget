package transcript

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ai-talker/internal/audio"
	"ai-talker/internal/config"
)

func TestStreamJobsReuseCloudClientAndReportOneTimeSetup(t *testing.T) {
	var factories, closes atomic.Int32
	factory := func(context.Context, config.Settings) (Transcriber, error) {
		factories.Add(1)
		time.Sleep(5 * time.Millisecond)
		return &fakeCloud{
			transcribe: func(context.Context, audio.Chunk) (ChunkResult, error) {
				return ChunkResult{Text: "Fala reconhecida.", Usage: map[string]any{}}, nil
			},
			close: func() error {
				closes.Add(1)
				return nil
			},
		}, nil
	}
	service, firstSource, directory := prepareService(t, speechParts(1), factory)
	secondSource := filepath.Join(directory, "audio-dois.wav")
	if err := os.WriteFile(secondSource, []byte("second synthetic input"), 0o600); err != nil {
		t.Fatal(err)
	}
	for index, source := range []string{firstSource, secondSource} {
		result, err := service.Transcribe(t.Context(), source, config.Default(), Request{Streaming: true})
		if err != nil || result.Text != "Fala reconhecida." || len(result.Parts) != 1 {
			t.Fatalf("stream %d não concluiu: %+v / %v", index+1, result, err)
		}
		setup, ok := result.Parts[0].Usage["cloud_client_setup_seconds"].(float64)
		if !ok || index == 0 && setup <= 0 || index == 1 && setup != 0 {
			t.Fatalf("métrica de criação do cliente no stream %d: %+v", index+1, result.Parts[0].Usage)
		}
	}
	if factories.Load() != 1 || closes.Load() != 0 {
		t.Fatalf("o cliente não foi reutilizado até o encerramento do serviço: criações=%d fechamentos=%d",
			factories.Load(), closes.Load())
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	if closes.Load() != 1 {
		t.Fatalf("o cliente compartilhado não foi encerrado uma única vez: %d", closes.Load())
	}
}

func TestNonStreamingJobsKeepTheirOwnCloudClientLifecycle(t *testing.T) {
	var factories, closes atomic.Int32
	factory := func(context.Context, config.Settings) (Transcriber, error) {
		factories.Add(1)
		return &fakeCloud{
			transcribe: func(context.Context, audio.Chunk) (ChunkResult, error) {
				return ChunkResult{Text: "Fala reconhecida.", Usage: map[string]any{}}, nil
			},
			close: func() error {
				closes.Add(1)
				return nil
			},
		}, nil
	}
	service, firstSource, directory := prepareService(t, speechParts(1), factory)
	secondSource := filepath.Join(directory, "arquivo-dois.wav")
	if err := os.WriteFile(secondSource, []byte("second synthetic input"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{firstSource, secondSource} {
		result, err := service.Transcribe(t.Context(), source, config.Default(), Request{})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Parts) != 1 {
			t.Fatal("a transcrição completa não preservou a parte", result)
		}
		if setup, ok := result.Parts[0].Usage["cloud_client_setup_seconds"].(float64); !ok || setup < 0 {
			t.Fatal("a transcrição completa não registrou preparação do cliente", result.Parts[0].Usage)
		}
	}
	if factories.Load() != 2 || closes.Load() != 2 {
		t.Fatalf("transcrição completa alterou o ciclo de vida anterior: criações=%d fechamentos=%d",
			factories.Load(), closes.Load())
	}
}

func TestConcurrentStreamClientWaitersCancelWithoutClosingTheSharedClient(t *testing.T) {
	t.Parallel()
	creating, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	var factories, closes atomic.Int32
	factory := func(ctx context.Context, _ config.Settings) (Transcriber, error) {
		if factories.Add(1) != 1 {
			t.Error("um waiter iniciou outra criação do mesmo cliente")
		} else {
			close(creating)
		}
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return &fakeCloud{close: func() error { closes.Add(1); return nil }}, nil
	}
	service, _, _ := prepareService(t, speechParts(1), factory)
	if err := service.begin(); err != nil {
		t.Fatal(err)
	}
	firstDone := make(chan error, 1)
	go func() {
		defer service.jobs.Done()
		engine, _, err := service.cloudEngine(t.Context(), config.Default(), factory, true)
		if engine != nil {
			err = errors.Join(err, engine.Close())
		}
		firstDone <- err
	}()
	select {
	case <-creating:
	case <-time.After(2 * time.Second):
		t.Fatal("a criação do cliente compartilhado não começou")
	}
	waiterCtx, cancelWaiter := context.WithCancel(t.Context())
	defer cancelWaiter()
	waiterDone := make(chan error, 1)
	go func() {
		_, _, err := service.cloudEngine(waiterCtx, config.Default(), factory, true)
		waiterDone <- err
	}()
	cancelWaiter()
	select {
	case err := <-waiterDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("o waiter não preservou seu cancelamento", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelar o waiter aguardou a fábrica de outro job")
	}
	if factories.Load() != 1 || closes.Load() != 0 {
		t.Fatal("o waiter alterou o ciclo de vida do cliente", factories.Load(), closes.Load())
	}
	unblock()
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatal("cancelar o waiter cancelou a criação original", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("o cliente original não terminou de preparar")
	}
	var waiters sync.WaitGroup
	for range 8 {
		waiters.Go(func() {
			engine, setup, err := service.cloudEngine(t.Context(), config.Default(), factory, true)
			if err != nil || engine == nil || setup != 0 {
				t.Error("o cliente pronto não foi reutilizado", setup, err)
				return
			}
			if err := engine.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	waiters.Wait()
	if factories.Load() != 1 || closes.Load() != 0 {
		t.Fatal("os jobs fecharam ou recriaram o cliente compartilhado", factories.Load(), closes.Load())
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	if closes.Load() != 1 {
		t.Fatal("o encerramento não fechou uma única vez o cliente compartilhado", closes.Load())
	}
}
