package whisper

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ai-talker/internal/audio"
	"ai-talker/internal/config"
)

func testInstallation(t *testing.T) (string, config.Settings) {
	t.Helper()
	directory := t.TempDir()
	settings := config.Default()
	settings.Provider = config.ProviderWhisper
	settings.Whisper.Model = "custom"
	settings.Whisper.BinaryPath = filepath.Join(directory, "motor local", "whisper-cli.exe")
	settings.Whisper.ModelPath = filepath.Join(directory, "modelos", "português.bin")
	for path, contents := range map[string]string{
		settings.Whisper.BinaryPath: "synthetic engine", settings.Whisper.ModelPath: "synthetic model",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return directory, settings
}

func testTranscriber(t *testing.T) (*transcriber, config.Settings) {
	t.Helper()
	directory, settings := testInstallation(t)
	engine, err := New(t.Context(), directory, settings)
	if err != nil {
		t.Fatal(err)
	}
	return engine.(*transcriber), settings
}

func speechChunk(t *testing.T) audio.Chunk {
	t.Helper()
	wav, err := audio.EncodeWAV(t.Context(), []byte{0, 0, 1, 0, 2, 0})
	if err != nil {
		t.Fatal(err)
	}
	return audio.Chunk{Audio: wav, Format: audio.FormatWAV, HasSpeech: true, Duration: 1}
}

func flagValue(cmd *exec.Cmd, name string) string {
	for i, argument := range cmd.Args {
		if argument == name && i+1 < len(cmd.Args) {
			return cmd.Args[i+1]
		}
	}
	return ""
}

func TestWhisperUsesConfiguredFilesAndPreservesLanguageWithoutShell(t *testing.T) {
	engine, settings := testTranscriber(t)
	engine.options.Language, engine.options.Threads, engine.options.BeamSize = "pt", 4, 1
	engine.glossary = "AWS, nomes portugueses; $(não executar)"
	chunk := speechChunk(t)
	var temporary string
	engine.run = func(cmd *exec.Cmd) error {
		input, output := flagValue(cmd, "-f"), flagValue(cmd, "-of")
		temporary = filepath.Dir(input)
		if cmd.Path != settings.Whisper.BinaryPath || cmd.Dir != filepath.Dir(cmd.Path) ||
			flagValue(cmd, "-m") != settings.Whisper.ModelPath || flagValue(cmd, "-l") != "pt" ||
			flagValue(cmd, "-t") != "4" || flagValue(cmd, "-bs") != "1" || flagValue(cmd, "--prompt") != engine.glossary {
			t.Fatalf("o processo local não recebeu as opções selecionadas: %v", cmd.Args)
		}
		for _, argument := range cmd.Args {
			if argument == "--translate" || argument == "-tr" {
				t.Fatal("a transcrição local tentou traduzir a fala")
			}
		}
		payload, err := os.ReadFile(input)
		if err != nil || !bytes.Equal(payload, chunk.Audio) {
			t.Fatal("o processo não recebeu o WAV original preparado")
		}
		return os.WriteFile(output+".txt", []byte("\ufeff  Olá, Mendes. Hello, team!\r\n"), 0o600)
	}
	result, err := engine.Transcribe(t.Context(), chunk)
	if err != nil || result.Text != "Olá, Mendes. Hello, team!" || result.ElapsedSeconds <= 0 {
		t.Fatalf("o texto multilíngue não foi lido corretamente: %+v, %v", result, err)
	}
	if _, err := os.Stat(temporary); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a transcrição deixou WAV e texto temporários no disco")
	}
}

func TestWhisperRejectsMissingInstallationAndDoesNotRunForSilence(t *testing.T) {
	settings := config.Default()
	if _, err := New(t.Context(), t.TempDir(), settings); err == nil || !strings.Contains(err.Error(), "motor") {
		t.Fatalf("o motor ausente não indicou como instalar: %v", err)
	}
	engine, settings := testTranscriber(t)
	if err := os.Remove(settings.Whisper.ModelPath); err != nil {
		t.Fatal(err)
	}
	if _, err := New(t.Context(), t.TempDir(), settings); err == nil || !strings.Contains(err.Error(), "modelo") {
		t.Fatalf("o modelo ausente não indicou como instalar: %v", err)
	}
	engine.run = func(*exec.Cmd) error { t.Fatal("silêncio iniciou o processo Whisper"); return nil }
	if result, err := engine.Transcribe(t.Context(), audio.Chunk{}); err != nil || result.Text != "" {
		t.Fatalf("silêncio deveria produzir texto vazio sem iniciar o motor: %+v, %v", result, err)
	}
}

func TestWhisperReportsInvalidAudioAndOutputWithoutExposingDiagnostics(t *testing.T) {
	engine, _ := testTranscriber(t)
	engine.run = func(*exec.Cmd) error { t.Fatal("áudio inválido iniciou o Whisper"); return nil }
	if _, err := engine.Transcribe(t.Context(), audio.Chunk{HasSpeech: true, Audio: []byte("mp3")}); err == nil {
		t.Fatal("áudio não preparado foi aceito")
	}
	for _, tc := range []struct {
		name    string
		payload []byte
		absent  bool
		failed  bool
	}{
		{name: "arquivo ausente", absent: true},
		{name: "UTF-8 inválido", payload: []byte{255, 254}},
		{name: "texto excessivo", payload: bytes.Repeat([]byte("a"), 4*processingTextLimit+1)},
		{name: "processo falhou", failed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine.run = func(cmd *exec.Cmd) error {
				if tc.failed {
					cmd.Stderr.Write([]byte("conteúdo confidencial da transcrição"))
					return &exec.ExitError{}
				}
				if tc.absent {
					return nil
				}
				return os.WriteFile(flagValue(cmd, "-of")+".txt", tc.payload, 0o600)
			}
			if _, err := engine.Transcribe(t.Context(), speechChunk(t)); err == nil ||
				strings.Contains(err.Error(), "conteúdo confidencial") {
				t.Fatalf("a falha não foi recuperável ou revelou diagnósticos com texto: %v", err)
			}
		})
	}
}

func TestWhisperCancelStopsLocalProcessAndRemovesTemporaryAudio(t *testing.T) {
	if os.Getenv("AI_TALKER_WHISPER_TEST_HELPER") == "1" {
		if err := os.WriteFile(os.Getenv("AI_TALKER_WHISPER_TEST_MARKER"), []byte("iniciado"), 0o600); err != nil {
			os.Exit(2)
		}
		time.Sleep(time.Hour)
		os.Exit(0)
	}
	engine, _ := testTranscriber(t)
	marker := filepath.Join(t.TempDir(), "iniciado")
	temporary := make(chan string, 1)
	engine.run = func(cmd *exec.Cmd) error {
		temporary <- filepath.Dir(flagValue(cmd, "-f"))
		cmd.Path = os.Args[0]
		cmd.Args = []string{cmd.Path, "-test.run=^TestWhisperCancelStopsLocalProcessAndRemovesTemporaryAudio$"}
		cmd.Env = append(os.Environ(), "AI_TALKER_WHISPER_TEST_HELPER=1", "AI_TALKER_WHISPER_TEST_MARKER="+marker)
		return cmd.Run()
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	chunk := speechChunk(t)
	go func() {
		_, err := engine.Transcribe(ctx, chunk)
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("o processo local não iniciou")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("o cancelamento não encerrou o processo local: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("o cancelamento deixou o processo local em execução")
	}
	if _, err := os.Stat(<-temporary); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cancelar deixou o áudio temporário no disco")
	}
}

func TestWhisperFingerprintDetectsReplacementAtSamePath(t *testing.T) {
	directory, settings := testInstallation(t)
	fingerprint := Fingerprint(directory)
	before, err := fingerprint(t.Context(), settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings.Whisper.ModelPath, []byte("replacement model"), 0o600); err != nil {
		t.Fatal(err)
	}
	after, err := fingerprint(t.Context(), settings)
	if err != nil || before == after {
		t.Fatalf("substituir os pesos no mesmo caminho reutilizou a identidade anterior: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := fingerprint(ctx, settings); !errors.Is(err, context.Canceled) {
		t.Fatalf("ler pesos ignorou o cancelamento: %v", err)
	}
	if _, err := Fingerprint(t.TempDir())(t.Context(), config.Default()); err != nil {
		t.Fatalf("uma instalação ausente impediu até a análise de silêncio: %v", err)
	}
}
