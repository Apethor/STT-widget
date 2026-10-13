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
)

type recoveryAudio struct{ fakePreparer }

func TestVoxtralOnlyUnintelligibleMarkersRecoverWithoutReplacingRecognizedParts(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		local bool
	}{
		{name: "portuguese marker", text: "[inaudível]", local: true},
		{name: "uppercase and punctuation", text: " [INAUDÍVEL]… ", local: true},
		{name: "multiple markers", text: "[inaudível]. [incompreensível]", local: true},
		{name: "english marker", text: "[unintelligible]", local: true},
		{name: "parentheses", text: "(inaudível)", local: true},
		{name: "partly recognized", text: "Olá, frase 1. [inaudível]"},
		{name: "literal word", text: "Falei a palavra inaudível."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var localCalls atomic.Int32
			service, source, _ := prepareService(t, speechParts(2),
				func(context.Context, config.Settings) (Transcriber, error) {
					return &fakeCloud{transcribe: func(_ context.Context, chunk audio.Chunk) (ChunkResult, error) {
						if chunk.Index == 0 {
							return ChunkResult{Text: "Primeira frase reconhecida."}, nil
						}
						return ChunkResult{Text: tc.text}, nil
					}}, nil
				})
			service.options.Audio = recoveryAudio{service.options.Audio.(fakePreparer)}
			service.options.LocalFactory = func(context.Context, config.Settings) (Transcriber, error) {
				return &fakeCloud{transcribe: func(context.Context, audio.Chunk) (ChunkResult, error) {
					localCalls.Add(1)
					return ChunkResult{Text: "Segunda frase recuperada."}, nil
				}}, nil
			}
			result, err := service.Transcribe(t.Context(), source, config.Default(), Request{})
			wantCalls := int32(0)
			wantText := strings.TrimSpace(tc.text)
			if tc.local {
				wantCalls = 1
				wantText = "Segunda frase recuperada."
			}
			if err != nil || localCalls.Load() != wantCalls || len(result.Parts) != 2 ||
				result.Parts[0].Text != "Primeira frase reconhecida." || result.Parts[1].Text != wantText {
				t.Fatalf("recuperação alterou fala aceita ou ignorou marcador: %+v / %v / %d", result, err, localCalls.Load())
			}
		})
	}
}

func TestCacheWriteFailureKeepsTextButCannotCountAsCompletedOrTriggerFallback(t *testing.T) {
	for _, child := range []bool{false, true} {
		t.Run(fmt.Sprintf("subdivision=%t", child), func(t *testing.T) {
			var directory string
			var localCalls atomic.Int32
			factory := func(context.Context, config.Settings) (Transcriber, error) {
				roots, err := filepath.Glob(filepath.Join(directory, "jobs-go-v1", "*"))
				if err != nil || len(roots) != 1 {
					return nil, fmt.Errorf("localizar cache: %v / %v", roots, err)
				}
				name := "part-00000.json"
				if child {
					name = "recovery-00000-0.json"
				}
				if err := os.Mkdir(filepath.Join(roots[0], name), 0o700); err != nil {
					return nil, err
				}
				return &fakeCloud{transcribe: func(_ context.Context, chunk audio.Chunk) (ChunkResult, error) {
					if child && chunk.Duration > 10 {
						return ChunkResult{}, ErrOutputLimit
					}
					return ChunkResult{Text: "Texto disponível apesar da falha no disco."}, nil
				}}, nil
			}
			duration := 10.0
			if child {
				duration = 20
			}
			chunks := []audio.Chunk{{Duration: duration, HasSpeech: true, SpeechSeconds: duration}}
			service, source, data := prepareService(t, chunks, factory)
			directory = data
			service.options.Audio = recoveryAudio{fakePreparer{chunks}}
			service.options.LocalFactory = func(context.Context, config.Settings) (Transcriber, error) {
				localCalls.Add(1)
				return nil, errors.New("falha de disco não pode iniciar Whisper")
			}
			var progress Progress
			result, err := service.Transcribe(t.Context(), source, config.Default(), Request{
				ContinueOnError: true, OnProgress: func(update Progress) { progress = update },
			})
			var failure *Error
			if !errors.As(err, &failure) || !result.NeedsReview || len(result.Parts) != 1 ||
				result.Parts[0].Error == "" || !strings.Contains(failure.PartialText, "Texto disponível") ||
				progress.Completed != 0 || localCalls.Load() != 0 {
				t.Fatalf("falha de cache contou sucesso, perdeu texto ou chamou fallback: %+v / %v / %+v", result, err, progress)
			}
			if child && (len(result.Parts[0].Children) != 2 || !result.Parts[0].Children[0].NeedsReview ||
				result.Parts[0].Children[0].Error == "") {
				t.Fatal("a falha do cache de um filho desapareceu nos metadados")
			}
		})
	}
}

func (p recoveryAudio) Subdivide(ctx context.Context, chunk audio.Chunk) ([]audio.Chunk, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	half := chunk.Duration / 2
	return []audio.Chunk{
		{Index: 0, Start: chunk.Start, Duration: half, HasSpeech: chunk.HasSpeech, SpeechSeconds: half, Audio: chunk.Audio, PCM: chunk.PCM},
		{Index: 1, Start: chunk.Start + half, Duration: half, HasSpeech: chunk.HasSpeech, SpeechSeconds: half, Audio: chunk.Audio, PCM: chunk.PCM},
	}, nil
}

func (p recoveryAudio) RecoveryWAV(ctx context.Context, chunk audio.Chunk) (audio.Chunk, error) {
	payload, err := audio.EncodeWAV(ctx, []byte{1, 0, 2, 0, 3, 0})
	chunk.Audio, chunk.Format = payload, audio.FormatWAV
	return chunk, err
}

func TestOutputLimitSubdividesOnlyItsPartUnderOneCloudSlotAndReusesAfterReopen(t *testing.T) {
	chunks := []audio.Chunk{
		{Index: 0, Start: 0, Duration: 10, HasSpeech: true, Audio: []byte("first")},
		{Index: 1, Start: 10, Duration: 40, HasSpeech: true, Audio: []byte("dense")},
		{Index: 2, Start: 50, Duration: 10, HasSpeech: true, Audio: []byte("last")},
	}
	var calls atomic.Int32
	factory := func(context.Context, config.Settings) (Transcriber, error) {
		return &fakeCloud{transcribe: func(ctx context.Context, chunk audio.Chunk) (ChunkResult, error) {
			if err := ctx.Err(); err != nil {
				return ChunkResult{}, err
			}
			calls.Add(1)
			if chunk.Duration > 10 {
				return ChunkResult{}, ErrOutputLimit
			}
			return ChunkResult{Text: fmt.Sprintf("Fala em %.0f segundos.", chunk.Start)}, nil
		}}, nil
	}
	service, source, directory := prepareService(t, chunks, factory)
	service.options.Audio = recoveryAudio{fakePreparer{chunks}}
	if err := service.SetConcurrency(1); err != nil {
		t.Fatal(err)
	}
	settings := config.Default()
	settings.Recovery.WhisperDisabled = true
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	first, err := service.Transcribe(ctx, source, settings, Request{Workers: 3, ContinueOnError: true})
	if err != nil || first.NeedsReview || first.DurationSeconds != 60 || len(first.Parts) != 3 ||
		len(first.Parts[1].Children) != 2 || !strings.Contains(first.Text, "50 segundos") {
		t.Fatalf("a recuperação perdeu cobertura/ordem ou segurou sua própria vaga: %+v / %v", first, err)
	}
	count := calls.Load()
	if count != 9 {
		t.Fatalf("as partes comuns foram subdivididas/repetidas: chamadas=%d", count)
	}
	reopened, err := New(t.Context(), Options{
		DataDir: directory, Audio: recoveryAudio{fakePreparer{chunks}}, CloudFactory: factory, MaxRequests: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	second, err := reopened.Transcribe(t.Context(), source, settings, Request{ContinueOnError: true})
	if err != nil || calls.Load() != count || second.Text != first.Text || second.ReusedParts != 3 {
		t.Fatalf("a reabertura reenviou partes recuperadas: %+v / %v / %d", second, err, calls.Load())
	}
}

func TestSubdivisionPlanResumesOnlyFailedChildrenAndPublishesAvailableText(t *testing.T) {
	chunks := []audio.Chunk{{Duration: 20, HasSpeech: true, Audio: []byte("dense")}}
	var mu sync.Mutex
	calls := make(map[string]int)
	fail := true
	factory := func(context.Context, config.Settings) (Transcriber, error) {
		return &fakeCloud{transcribe: func(_ context.Context, chunk audio.Chunk) (ChunkResult, error) {
			mu.Lock()
			defer mu.Unlock()
			key := fmt.Sprintf("%.0f/%.0f", chunk.Start, chunk.Duration)
			calls[key]++
			if chunk.Duration > 10 || fail && chunk.Start >= 10 {
				return ChunkResult{}, ErrOutputLimit
			}
			return ChunkResult{Text: fmt.Sprintf("Fala aceita em %.0f.", chunk.Start)}, nil
		}}, nil
	}
	service, source, _ := prepareService(t, chunks, factory)
	service.options.Audio = recoveryAudio{fakePreparer{chunks}}
	settings := config.Default()
	settings.Recovery.WhisperDisabled = true
	first, err := service.Transcribe(t.Context(), source, settings, Request{ContinueOnError: true})
	var failure *Error
	if !errors.As(err, &failure) || !errors.Is(err, ErrOutputLimit) ||
		!strings.Contains(failure.PartialText, "aceita em 0") || !first.NeedsReview || first.DurationSeconds != 20 {
		t.Fatalf("um filho aceito desapareceu no erro do outro: %+v / %v", first, err)
	}
	mu.Lock()
	fail = false
	mu.Unlock()
	second, err := service.Transcribe(t.Context(), source, settings, Request{ContinueOnError: true})
	mu.Lock()
	rootCalls, acceptedCalls := calls["0/20"], calls["0/10"]
	mu.Unlock()
	if err != nil || second.NeedsReview || rootCalls != 1 || acceptedCalls != 1 ||
		!strings.Contains(second.Text, "aceita em 15") {
		t.Fatalf("a retomada ignorou o plano ou reenviou um filho aceito: %+v / %v / %v", second, err, calls)
	}
}

func TestRecoveryBudgetBoundsAContinuouslyTruncatedPart(t *testing.T) {
	var calls atomic.Int32
	service, source, _ := prepareService(t, []audio.Chunk{{Duration: 300, HasSpeech: true}}, func(context.Context, config.Settings) (Transcriber, error) {
		return &fakeCloud{transcribe: func(context.Context, audio.Chunk) (ChunkResult, error) {
			calls.Add(1)
			return ChunkResult{}, ErrOutputLimit
		}}, nil
	})
	service.options.Audio = recoveryAudio{service.options.Audio.(fakePreparer)}
	settings := config.Default()
	settings.Recovery.WhisperDisabled = true
	result, err := service.Transcribe(t.Context(), source, settings, Request{ContinueOnError: true})
	if !errors.Is(err, ErrOutputLimit) || !result.NeedsReview || calls.Load() > maxRecoveryCalls ||
		result.DurationSeconds != 300 {
		t.Fatalf("a árvore multiplicou chamadas ou apresentou uma resposta completa: %+v / %v / %d", result, err, calls.Load())
	}
}

func TestVoxtralEmptySpeechFallsBackToPreparedWhisperAndKeepsCloudPreference(t *testing.T) {
	var cloudCalls, localCalls atomic.Int32
	service, source, _ := prepareService(t, speechParts(1), func(context.Context, config.Settings) (Transcriber, error) {
		return &fakeCloud{transcribe: func(context.Context, audio.Chunk) (ChunkResult, error) {
			cloudCalls.Add(1)
			return ChunkResult{}, nil
		}}, nil
	})
	service.options.Audio = recoveryAudio{service.options.Audio.(fakePreparer)}
	service.options.LocalFactory = func(_ context.Context, settings config.Settings) (Transcriber, error) {
		if settings.TranscriptionProvider() != config.ProviderWhisper {
			t.Error("o fallback não recebeu as opções locais")
		}
		return &fakeCloud{transcribe: func(_ context.Context, chunk audio.Chunk) (ChunkResult, error) {
			localCalls.Add(1)
			if chunk.Format != audio.FormatWAV || string(chunk.Audio[:4]) != "RIFF" {
				t.Error("o Whisper recebeu os bytes MP3 do Cloud")
			}
			return ChunkResult{Text: "Texto recuperado localmente."}, nil
		}}, nil
	}
	settings := config.Default()
	first, err := service.Transcribe(t.Context(), source, settings, Request{})
	if err != nil || first.NeedsReview || first.Text != "Texto recuperado localmente." ||
		first.Parts[0].Engine != "whisper.cpp/"+settings.Whisper.Model ||
		first.ModelID != config.VoxtralSmall || first.Region != settings.Region ||
		settings.TranscriptionProvider() != config.ProviderBedrock {
		t.Fatalf("fallback perdeu proveniência ou alterou a preferência: %+v / %v", first, err)
	}
	second, err := service.Transcribe(t.Context(), source, settings, Request{})
	if err != nil || second.ReusedParts != 1 || cloudCalls.Load() != 1 || localCalls.Load() != 1 {
		t.Fatalf("o cache reenviou a parte recuperada: %+v / %v", second, err)
	}
}

func TestFallbackOptOutAndNonVoxtralOrStreamNeverCallLocal(t *testing.T) {
	for _, mode := range []string{"opt-out", "mini", "stream", "silence"} {
		t.Run(mode, func(t *testing.T) {
			chunks := speechParts(1)
			if mode == "silence" {
				chunks[0].HasSpeech = false
			}
			service, source, _ := prepareService(t, chunks, func(context.Context, config.Settings) (Transcriber, error) {
				return &fakeCloud{transcribe: func(context.Context, audio.Chunk) (ChunkResult, error) { return ChunkResult{}, nil }}, nil
			})
			service.options.Audio = recoveryAudio{fakePreparer{chunks}}
			service.options.LocalFactory = func(context.Context, config.Settings) (Transcriber, error) {
				t.Error("o fallback saiu do escopo autorizado")
				return nil, errors.New("não deveria iniciar")
			}
			settings := config.Default()
			settings.Recovery.WhisperDisabled = mode == "opt-out"
			if mode == "mini" {
				settings.Cloud.ModelID = config.VoxtralMini
			}
			if _, err := service.Transcribe(t.Context(), source, settings, Request{Streaming: mode == "stream"}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestContinueOnErrorKeepsLaterAcceptedPartsWithoutPublishingCompleteSnapshot(t *testing.T) {
	service, source, directory := prepareService(t, speechParts(3), func(context.Context, config.Settings) (Transcriber, error) {
		return &fakeCloud{transcribe: func(_ context.Context, chunk audio.Chunk) (ChunkResult, error) {
			if chunk.Index == 0 {
				return ChunkResult{}, errors.New("falha permanente")
			}
			return ChunkResult{Text: fmt.Sprintf("Parte %d aceita.", chunk.Index+1)}, nil
		}}, nil
	})
	result, err := service.Transcribe(t.Context(), source, config.Default(), Request{ContinueOnError: true, PersistLatest: true})
	var failure *Error
	if !errors.As(err, &failure) || result.DurationSeconds != 30 || !result.NeedsReview ||
		!strings.Contains(failure.PartialText, "Parte 2 aceita.") || !strings.Contains(failure.PartialText, "Parte 3 aceita.") {
		t.Fatalf("falhar no começo descartou respostas posteriores: %+v / %v", result, err)
	}
	if result.Exports != nil {
		t.Fatal("uma resposta incompleta recebeu exportações de resultado completo", directory)
	}
}

func TestCancelledAdmissionSendsNothingEvenAfterProgressWasEmitted(t *testing.T) {
	var calls atomic.Int32
	service, source, _ := prepareService(t, speechParts(1), func(context.Context, config.Settings) (Transcriber, error) {
		return &fakeCloud{transcribe: func(context.Context, audio.Chunk) (ChunkResult, error) {
			calls.Add(1)
			return ChunkResult{Text: "não deveria enviar"}, nil
		}}, nil
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	_, err := service.Transcribe(ctx, source, config.Default(), Request{OnProgress: func(progress Progress) {
		if progress.Stage == "transcribe" {
			cancel()
		}
	}})
	service.Close()
	if !errors.Is(err, context.Canceled) || calls.Load() != 0 {
		t.Fatalf("a primeira tentativa começou depois de Cancelar: %v / %d", err, calls.Load())
	}
}
