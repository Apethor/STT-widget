package playback

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"ai-talker/internal/audio"
	"ai-talker/internal/platform"
)

// ExportMP3 uses the exact playback mix, independent of playback volume.
// Replacement happens only after encoding completes; failures keep the target.
func ExportMP3(ctx context.Context, source, destination, directory string) (err error) {
	absolute, err := filepath.Abs(destination)
	if err != nil || destination == "" || !strings.EqualFold(filepath.Ext(absolute), ".mp3") {
		return errors.New("selecione um arquivo MP3 para exportar")
	}
	inputs, err := recordingInputs(source)
	if err != nil {
		return err
	}
	for _, input := range inputs {
		if strings.EqualFold(input.path, absolute) {
			return errors.New("escolha outro destino para preservar a gravação original")
		}
		if original, err := os.Stat(input.path); err == nil {
			if target, err := os.Stat(absolute); err == nil && os.SameFile(original, target) {
				return errors.New("escolha outro destino para preservar a gravação original")
			}
		}
	}
	prepared, err := prepareAudio(ctx, source, directory)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, prepared.remove()) }()
	processor, err := audio.New()
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(absolute), ".export-*.mp3")
	if err != nil {
		return fmt.Errorf("reservar o destino de exportação: %w", err)
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err := file.Close(); err != nil {
		return err
	}
	command := exec.CommandContext(ctx, processor.FFmpeg, "-v", "error", "-nostdin", "-y",
		"-protocol_whitelist", "file,pipe", "-i", prepared.path, "-vn",
		"-ar", "48000", "-ac", "2", "-codec:a", "libmp3lame", "-b:a", "192k", "-f", "mp3", temporary)
	platform.Hide(command)
	command.WaitDelay = 3 * time.Second
	var diagnostic diagnosticBuffer
	command.Stderr = &diagnostic
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("exportar MP3: %s: %w", strings.TrimSpace(diagnostic.String()), err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(temporary, absolute); err != nil {
		return fmt.Errorf("salvar o MP3: %w", err)
	}
	return nil
}
