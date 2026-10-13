package cloud

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"ai-talker/internal/config"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/transcribe"
	jobtypes "github.com/aws/aws-sdk-go-v2/service/transcribe/types"
	"github.com/aws/smithy-go"
)

type fakeTranscriptionJobs struct {
	start  func(context.Context, *transcribe.StartTranscriptionJobInput) error
	get    func(context.Context, *transcribe.GetTranscriptionJobInput) (*transcribe.GetTranscriptionJobOutput, error)
	delete func(context.Context, *transcribe.DeleteTranscriptionJobInput) error
}

func (c fakeTranscriptionJobs) StartTranscriptionJob(ctx context.Context, input *transcribe.StartTranscriptionJobInput,
	_ ...func(*transcribe.Options),
) (*transcribe.StartTranscriptionJobOutput, error) {
	return &transcribe.StartTranscriptionJobOutput{}, c.start(ctx, input)
}
func (c fakeTranscriptionJobs) GetTranscriptionJob(ctx context.Context, input *transcribe.GetTranscriptionJobInput,
	_ ...func(*transcribe.Options),
) (*transcribe.GetTranscriptionJobOutput, error) {
	return c.get(ctx, input)
}
func (c fakeTranscriptionJobs) DeleteTranscriptionJob(ctx context.Context, input *transcribe.DeleteTranscriptionJobInput,
	_ ...func(*transcribe.Options),
) (*transcribe.DeleteTranscriptionJobOutput, error) {
	return &transcribe.DeleteTranscriptionJobOutput{}, c.delete(ctx, input)
}

type fakeTranscriptionObjects struct {
	put    func(context.Context, *s3.PutObjectInput) error
	get    func(context.Context, *s3.GetObjectInput) (*s3.GetObjectOutput, error)
	delete func(context.Context, *s3.DeleteObjectInput) error
}

func (c fakeTranscriptionObjects) PutObject(ctx context.Context, input *s3.PutObjectInput,
	_ ...func(*s3.Options),
) (*s3.PutObjectOutput, error) {
	return &s3.PutObjectOutput{}, c.put(ctx, input)
}
func (c fakeTranscriptionObjects) GetObject(ctx context.Context, input *s3.GetObjectInput,
	_ ...func(*s3.Options),
) (*s3.GetObjectOutput, error) {
	return c.get(ctx, input)
}
func (c fakeTranscriptionObjects) DeleteObject(ctx context.Context, input *s3.DeleteObjectInput,
	_ ...func(*s3.Options),
) (*s3.DeleteObjectOutput, error) {
	return &s3.DeleteObjectOutput{}, c.delete(ctx, input)
}

type batchTestCalls struct {
	jobInput            *transcribe.StartTranscriptionJobInput
	deletedJobs         []string
	deletedKeys         []string
	inputKey, outputKey string
	uploaded            []byte
}

func batchTestEngine(t *testing.T) (*transcribeBatch, *batchTestCalls) {
	t.Helper()
	settings := config.Default()
	settings.Cloud.ModelID, settings.Cloud.S3Bucket = config.TranscribeBatch, "test-transcripts"
	calls := new(batchTestCalls)
	objects := fakeTranscriptionObjects{
		put: func(_ context.Context, input *s3.PutObjectInput) error {
			if aws.ToString(input.Bucket) != settings.Cloud.S3Bucket || aws.ToString(input.ContentType) != "audio/wav" {
				t.Error("o upload perdeu o bucket ou o formato")
			}
			calls.inputKey = aws.ToString(input.Key)
			var err error
			calls.uploaded, err = io.ReadAll(input.Body)
			return err
		},
		get: func(_ context.Context, input *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
			calls.outputKey = aws.ToString(input.Key)
			return &s3.GetObjectOutput{Body: io.NopCloser(strings.NewReader(
				`{"results":{"transcripts":[{"transcript":"Texto reconhecido."}]}}`))}, nil
		},
		delete: func(ctx context.Context, input *s3.DeleteObjectInput) error {
			if ctx.Err() != nil || aws.ToString(input.Bucket) != settings.Cloud.S3Bucket {
				t.Error("a limpeza usou contexto cancelado ou outro bucket")
			}
			calls.deletedKeys = append(calls.deletedKeys, aws.ToString(input.Key))
			return nil
		},
	}
	jobs := fakeTranscriptionJobs{
		start: func(_ context.Context, input *transcribe.StartTranscriptionJobInput) error {
			calls.jobInput = input
			return nil
		},
		get: func(_ context.Context, input *transcribe.GetTranscriptionJobInput) (*transcribe.GetTranscriptionJobOutput, error) {
			return batchStatus(input, jobtypes.TranscriptionJobStatusCompleted), nil
		},
		delete: func(ctx context.Context, input *transcribe.DeleteTranscriptionJobInput) error {
			if ctx.Err() != nil {
				t.Error("a limpeza do job usou contexto cancelado")
			}
			calls.deletedJobs = append(calls.deletedJobs, aws.ToString(input.TranscriptionJobName))
			return nil
		},
	}
	return &transcribeBatch{settings: settings, jobs: jobs, objects: objects,
		wait: func(ctx context.Context, _ time.Duration) error { return ctx.Err() }}, calls
}

func batchStatus(input *transcribe.GetTranscriptionJobInput, status jobtypes.TranscriptionJobStatus) *transcribe.GetTranscriptionJobOutput {
	return &transcribe.GetTranscriptionJobOutput{TranscriptionJob: &jobtypes.TranscriptionJob{
		TranscriptionJobName: input.TranscriptionJobName, TranscriptionJobStatus: status,
	}}
}

func TestTranscribeBatchUploadsBoundedWAVReadsFinalTextAndRemovesOnlyItsObjects(t *testing.T) {
	t.Parallel()
	engine, calls := batchTestEngine(t)
	chunk := pcmSpeech(t, 1)
	result, err := engine.Transcribe(t.Context(), chunk)
	input := calls.jobInput
	if err != nil || result.Text != "Texto reconhecido." || input == nil ||
		input.LanguageCode != "pt-BR" || aws.ToBool(input.IdentifyMultipleLanguages) ||
		input.MediaFormat != jobtypes.MediaFormatWav || aws.ToInt32(input.MediaSampleRateHertz) != 16000 ||
		!bytes.Equal(calls.uploaded, chunk.Audio) || result.Usage["jobs"] != 1 {
		t.Fatal("o fluxo de arquivos perdeu o áudio, idioma ou resultado final", result, input, err)
	}
	name := aws.ToString(input.TranscriptionJobName)
	prefix := "ai-talker/transcribe/" + name + "/"
	if !strings.HasPrefix(name, "aitalker-") || len(name) != len("aitalker-")+32 ||
		calls.inputKey != prefix+"audio.wav" || calls.outputKey != prefix+"transcript.json" ||
		aws.ToString(input.Media.MediaFileUri) != "s3://test-transcripts/"+calls.inputKey ||
		aws.ToString(input.OutputKey) != calls.outputKey || aws.ToString(input.OutputBucketName) != "test-transcripts" ||
		!slices.Equal(calls.deletedJobs, []string{name}) ||
		!slices.Equal(calls.deletedKeys, []string{calls.inputKey, calls.outputKey}) {
		t.Fatal("a limpeza perdeu seu namespace exclusivo", calls)
	}
}

func TestTranscribeBatchPreservesTimestampedSpeakerSegments(t *testing.T) {
	t.Parallel()
	engine, calls := batchTestEngine(t)
	engine.settings.SpeakerLabels = true
	objects := engine.objects.(fakeTranscriptionObjects)
	objects.get = func(context.Context, *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
		payload := `{"results":{"transcripts":[{"transcript":"Olá, mundo."}],"items":[` +
			`{"start_time":"0.1","end_time":"0.3","type":"pronunciation","alternatives":[{"content":"Olá"}]},` +
			`{"type":"punctuation","alternatives":[{"content":","}]},` +
			`{"start_time":"0.4","end_time":"0.7","type":"pronunciation","alternatives":[{"content":"mundo"}]},` +
			`{"type":"punctuation","alternatives":[{"content":"."}]}],` +
			`"speaker_labels":{"segments":[{"start_time":"0.0","end_time":"0.8","speaker_label":"spk_0"}]}}}`
		return &s3.GetObjectOutput{Body: io.NopCloser(strings.NewReader(payload))}, nil
	}
	engine.objects = objects
	result, err := engine.Transcribe(t.Context(), pcmSpeech(t, 1))
	if err != nil || result.Text != "Olá, mundo." || len(result.Segments) != 2 ||
		result.Segments[0].Speaker != "spk_0" || result.Segments[0].Text != "Olá," ||
		result.Segments[1].Speaker != "spk_0" || result.Segments[1].Text != "mundo." ||
		calls.jobInput.Settings == nil || !aws.ToBool(calls.jobInput.Settings.ShowSpeakerLabels) {
		t.Fatalf("Amazon Transcribe perdeu timestamps, locutor ou pontuação: %+v %+v %v", result, calls.jobInput, err)
	}
}

func TestTranscribeMissingBucketIsActionableAndDoesNotLoadAWSConfiguration(t *testing.T) {
	settings := config.Default()
	settings.Cloud.ModelID = config.TranscribeBatch
	settings.Profile = "perfil-inexistente-na-fixture"
	_, err := New(t.Context(), settings)
	if !errors.Is(err, config.ErrTranscribeBucketRequired) ||
		!strings.Contains(err.Error(), "streaming") {
		t.Fatal("o requisito de S3 foi escondido por configuração AWS ou não oferece recuperação", err)
	}
}

func TestTranscribeBatchAutoLanguageAndCleanupFailureKeepTheTranscript(t *testing.T) {
	t.Parallel()
	engine, calls := batchTestEngine(t)
	engine.settings.Cloud.Language = "auto"
	objects := engine.objects.(fakeTranscriptionObjects)
	objects.delete = func(context.Context, *s3.DeleteObjectInput) error { return errors.New("S3 indisponível") }
	engine.objects = objects
	result, err := engine.Transcribe(t.Context(), pcmSpeech(t, 1))
	if err != nil || result.Text != "Texto reconhecido." || result.ReviewReason == "" ||
		result.Usage["cleanup_pending"] != true || !aws.ToBool(calls.jobInput.IdentifyMultipleLanguages) ||
		calls.jobInput.LanguageCode != "" || len(calls.jobInput.LanguageOptions) != 5 {
		t.Fatal("a falha de limpeza apagou uma transcrição concluída ou perdeu o idioma", result, err)
	}
}

func TestTranscribeBatchFailuresReleaseOwnedArtifactsAndReturnNoFinalText(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"upload", "start", "poll", "failed", "get", "malformed"} {
		t.Run(stage, func(t *testing.T) {
			engine, calls := batchTestEngine(t)
			failure := errors.New("AWS indisponível")
			objects := engine.objects.(fakeTranscriptionObjects)
			jobs := engine.jobs.(fakeTranscriptionJobs)
			switch stage {
			case "upload":
				objects.put = func(_ context.Context, input *s3.PutObjectInput) error {
					calls.inputKey = aws.ToString(input.Key)
					return failure
				}
			case "start":
				jobs.start = func(context.Context, *transcribe.StartTranscriptionJobInput) error { return failure }
				jobs.get = func(context.Context, *transcribe.GetTranscriptionJobInput) (*transcribe.GetTranscriptionJobOutput, error) {
					return nil, &smithy.GenericAPIError{Code: "BadRequestException", Message: "The job does not exist"}
				}
			case "poll":
				first := true
				jobs.get = func(_ context.Context, input *transcribe.GetTranscriptionJobInput) (*transcribe.GetTranscriptionJobOutput, error) {
					if first {
						first = false
						return nil, failure
					}
					return batchStatus(input, jobtypes.TranscriptionJobStatusCompleted), nil
				}
			case "failed":
				jobs.get = func(_ context.Context, input *transcribe.GetTranscriptionJobInput) (*transcribe.GetTranscriptionJobOutput, error) {
					return batchStatus(input, jobtypes.TranscriptionJobStatusFailed), nil
				}
			case "get":
				objects.get = func(context.Context, *s3.GetObjectInput) (*s3.GetObjectOutput, error) { return nil, failure }
			case "malformed":
				objects.get = func(context.Context, *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
					return &s3.GetObjectOutput{Body: io.NopCloser(strings.NewReader(`{"results":`))}, nil
				}
			}
			engine.objects, engine.jobs = objects, jobs
			result, err := engine.Transcribe(t.Context(), pcmSpeech(t, 1))
			if err == nil || result.Text != "" || len(calls.deletedKeys) != 2 {
				t.Fatal("a falha virou resultado completo ou deixou os objetos sem limpeza", stage, result, calls, err)
			}
			for _, key := range calls.deletedKeys {
				if !strings.HasPrefix(key, "ai-talker/transcribe/aitalker-") ||
					!strings.HasSuffix(key, "/audio.wav") && !strings.HasSuffix(key, "/transcript.json") {
					t.Fatal("a falha removeu um objeto fora do namespace do app", key)
				}
			}
		})
	}
}

func TestTranscribeBatchCancellationWaitsForTerminalJobBeforeCleanup(t *testing.T) {
	t.Parallel()
	engine, calls := batchTestEngine(t)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	jobs := engine.jobs.(fakeTranscriptionJobs)
	polls := 0
	jobs.get = func(jobCtx context.Context, input *transcribe.GetTranscriptionJobInput) (*transcribe.GetTranscriptionJobOutput, error) {
		polls++
		if polls == 1 {
			return batchStatus(input, jobtypes.TranscriptionJobStatusInProgress), nil
		}
		if jobCtx.Err() != nil {
			t.Error("a limpeza herdou o cancelamento do usuário")
		}
		return batchStatus(input, jobtypes.TranscriptionJobStatusCompleted), nil
	}
	engine.jobs = jobs
	engine.wait = func(waitCtx context.Context, _ time.Duration) error {
		cancel()
		return waitCtx.Err()
	}
	result, err := engine.Transcribe(ctx, pcmSpeech(t, 1))
	if !errors.Is(err, context.Canceled) || result.Text != "" || polls != 2 ||
		len(calls.deletedJobs) != 1 || len(calls.deletedKeys) != 2 {
		t.Fatal("cancelar removeu a entrada de um job ativo ou perdeu sua limpeza", calls, polls, err)
	}
}

func TestTranscribeBatchKeepsNamespaceWhenCanceledJobStillRuns(t *testing.T) {
	t.Parallel()
	engine, calls := batchTestEngine(t)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	jobs := engine.jobs.(fakeTranscriptionJobs)
	jobs.get = func(_ context.Context, input *transcribe.GetTranscriptionJobInput) (*transcribe.GetTranscriptionJobOutput, error) {
		return batchStatus(input, jobtypes.TranscriptionJobStatusInProgress), nil
	}
	engine.jobs = jobs
	engine.wait = func(waitCtx context.Context, _ time.Duration) error {
		if waitCtx == ctx || waitCtx.Err() != nil {
			return waitCtx.Err()
		}
		cancel()
		if pollsCtx := waitCtx.Err(); pollsCtx != nil {
			return pollsCtx
		}
		return context.DeadlineExceeded
	}
	_, err := engine.Transcribe(ctx, pcmSpeech(t, 1))
	if err == nil || len(calls.deletedJobs) != 0 || len(calls.deletedKeys) != 0 ||
		!strings.Contains(err.Error(), "limpeza temporária") || !strings.Contains(err.Error(), "pendente") {
		t.Fatal("a entrada de um job ativo foi removida ou a limpeza pendente ficou oculta", calls, err)
	}
}

func TestTranscribeBatchPendingCleanupHidesRawAWSDiagnostics(t *testing.T) {
	t.Parallel()
	engine, calls := batchTestEngine(t)
	failure := errors.New("credential_process diagnostic: SYNTHETIC-SECRET")
	jobs := engine.jobs.(fakeTranscriptionJobs)
	jobs.get = func(_ context.Context, input *transcribe.GetTranscriptionJobInput) (*transcribe.GetTranscriptionJobOutput, error) {
		return batchStatus(input, jobtypes.TranscriptionJobStatusInProgress), nil
	}
	engine.jobs = jobs
	engine.wait = func(context.Context, time.Duration) error { return failure }
	_, err := engine.Transcribe(t.Context(), pcmSpeech(t, 1))
	if !errors.Is(err, failure) || strings.Contains(err.Error(), "SYNTHETIC-SECRET") ||
		len(calls.deletedKeys) != 0 || len(calls.deletedJobs) != 0 {
		t.Fatal("a limpeza perdeu a causa, expôs o diagnóstico ou removeu arquivos do job ativo", err)
	}
}

func TestCloudFactoryCreatesEachEngineWithoutResolvingCredentials(t *testing.T) {
	command, record := prepareCredentialHelper(t)
	isolateAWS(t, "[profile engines-test]\ncredential_process = "+command+"\n")
	for _, model := range config.CloudModels() {
		settings := config.Default()
		settings.Profile = "engines-test"
		settings.Cloud.ModelID = model.ID
		settings.Cloud.S3Bucket = "synthetic-bucket"
		engine, err := New(t.Context(), settings)
		if err != nil {
			t.Fatal(model.ID, err)
		}
		if err := engine.Close(); err != nil {
			t.Fatal(model.ID, err)
		}
		switch model.Backend {
		case config.CloudConverse:
			if _, ok := engine.(*transcriber); !ok {
				t.Fatal("Voxtral não foi roteado para Converse")
			}
		case config.CloudTranscribe:
			if _, ok := engine.(*transcribeBatch); !ok {
				t.Fatal("Transcribe arquivo não foi roteado para S3/job")
			}
		case config.CloudStreaming:
			if _, ok := engine.(*transcribeStreaming); !ok {
				t.Fatal("Transcribe streaming não foi roteado para conexão direta")
			}
		case config.CloudSonic:
			if _, ok := engine.(*sonicTranscriber); !ok {
				t.Fatal("Nova Sonic não foi roteado para conexão bidirecional")
			}
		}
	}
	if calls := helperRecords(t, record); len(calls) != 0 {
		t.Fatal("construir os motores solicitou credenciais ou iniciou uma chamada", calls)
	}
}

func TestCloudFactoryRejectsNovaSonicOutsideDocumentedRegionsBeforeLoadingCredentials(t *testing.T) {
	command, record := prepareCredentialHelper(t)
	isolateAWS(t, "[profile engines-test]\ncredential_process = "+command+"\n")
	settings := config.Default()
	settings.Profile = "engines-test"
	settings.Region = "us-east-2"
	settings.Cloud.ModelID = config.NovaSonic25
	if _, err := New(t.Context(), settings); err == nil || !strings.Contains(err.Error(), "us-east-1") {
		t.Fatalf("Nova 2.5 Sonic não explicou sua região indisponível: %v", err)
	}
	if calls := helperRecords(t, record); len(calls) != 0 {
		t.Fatalf("a rejeição regional leu credenciais AWS: %v", calls)
	}
}
