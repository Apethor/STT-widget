package transcript

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"ai-talker/internal/audio"
	"ai-talker/internal/config"
)

type localPreparer struct{ fakePreparer }

func (p localPreparer) Split(ctx context.Context, path string, options audio.ChunkOptions, yield func(audio.Chunk) error) error {
	if options.Format != audio.FormatWAV {
		return errors.New("o serviço local não escolheu WAV")
	}
	return p.fakePreparer.Split(ctx, path, options, yield)
}

func localSettings() config.Settings {
	settings := config.Default()
	settings.Provider = config.ProviderWhisper
	return settings
}

func TestWhisperServiceUsesLocalFactoryAndSeparateModelCache(t *testing.T) {
	var awsCalls, localCalls atomic.Int32
	service, source, _ := prepareService(t, speechParts(1), func(context.Context, config.Settings) (Transcriber, error) {
		awsCalls.Add(1)
		return nil, errors.New("Whisper não pode solicitar AWS")
	})
	service.options.Audio = localPreparer{fakePreparer{chunks: speechParts(1)}}
	service.options.LocalFactory = func(context.Context, config.Settings) (Transcriber, error) {
		return &fakeCloud{transcribe: func(context.Context, audio.Chunk) (ChunkResult, error) {
			localCalls.Add(1)
			return ChunkResult{Text: "Olá, mundo. Hello, world!"}, nil
		}}, nil
	}
	identity := "modelo-base-primeiro"
	service.options.LocalFingerprint = func(context.Context, config.Settings) (string, error) { return identity, nil }
	settings := localSettings()
	first, err := service.Transcribe(t.Context(), source, settings, Request{Workers: 4})
	if err != nil || first.Region != "local" || first.ModelID != "whisper.cpp/base" || awsCalls.Load() != 0 {
		t.Fatalf("a transcrição local usou AWS ou não identificou o motor: %+v, %v", first, err)
	}
	second, err := service.Transcribe(t.Context(), source, settings, Request{})
	if err != nil || second.JobID != first.JobID || second.ReusedParts != 1 || localCalls.Load() != 1 {
		t.Fatalf("o cache local não reutilizou o mesmo modelo: %+v, %v", second, err)
	}
	identity = "modelo-base-substituído"
	third, err := service.Transcribe(t.Context(), source, settings, Request{})
	if err != nil || third.JobID == first.JobID || third.ReusedParts != 0 || localCalls.Load() != 2 {
		t.Fatalf("substituir os pesos reutilizou a transcrição anterior: %+v, %v", third, err)
	}
}

func TestWhisperServiceCancelStopsInferenceAndReleasesSharedCPUSlot(t *testing.T) {
	entered, secondReady, closed := make(chan struct{}), make(chan struct{}), make(chan struct{}, 2)
	var factories, active, peak atomic.Int32
	service, source, directory := prepareService(t, speechParts(1), func(context.Context, config.Settings) (Transcriber, error) {
		return nil, errors.New("AWS não deve iniciar")
	})
	secondSource := filepath.Join(directory, "segundo.wav")
	if err := os.WriteFile(secondSource, []byte("second synthetic input identity"), 0o600); err != nil {
		t.Fatal(err)
	}
	service.options.LocalFactory = func(context.Context, config.Settings) (Transcriber, error) {
		index := factories.Add(1)
		if index == 2 {
			close(secondReady)
		}
		return &fakeCloud{
			transcribe: func(ctx context.Context, _ audio.Chunk) (ChunkResult, error) {
				concurrent := active.Add(1)
				for previous := peak.Load(); concurrent > previous && !peak.CompareAndSwap(previous, concurrent); previous = peak.Load() {
				}
				defer active.Add(-1)
				if index == 1 {
					close(entered)
					<-ctx.Done()
					return ChunkResult{}, ctx.Err()
				}
				return ChunkResult{Text: "Texto do segundo áudio."}, nil
			},
			close: func() error { closed <- struct{}{}; return nil },
		}, nil
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	go func() {
		_, err := service.Transcribe(ctx, source, localSettings(), Request{Workers: 4})
		firstDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("o primeiro modelo local não iniciou")
	}
	go func() {
		_, err := service.Transcribe(t.Context(), secondSource, localSettings(), Request{Workers: 4})
		secondDone <- err
	}()
	select {
	case <-secondReady:
	case <-time.After(3 * time.Second):
		t.Fatal("o segundo pedido não chegou à fila local")
	}
	cancel()
	for _, done := range []struct {
		channel <-chan error
		want    error
	}{{firstDone, context.Canceled}, {secondDone, nil}} {
		select {
		case err := <-done.channel:
			if !errors.Is(err, done.want) {
				t.Fatalf("cancelar não encerrou o primeiro modelo e liberou o segundo: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("a fila local ficou bloqueada depois do cancelamento")
		}
	}
	if peak.Load() != 1 {
		t.Fatalf("mais de um modelo usou a CPU simultaneamente: %d", peak.Load())
	}
	for range 2 {
		select {
		case <-closed:
		case <-time.After(3 * time.Second):
			t.Fatal("o transcritor local não foi liberado")
		}
	}
}

func TestWhisperSilenceRequiresNeitherInstalledModelNorAWS(t *testing.T) {
	service, source, _ := prepareService(t, []audio.Chunk{{Duration: 2}}, func(context.Context, config.Settings) (Transcriber, error) {
		t.Error("silêncio inicializou AWS")
		return nil, errors.New("AWS não deve iniciar")
	})
	service.options.LocalFactory = func(context.Context, config.Settings) (Transcriber, error) {
		t.Error("silêncio inicializou um modelo local")
		return nil, errors.New("modelo não instalado")
	}
	result, err := service.Transcribe(t.Context(), source, localSettings(), Request{})
	if err != nil || result.Text != "" || result.NeedsReview || result.Region != "local" {
		t.Fatalf("silêncio exigiu instalação local ou manteve texto antigo: %+v, %v", result, err)
	}
}

func TestWhisperFingerprintIsSeparateAndKeepsBedrockCompatibility(t *testing.T) {
	settings := config.Default()
	settings.Recovery.WhisperDisabled = true
	bedrock, err := fingerprint("audio", settings)
	if err != nil {
		t.Fatal(err)
	}
	settings.Whisper.Model = "tiny"
	settings.Whisper.Threads = 4
	settings.Provider = ""
	unchanged, err := fingerprintWithModel("audio", settings, "irrelevant-local-model")
	if err != nil || unchanged != bedrock {
		t.Fatal("opções locais mudaram o cache Bedrock com fallback desligado")
	}
	settings.Provider = config.ProviderWhisper
	local, err := fingerprintWithModel("audio", settings, "local-model")
	if err != nil || local == bedrock {
		t.Fatal("Bedrock e Whisper compartilharam a identidade do cache")
	}
	settings.Whisper.Language = "pt"
	changed, err := fingerprintWithModel("audio", settings, "local-model")
	if err != nil || changed == local {
		t.Fatal("a escolha de idioma reutilizou um cache local incompatível")
	}
}

func TestFallbackModelChangesRecoveryCacheWithoutChangingConcurrencyIdentity(t *testing.T) {
	settings := config.Default()
	first, err := fingerprintWithModel("audio", settings, "model-one")
	if err != nil {
		t.Fatal(err)
	}
	second, _ := fingerprintWithModel("audio", settings, "model-two")
	if first == second {
		t.Fatal("a troca de pesos reutilizou texto de um fallback diferente")
	}
	settings.Recovery.Concurrency = 4
	concurrent, _ := fingerprintWithModel("audio", settings, "model-two")
	if concurrent != second {
		t.Fatal("o ajuste de carga invalidou partes já aceitas")
	}
}
