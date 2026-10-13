package whisper

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"ai-talker/internal/config"
	"ai-talker/internal/transcript"
)

func TestCheckTranscribesBundledSpeechWithSelectedCPUOptions(t *testing.T) {
	directory, settings := testInstallation(t)
	settings.Whisper.Language, settings.Whisper.Threads, settings.Whisper.BeamSize = "pt", 4, 1
	settings.Glossary = "Termos pessoais que não pertencem ao teste"
	var temporary string
	result, err := check(t.Context(), directory, settings, func(ctx context.Context, selected config.Settings) (transcript.Transcriber, error) {
		engine, err := New(ctx, directory, selected)
		if err != nil {
			return nil, err
		}
		engine.(*transcriber).run = func(cmd *exec.Cmd) error {
			temporary = filepath.Dir(flagValue(cmd, "-f"))
			wav, err := os.ReadFile(flagValue(cmd, "-f"))
			if err != nil || string(wav[:4]) != "RIFF" ||
				binary.LittleEndian.Uint32(wav[24:28]) != 16000 ||
				binary.LittleEndian.Uint16(wav[22:24]) != 1 ||
				binary.LittleEndian.Uint16(wav[34:36]) != 16 {
				t.Fatal("o diagnóstico não forneceu uma amostra WAV mono 16 kHz de fala")
			}
			if flagValue(cmd, "-m") != settings.Whisper.ModelPath || flagValue(cmd, "-l") != "en" ||
				flagValue(cmd, "-t") != "4" || flagValue(cmd, "-bs") != "1" || flagValue(cmd, "--prompt") != "" {
				t.Fatalf("o diagnóstico perdeu o modelo/CPU ou usou o idioma/glossário pessoal: %v", cmd.Args)
			}
			return os.WriteFile(flagValue(cmd, "-of")+".txt", []byte(" HELLO! This is a short recording.\n"), 0o600)
		}
		return engine, nil
	})
	if err != nil || result.Text != "HELLO! This is a short recording." || result.ElapsedSeconds <= 0 ||
		settings.Whisper.Language != "pt" || settings.Glossary == "" {
		t.Fatalf("o teste não aprovou a inferência correta ou alterou as preferências: %+v, %v", result, err)
	}
	if _, err := os.Stat(temporary); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("o teste deixou áudio e texto temporários")
	}
}

func TestCheckReportsMissingEngineModelAndOfficialDLLsBeforeInference(t *testing.T) {
	directory := t.TempDir()
	settings := config.Default()
	factory := func(context.Context, config.Settings) (transcript.Transcriber, error) {
		t.Fatal("o diagnóstico iniciou inferência sem os arquivos necessários")
		return nil, nil
	}
	if _, err := check(t.Context(), directory, settings, factory); err == nil ||
		!strings.Contains(err.Error(), "motor Whisper") || !strings.Contains(err.Error(), "modelo Whisper") ||
		!strings.Contains(err.Error(), "whisper.dll") {
		t.Fatalf("o diagnóstico não explicou os arquivos ausentes: %v", err)
	}
	binary, model := Paths(directory, settings.Whisper)
	for _, path := range []string{binary, model} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("synthetic"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := check(t.Context(), directory, settings, factory); err == nil ||
		!strings.Contains(err.Error(), "DLLs") || strings.Contains(err.Error(), "modelo Whisper ausente") {
		t.Fatalf("a instalação parcial não identificou suas DLLs: %v", err)
	}
}

func TestCheckRejectsEmptyWrongTextAndLocalProcessFailure(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		fail       bool
	}{
		{name: "vazio"}, {name: "texto sem a amostra", text: "This is an unrelated response."},
		{name: "fala incompleta", text: "Hello."}, {name: "erro do motor", fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			directory, settings := testInstallation(t)
			_, err := check(t.Context(), directory, settings, func(ctx context.Context, selected config.Settings) (transcript.Transcriber, error) {
				engine, err := New(ctx, directory, selected)
				if err != nil {
					return nil, err
				}
				engine.(*transcriber).run = func(cmd *exec.Cmd) error {
					if tc.fail {
						return errors.New("motor indisponível")
					}
					return os.WriteFile(flagValue(cmd, "-of")+".txt", []byte(tc.text), 0o600)
				}
				return engine, nil
			})
			if err == nil || (tc.fail && !strings.Contains(err.Error(), "motor indisponível")) ||
				(!tc.fail && !strings.Contains(err.Error(), "não reconheceu")) {
				t.Fatalf("o teste aprovou uma saída incorreta ou ocultou o erro: %v", err)
			}
		})
	}
}

func TestCheckRejectsCancellationBeforeOpeningLocalEngine(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Check(ctx, t.TempDir(), config.Default()); !errors.Is(err, context.Canceled) {
		t.Fatalf("o diagnóstico não respeitou o cancelamento: %v", err)
	}
}
