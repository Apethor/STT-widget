package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ai-talker/internal/config"
	"ai-talker/internal/transcript"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/aws/aws-sdk-go-v2/service/transcribestreaming"
	streamtypes "github.com/aws/aws-sdk-go-v2/service/transcribestreaming/types"
)

func requireMeasuredPhases(t *testing.T, usage map[string]any, names ...string) {
	t.Helper()
	for _, name := range names {
		value, ok := usage[name].(float64)
		if !ok || value < 0 {
			t.Errorf("a fase %s não foi registrada em segundos: %+v", name, usage)
		}
	}
	if elapsed, ok := usage["first_text_seconds"].(float64); !ok || elapsed < 0 {
		t.Errorf("a chegada do primeiro texto não foi registrada: %+v", usage)
	}
}

func TestCloudFileReportsFirstTextWithoutChangingTheRequest(t *testing.T) {
	t.Parallel()
	var calls int
	engine, err := NewWithClient(config.Default(), converseFunc(func(_ context.Context, input *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
		calls++
		prompt, ok := input.Messages[0].Content[0].(*types.ContentBlockMemberText)
		if !ok || !strings.Contains(prompt.Value, "Transcribe this audio verbatim") {
			t.Error("a instrumentação alterou a instrução de transcrição completa")
		}
		return completeResponse("Texto completo.", types.StopReasonEndTurn), nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	result, err := engine.Transcribe(t.Context(), spokenChunk(10))
	if err != nil || result.Text != "Texto completo." || calls != 1 {
		t.Fatalf("a instrumentação alterou o reconhecimento: %+v %v, envios=%d", result, err, calls)
	}
	requireMeasuredPhases(t, result.Usage, "model_request_seconds")
	if result.Usage["first_text_stage"] != "final" {
		t.Fatalf("a transcrição completa foi apresentada como parcial: %+v", result.Usage)
	}
}

func TestCloudCallbackWithConverseRecordsTheFinalResponseStage(t *testing.T) {
	t.Parallel()
	engine, err := NewWithClient(config.Default(), converseFunc(func(context.Context, *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
		return completeResponse("Texto completo.", types.StopReasonEndTurn), nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	var received []string
	result, err := engine.(transcript.StreamingTranscriber).TranscribeStream(t.Context(), spokenChunk(10),
		func(text string) { received = append(received, text) })
	if err != nil || result.Text != "Texto completo." || len(received) != 1 ||
		received[0] != result.Text || result.Usage["first_text_stage"] != "final" {
		t.Fatalf("um callback apresentou a resposta final de Converse como delta provisório: %+v %v %v", result, received, err)
	}
}

func TestNativeStreamsMeasureFirstTextWithoutAPartialCallback(t *testing.T) {
	t.Parallel()
	t.Run("transcribe", func(t *testing.T) {
		t.Parallel()
		stream := &fakeTranscribeAudioStream{events: make(chan streamtypes.TranscriptResultStream, 2)}
		stream.events <- transcribeEvent("one", "Parcial.", 0, true)
		stream.events <- transcribeEvent("one", "Texto final.", 0, false)
		close(stream.events)
		engine := &transcribeStreaming{settings: config.Default(),
			start: func(context.Context, *transcribestreaming.StartStreamTranscriptionInput) (transcribeAudioStream, error) {
				return stream, nil
			}}
		result, err := engine.Transcribe(t.Context(), pcmSpeech(t, 1))
		if err != nil || result.Text != "Texto final." || stream.closed.Load() != 1 {
			t.Fatalf("a medição alterou o resultado final: %+v %v", result, err)
		}
		requireMeasuredPhases(t, result.Usage,
			"request_open_seconds", "audio_send_seconds", "input_finalize_seconds", "stream_close_seconds")
		if result.Usage["first_text_stage"] != "partial" {
			t.Fatalf("a primeira resposta provisória não foi distinguida da final: %+v", result.Usage)
		}
	})
	t.Run("sonic", func(t *testing.T) {
		t.Parallel()
		stream := &fakeSonicStream{events: make(chan types.InvokeModelWithBidirectionalStreamOutput, 3)}
		sonicText(t, stream, "one", "USER", "FINAL", "Texto final.")
		close(stream.events)
		settings := config.Default()
		settings.Cloud.ModelID = config.NovaSonic25
		engine := &sonicTranscriber{settings: settings,
			start: func(context.Context, string) (sonicStream, error) { return stream, nil },
			wait:  func(ctx context.Context, _ time.Duration) error { return ctx.Err() }}
		result, err := engine.Transcribe(t.Context(), pcmSpeech(t, 1))
		if err != nil || result.Text != "Texto final." || stream.closed.Load() != 1 {
			t.Fatalf("a medição alterou a fala original: %+v %v", result, err)
		}
		requireMeasuredPhases(t, result.Usage,
			"request_open_seconds", "session_setup_seconds", "audio_send_seconds", "input_finalize_seconds", "stream_close_seconds")
		if result.Usage["first_text_stage"] != "final" {
			t.Fatalf("um bloco final de ditado foi apresentado como hipótese: %+v", result.Usage)
		}
	})
}

func TestTranscribeBatchMeasuresUploadJobDownloadAndCleanup(t *testing.T) {
	t.Parallel()
	engine, calls := batchTestEngine(t)
	result, err := engine.Transcribe(t.Context(), pcmSpeech(t, 1))
	if err != nil || result.Text != "Texto reconhecido." || len(calls.deletedKeys) != 2 || len(calls.deletedJobs) != 1 {
		t.Fatalf("a medição alterou o arquivo ou a limpeza: %+v %+v %v", result, calls, err)
	}
	requireMeasuredPhases(t, result.Usage,
		"audio_upload_seconds", "job_start_seconds", "job_poll_seconds", "job_wait_seconds", "transcript_download_seconds", "cleanup_seconds")
	if result.Usage["job_polls"] != 1 || result.Usage["first_text_stage"] != "final" {
		t.Fatalf("a espera ou o texto final não foram identificados: %+v", result.Usage)
	}
}

func TestSonicIncompleteErrorsNameTheSelectedModel(t *testing.T) {
	t.Parallel()
	for _, model := range []struct{ id, name string }{
		{config.NovaSonic, "Nova 2 Sonic"},
		{config.NovaSonic25, "Nova 2.5 Sonic"},
	} {
		t.Run(model.name, func(t *testing.T) {
			t.Parallel()
			settings := config.Default()
			settings.Cloud.ModelID = model.id
			engine := &sonicTranscriber{settings: settings,
				start: func(context.Context, string) (sonicStream, error) { return nil, nil }}
			_, err := engine.Transcribe(t.Context(), pcmSpeech(t, 1))
			if err == nil || !strings.Contains(err.Error(), model.name) ||
				model.id == config.NovaSonic && strings.Contains(err.Error(), "Nova 2.5") {
				t.Fatalf("o erro atribuiu a falha a outro modelo: %v", err)
			}
		})
	}
}

func TestCredentialTimingsStayWithEachConcurrentCallAndKeepTheSDKCache(t *testing.T) {
	t.Parallel()
	var retrievals atomic.Int32
	upstream := aws.CredentialsProviderFunc(func(ctx context.Context) (aws.Credentials, error) {
		retrievals.Add(1)
		if err := ctx.Err(); err != nil {
			return aws.Credentials{}, err
		}
		return aws.Credentials{AccessKeyID: "fake-id", SecretAccessKey: "fake-secret", SessionToken: "fake-token"}, nil
	})
	provider := timedCredentialsProvider{provider: aws.NewCredentialsCache(upstream)}
	if retrievals.Load() != 0 {
		t.Fatal("a criação do wrapper antecipou uma resolução de credenciais")
	}
	var calls sync.WaitGroup
	for range 8 {
		calls.Go(func() {
			ctx, timings := withRequestTimings(t.Context())
			if _, err := provider.Retrieve(ctx); err != nil {
				t.Error("a resolução do cliente compartilhado falhou", err)
			}
			usage := timings.record(nil)
			if usage["credential_resolutions"] != 1 {
				t.Error("as chamadas simultâneas compartilharam metadados", usage)
			}
			if seconds, ok := usage["credential_resolution_seconds"].(float64); !ok || seconds < 0 {
				t.Error("a resolução de credenciais não foi temporizada", usage)
			}
			data, err := json.Marshal(usage)
			if err != nil || strings.Contains(string(data), "fake-id") ||
				strings.Contains(string(data), "fake-secret") || strings.Contains(string(data), "fake-token") {
				t.Error("a instrumentação incluiu uma credencial nos metadados")
			}
		})
	}
	calls.Wait()
	if retrievals.Load() != 1 {
		t.Fatalf("a instrumentação eliminou o cache do SDK: resoluções=%d", retrievals.Load())
	}
	static := credentials.NewStaticCredentialsProvider("fake-id", "fake-secret", "fake-token")
	staticCache := aws.NewCredentialsCache(static)
	timedStatic := timedCredentialsProvider{provider: staticCache}
	if !slices.Equal(timedStatic.ProviderSources(), staticCache.ProviderSources()) ||
		!aws.IsCredentialsProvider(timedStatic, static) {
		t.Fatal("a instrumentação perdeu o contrato e a cadeia do provider selecionado")
	}
}

func TestCredentialTimingPreservesCancellation(t *testing.T) {
	t.Parallel()
	provider := timedCredentialsProvider{provider: aws.CredentialsProviderFunc(func(ctx context.Context) (aws.Credentials, error) {
		return aws.Credentials{}, ctx.Err()
	})}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	ctx, timings := withRequestTimings(ctx)
	if _, err := provider.Retrieve(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("a instrumentação removeu o cancelamento", err)
	}
	if timings.record(nil)["credential_resolutions"] != 1 {
		t.Fatal("a resolução cancelada não foi registrada")
	}
}

func TestFirstTextTimingIgnoresEmptyTextAndDoesNotEnableStreaming(t *testing.T) {
	t.Parallel()
	timer := &firstTextTimer{started: time.Now().Add(-time.Millisecond)}
	if timer.wrap(nil) != nil {
		t.Fatal("medir texto ativou um callback de Stream em uma transcrição completa")
	}
	timer.observe(" \n ", "partial")
	if len(timer.record(nil)) != 0 {
		t.Fatal("um texto vazio foi registrado como chegada do primeiro texto")
	}
	timer.observe("Texto final.", "final")
	var calls sync.WaitGroup
	for range 8 {
		calls.Go(func() {
			timer.observe("Revisão posterior.", "partial")
			if timer.record(nil)["first_text_stage"] != "final" {
				t.Error("uma observação posterior substituiu a primeira")
			}
		})
	}
	calls.Wait()
}
