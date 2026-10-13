package whisper

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"ai-talker/internal/audio"
	"ai-talker/internal/config"
	"ai-talker/internal/transcript"
)

//go:embed testdata/motor.wav
var checkAudio []byte

const checkPhrase = "Hello. This is a short recording."

type CheckResult struct {
	Text           string
	ElapsedSeconds float64
}

// Check exercises local inference with bundled synthetic speech. It uses the
// selected engine, model and CPU options without a microphone or AWS request.
func Check(ctx context.Context, directory string, settings config.Settings) (CheckResult, error) {
	return check(ctx, directory, settings, Factory(directory))
}

func check(
	ctx context.Context,
	directory string,
	settings config.Settings,
	factory transcript.Factory,
) (result CheckResult, err error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := settings.Whisper.Validate(); err != nil {
		return result, err
	}
	if err := checkFiles(directory, settings.Whisper); err != nil {
		return result, err
	}
	// The bundled sample is in English. The user's language and glossary are
	// preserved; only this diagnostic request uses the sample's language.
	settings.Whisper.Language = "en"
	settings.Glossary = ""
	started := time.Now()
	engine, err := factory(ctx, settings)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, engine.Close()) }()
	transcription, err := engine.Transcribe(ctx, audio.Chunk{
		Audio: checkAudio, Format: audio.FormatWAV, HasSpeech: true, Duration: 3.5,
	})
	if err != nil {
		return result, fmt.Errorf("o teste de transcrição local falhou: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if !recognizesCheckPhrase(transcription.Text) {
		return result, fmt.Errorf(
			"o motor executou, mas não reconheceu a amostra de voz esperada (%s). Confira o arquivo GGML ou tente outro modelo",
			checkPhrase,
		)
	}
	return CheckResult{Text: strings.TrimSpace(transcription.Text), ElapsedSeconds: time.Since(started).Seconds()}, nil
}

func checkFiles(directory string, options config.Whisper) error {
	binary, model := Paths(directory, options)
	files := []struct{ path, name, remedy string }{
		{binary, "motor Whisper", "baixe o motor ou selecione whisper-cli.exe"},
		{model, "modelo Whisper", "baixe o modelo selecionado ou escolha um arquivo GGML"},
	}
	automatic, absErr := filepath.Abs(BinaryPath(directory))
	selected, selectedErr := filepath.Abs(binary)
	if absErr == nil && selectedErr == nil && strings.EqualFold(automatic, selected) {
		for _, name := range engineFiles[1:] {
			files = append(files, struct{ path, name, remedy string }{
				filepath.Join(filepath.Dir(binary), name), name, "baixe novamente o motor para restaurar suas DLLs",
			})
		}
	}
	var missing []string
	for _, file := range files {
		info, err := os.Stat(file.path)
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			missing = append(missing, fmt.Sprintf("%s ausente ou vazio: %s", file.name, file.remedy))
		}
	}
	if len(missing) != 0 {
		return errors.New(strings.Join(missing, ". "))
	}
	return nil
}

func recognizesCheckPhrase(text string) bool {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) })
	seen := make(map[string]bool, len(words))
	for _, word := range words {
		seen[word] = true
	}
	matches := 0
	for _, word := range []string{"hello", "this", "is", "a", "short", "recording"} {
		if seen[word] {
			matches++
		}
	}
	return seen["short"] && seen["recording"] && matches >= 4
}
