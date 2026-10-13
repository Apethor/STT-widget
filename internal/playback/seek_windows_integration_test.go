//go:build windows && integration

package playback

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ai-talker/internal/audio"
)

func TestWindowsPlaybackSeeksForwardBackwardPausedAndToEnd(t *testing.T) {
	if os.Getenv("AI_TALKER_PLAYBACK_TEST") != "1" {
		t.Skip("seek nativo com silêncio é uma integração opcional")
	}
	directory := t.TempDir()
	source := filepath.Join(directory, "silence.wav")
	payload, err := audio.EncodeWAV(t.Context(), make([]byte, 4*audio.SampleRate*2))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	player := New(directory)
	player.SetVolume(0)
	done := make(chan error, 1)
	go func() { done <- player.Run(ctx, source) }()
	await := func(predicate func(Snapshot) bool) {
		t.Helper()
		deadline := time.NewTimer(3 * time.Second)
		defer deadline.Stop()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for !predicate(player.Snapshot()) {
			select {
			case err := <-done:
				t.Fatal("a reprodução encerrou antes do ajuste", err, player.Snapshot())
			case <-deadline.C:
				t.Fatal("o Windows não aplicou a posição pedida", player.Snapshot())
			case <-ticker.C:
			}
		}
	}
	await(func(state Snapshot) bool { return state.Ready })
	player.SetPaused(true)
	player.Seek(3)
	await(func(state Snapshot) bool { return state.Paused && state.Position >= 2.99 && state.Position <= 3.1 })
	player.Seek(0.5)
	await(func(state Snapshot) bool { return state.Position >= 0.49 && state.Position <= 0.6 })
	player.SetPaused(false)
	await(func(state Snapshot) bool { return !state.Paused && state.Position > 0.65 })
	player.Seek(4)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal("selecionar o fim não concluiu normalmente", err)
		}
	case <-ctx.Done():
		t.Fatal("selecionar o fim deixou a reprodução ativa")
	}
	original, err := os.ReadFile(source)
	if err != nil || string(original) != string(payload) {
		t.Fatal("mudar a posição alterou o áudio original", err)
	}
}
