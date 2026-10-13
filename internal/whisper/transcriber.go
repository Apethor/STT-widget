package whisper

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"ai-talker/internal/audio"
	"ai-talker/internal/config"
	"ai-talker/internal/platform"
	"ai-talker/internal/transcript"
)

type transcriber struct {
	binary   string
	model    string
	options  config.Whisper
	glossary string
	run      func(*exec.Cmd) error
}

func Paths(directory string, options config.Whisper) (binary, model string) {
	binary, model = options.BinaryPath, options.ModelPath
	if strings.TrimSpace(binary) == "" {
		binary = BinaryPath(directory)
		// Preserve an earlier managed installation until the user downloads
		// the new engine; updating the app must not make local capture unusable.
		if _, err := os.Stat(binary); errors.Is(err, os.ErrNotExist) {
			previous := filepath.Join(directory, "whisper", "engine-1.9.2")
			if engineInstalled(previous) {
				binary = filepath.Join(previous, "whisper-cli.exe")
			}
		}
	}
	if strings.TrimSpace(model) == "" {
		model = ModelPath(directory, options.Model)
	}
	return binary, model
}

func Factory(directory string) transcript.Factory {
	return func(ctx context.Context, settings config.Settings) (transcript.Transcriber, error) {
		return New(ctx, directory, settings)
	}
}

func New(ctx context.Context, directory string, settings config.Settings) (transcript.Transcriber, error) {
	if err := settings.Whisper.Validate(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	binary, model := Paths(directory, settings.Whisper)
	for _, file := range []struct {
		path    string
		message string
	}{
		{binary, "O motor Whisper não foi encontrado. Em Configurações → Whisper, baixe o motor ou selecione whisper-cli.exe."},
		{model, "O modelo Whisper não foi encontrado. Em Configurações → Whisper, baixe um modelo ou selecione um arquivo GGML."},
	} {
		info, err := os.Stat(file.path)
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			return nil, errors.New(file.message)
		}
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		return nil, fmt.Errorf("localizar o motor Whisper: %w", err)
	}
	model, err = filepath.Abs(model)
	if err != nil {
		return nil, fmt.Errorf("localizar o modelo Whisper: %w", err)
	}
	return &transcriber{
		binary: binary, model: model, options: settings.Whisper, glossary: settings.Glossary,
		run: func(cmd *exec.Cmd) error { return cmd.Run() },
	}, nil
}

func (t *transcriber) Transcribe(ctx context.Context, chunk audio.Chunk) (transcript.ChunkResult, error) {
	if err := ctx.Err(); err != nil {
		return transcript.ChunkResult{}, err
	}
	if !chunk.HasSpeech {
		return transcript.ChunkResult{Usage: map[string]any{}}, nil
	}
	if chunk.Format != audio.FormatWAV || len(chunk.Audio) < 44 ||
		string(chunk.Audio[:4]) != "RIFF" || string(chunk.Audio[8:12]) != "WAVE" {
		return transcript.ChunkResult{}, errors.New("o Whisper precisa de áudio WAV mono preparado pelo aplicativo")
	}
	started := time.Now()
	directory, err := os.MkdirTemp("", "ai-talker-whisper-")
	if err != nil {
		return transcript.ChunkResult{}, fmt.Errorf("preparar a transcrição local: %w", err)
	}
	defer os.RemoveAll(directory)
	input, output := filepath.Join(directory, "audio.wav"), filepath.Join(directory, "resultado")
	if err := os.WriteFile(input, chunk.Audio, 0o600); err != nil {
		return transcript.ChunkResult{}, fmt.Errorf("preparar o áudio local: %w", err)
	}
	threads := t.options.Threads
	if threads == 0 {
		threads = min(8, runtime.NumCPU())
	}
	arguments := []string{
		"-m", t.model, "-f", input, "-l", t.options.Language,
		"-t", strconv.Itoa(threads), "-bs", strconv.Itoa(t.options.BeamSize),
		"-ng", "-nt", "-otxt", "-of", output,
	}
	if strings.TrimSpace(t.glossary) != "" {
		arguments = append(arguments, "--prompt", t.glossary)
	}
	cmd := exec.CommandContext(ctx, t.binary, arguments...)
	cmd.Dir = filepath.Dir(t.binary)
	cmd.WaitDelay = 2 * time.Second
	platform.Hide(cmd)
	// CLI diagnostics can contain the transcription. Read the output file,
	// and keep raw stdout/stderr out of user errors and application logs.
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := t.run(cmd); err != nil {
		if ctx.Err() != nil {
			return transcript.ChunkResult{}, ctx.Err()
		}
		return transcript.ChunkResult{}, fmt.Errorf("O Whisper não conseguiu transcrever. Confira o motor, o arquivo GGML e a memória disponível: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return transcript.ChunkResult{}, err
	}
	file, err := os.Open(output + ".txt")
	if err != nil {
		return transcript.ChunkResult{}, errors.New("O Whisper terminou sem produzir a transcrição. Confira se whisper-cli.exe é compatível com o modelo GGML.")
	}
	defer file.Close()
	payload, err := io.ReadAll(io.LimitReader(file, 4*processingTextLimit+1))
	if err != nil || len(payload) > 4*processingTextLimit || !utf8.Valid(payload) {
		return transcript.ChunkResult{}, errors.New("O Whisper produziu um texto inválido ou excessivo. Tente um modelo diferente.")
	}
	text := strings.TrimSpace(strings.TrimPrefix(string(payload), "\ufeff"))
	return transcript.ChunkResult{Text: text, Usage: map[string]any{}, ElapsedSeconds: time.Since(started).Seconds()}, nil
}

const processingTextLimit = 100000

func (t *transcriber) Close() error { return nil }

// Fingerprint includes the actual engine and model contents, so replacing a
// custom model in the same path cannot reuse a transcription from old weights.
func Fingerprint(directory string) func(context.Context, config.Settings) (string, error) {
	return func(ctx context.Context, settings config.Settings) (string, error) {
		binary, model := Paths(directory, settings.Whisper)
		hash := sha256.New()
		buffer := make([]byte, 256*1024)
		for _, path := range []string{binary, model} {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			file, err := os.Open(path)
			if errors.Is(err, os.ErrNotExist) {
				// Silence is handled without initializing a model. A speech
				// part reports the missing installation from New.
				hash.Write([]byte("missing:" + path + "\x00"))
				continue
			}
			if err != nil {
				return "", fmt.Errorf("identificar os arquivos Whisper: %w", err)
			}
			for {
				read, readErr := file.Read(buffer)
				hash.Write(buffer[:read])
				if ctx.Err() != nil {
					file.Close()
					return "", ctx.Err()
				}
				if readErr != nil {
					closeErr := file.Close()
					if errors.Is(readErr, io.EOF) && closeErr == nil {
						break
					}
					return "", fmt.Errorf("identificar o modelo Whisper: %w", errors.Join(readErr, closeErr))
				}
			}
			hash.Write([]byte{0})
		}
		return hex.EncodeToString(hash.Sum(nil)), nil
	}
}
