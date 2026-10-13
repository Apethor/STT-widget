package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ai-talker/internal/config"
	"ai-talker/internal/transcript"
)

type stubRunner struct {
	result   transcript.Result
	err      error
	calls    int
	settings config.Settings
	request  transcript.Request
	source   string
}

func (r *stubRunner) Transcribe(
	_ context.Context,
	source string,
	settings config.Settings,
	request transcript.Request,
) (transcript.Result, error) {
	r.calls++
	r.settings, r.request, r.source = settings, request, source
	request.OnProgress(transcript.Progress{Message: "Preparando o áudio…"})
	return r.result, r.err
}

func TestStdoutContainsOnlyCurrentTextAndAcceptsOptionsAfterPath(t *testing.T) {
	var stdout, stderr bytes.Buffer
	runner := &stubRunner{result: transcript.Result{Text: "Texto em português com TypeScript."}}
	app := App{DataDir: t.TempDir(), Runner: runner, Stdout: &stdout, Stderr: &stderr}
	code := app.Run(t.Context(), []string{"transcribe", "caminho com espaços.wav",
		"--stdout", "--profile", "isolated", "--quality", "fast", "--chunk-seconds", "60"})
	if code != 0 || stdout.String() != runner.result.Text+"\n" {
		t.Fatalf("stdout is not pipeable: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if runner.source != "caminho com espaços.wav" || runner.settings.Profile != "isolated" ||
		runner.settings.Quality != "fast" || runner.settings.ChunkSeconds != 60 {
		t.Fatalf("CLI path or flags changed: %+v", runner)
	}
	if !runner.request.PersistLatest || !strings.Contains(stderr.String(), "Preparando") {
		t.Fatal("CLI did not isolate diagnostics from text or request latest persistence")
	}
}

func TestCloudModelFlagsOverrideSavedQualityAndDoctorReportsItsAPI(t *testing.T) {
	directory := t.TempDir()
	settings := config.Default()
	settings.Cloud.ModelID = config.NovaSonic
	settings.Cloud.StreamModelID = config.VoxtralMini
	if err := config.Save(directory, settings); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"--quality", "fast"},
		{"--quality", "fast", "--model", config.TranscribeStreaming,
			"--stream-model", config.NovaSonic, "--transcribe-language", "en-US"},
		{"--model", config.TranscribeBatch, "--transcribe-bucket", "transcripts-test", "--transcribe-language", "auto"},
	} {
		var stdout, stderr bytes.Buffer
		runner := &stubRunner{result: transcript.Result{Text: "Fala."}}
		app := App{DataDir: directory, Runner: runner, Stdout: &stdout, Stderr: &stderr}
		code := app.Run(t.Context(), append([]string{"transcribe", "audio.wav", "--stdout"}, args...))
		if code != 0 {
			t.Fatal("os parâmetros Cloud não foram aceitos", stderr.String())
		}
		model := config.VoxtralMini
		if len(args) > 2 {
			model = config.TranscribeStreaming
			if args[1] == config.TranscribeBatch {
				model = config.TranscribeBatch
			}
		}
		if runner.settings.ModelID() != model {
			t.Fatal("o modelo explícito/quality não substituiu a preferência antiga", runner.settings)
		}
		stdout.Reset()
		app.Verify = func(context.Context, config.Settings) error { return nil }
		if code := app.Run(t.Context(), append([]string{"doctor"}, args...)); code != 0 ||
			!strings.Contains(stdout.String(), "API: "+cloudAPI(runner.settings)) {
			t.Fatal("doctor não informou a API do motor selecionado", code, stdout.String())
		}
	}
}

func TestModelsListsCatalogueWithoutLoadingAWSOrPreferences(t *testing.T) {
	var output bytes.Buffer
	app := App{DataDir: filepath.Join(t.TempDir(), "absent"), Stdout: &output}
	if code := app.Run(t.Context(), []string{"models"}); code != 0 {
		t.Fatal(code)
	}
	for _, model := range config.CloudModels() {
		if !strings.Contains(output.String(), model.ID) || !strings.Contains(output.String(), model.Name) {
			t.Fatal("o CLI omitiu uma opção Cloud", model.ID)
		}
	}
}
func TestSilenceDoesNotCopyOrCreateMarkdown(t *testing.T) {
	var stdout, stderr bytes.Buffer
	copied := false
	directory := t.TempDir()
	destination := filepath.Join(directory, "silence.md")
	app := App{
		DataDir: directory, Runner: &stubRunner{}, Stdout: &stdout, Stderr: &stderr,
		Copy: func(string) error { copied = true; return nil },
	}
	code := app.Run(t.Context(), []string{"transcribe", "silence.wav", "--copy", "--markdown", destination})
	if code != 0 || copied || !strings.Contains(stdout.String(), "Nenhuma fala detectada") {
		t.Fatalf("silence produced a copy or failure: code=%d copied=%v %s", code, copied, stdout.String())
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("silent audio created a misleading Markdown: %v", err)
	}
}

func TestExactMarkdownAndJSONResult(t *testing.T) {
	var stdout, stderr bytes.Buffer
	directory := t.TempDir()
	source := filepath.Join(directory, "automatic.md")
	destination := filepath.Join(directory, "requested", "transcricao.md")
	content := "# Transcrição\n\nFala integral em português.\n"
	if err := os.WriteFile(source, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &stubRunner{result: transcript.Result{
		Text: "Fala integral em português.", Source: "spoken.wav",
		Parts:   []transcript.Part{{Index: 0, Duration: 2, DetectedSpeech: true}},
		Exports: map[string]string{"md": source, "txt": "automatic.txt"},
	}}
	app := App{DataDir: directory, Runner: runner, Stdout: &stdout, Stderr: &stderr}
	code := app.Run(t.Context(), []string{"transcribe", "spoken.wav", "--markdown", destination, "--json"})
	if code != 0 {
		t.Fatalf("Markdown delivery failed: code=%d %s", code, stderr.String())
	}
	var result struct {
		transcript.Result
		Markdown string `json:"markdown"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("JSON is mixed with diagnostics: %s: %v", stdout.String(), err)
	}
	if result.Markdown != destination || len(result.Parts) != 1 || result.Text != runner.result.Text {
		t.Fatalf("machine result did not identify exact file and coverage: %+v", result)
	}
	saved, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(saved) != content {
		t.Fatal("exact Markdown delivery changed the transcript")
	}
}

func TestExistingMarkdownIsPreservedBeforeInference(t *testing.T) {
	directory := t.TempDir()
	destination := filepath.Join(directory, "already.md")
	if err := os.WriteFile(destination, []byte("existing document"), 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	runner := &stubRunner{}
	app := App{DataDir: directory, Runner: runner, Stdout: &output, Stderr: &output}
	code := app.Run(t.Context(), []string{"transcribe", "spoken.wav", "--markdown", destination})
	if code != 1 || runner.calls != 0 {
		t.Fatalf("existing destination reached inference: code=%d calls=%d", code, runner.calls)
	}
	saved, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(saved) != "existing document" {
		t.Fatal("existing Markdown was overwritten")
	}
}

func TestIncompleteRecognitionAndPartialFailureAreIdentified(t *testing.T) {
	tests := []struct {
		name   string
		result transcript.Result
		err    error
		code   int
		field  string
	}{
		{
			name: "empty detected speech", code: 3, field: `"needs_review":true`,
			result: transcript.Result{NeedsReview: true},
		},
		{
			name: "partial AWS failure", code: 1, field: `"partial":true`,
			err: &transcript.Error{Cause: errors.New("simulated AWS error"), PartialText: "Primeira parte."},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			app := App{
				DataDir: t.TempDir(), Runner: &stubRunner{result: test.result, err: test.err},
				Stdout: &stdout, Stderr: &stderr,
			}
			code := app.Run(t.Context(), []string{"transcribe", "input.wav", "--json"})
			if code != test.code || !strings.Contains(stdout.String(), test.field) {
				t.Fatalf("incomplete result hidden: code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
			}
			if !json.Valid(stdout.Bytes()) {
				t.Fatalf("partial output is not valid JSON: %s", stdout.String())
			}
		})
	}
}

func TestInvalidFlagsAndCancellation(t *testing.T) {
	arguments := [][]string{
		{"transcribe", "input.wav", "--chunk-seconds", "0"},
		{"transcribe", "input.wav", "--overlap-seconds", "-1"},
		{"transcribe", "input.wav", "--workers", "9"},
		{"transcribe", "input.wav", "--stdout", "--json"},
		{"transcribe", "input.wav", "--unknown"},
	}
	for index, args := range arguments {
		t.Run(args[len(args)-1], func(t *testing.T) {
			var output bytes.Buffer
			runner := &stubRunner{}
			app := App{DataDir: t.TempDir(), Runner: runner, Stdout: &output, Stderr: &output}
			if code := app.Run(t.Context(), args); code != 2 || runner.calls != 0 {
				t.Fatalf("invalid case %d reached inference: code=%d calls=%d %s", index, code, runner.calls, output.String())
			}
		})
	}
	var output bytes.Buffer
	app := App{
		DataDir: t.TempDir(), Runner: &stubRunner{err: context.Canceled},
		Stdout: &output, Stderr: &output,
	}
	if code := app.Run(t.Context(), []string{"transcribe", "input.wav"}); code != 130 {
		t.Fatalf("cancellation exit code = %d", code)
	}
}
