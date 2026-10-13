//go:build windows && integration

package playback

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// This opt-in integration sends only silence to the default output device.
// It verifies native timing/pause/teardown without playing the user's audio.
func TestWindowsPlaybackSilentIntegration(t *testing.T) {
	if os.Getenv("AI_TALKER_PLAYBACK_TEST") != "1" {
		t.Skip("a reprodução nativa de silêncio é uma integração opcional")
	}
	directory := t.TempDir()
	source := filepath.Join(directory, "silêncio sintético.wav")
	silenceFixture(t, source, 2)
	player := New(directory)
	ctx, cancel := context.WithTimeout(t.Context(), 7*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- player.Run(ctx, source) }()
	awaitPlayback(t, func() bool {
		if player.Snapshot().Ready {
			return true
		}
		select {
		case err := <-result:
			t.Fatal("o Windows não abriu a saída:", err)
		default:
		}
		return false
	})
	awaitPlayback(t, func() bool { return player.Snapshot().Position >= 0.15 })
	player.SetPaused(true)
	time.Sleep(120 * time.Millisecond)
	position := player.Snapshot().Position
	time.Sleep(180 * time.Millisecond)
	if !player.Snapshot().Paused || math.Abs(player.Snapshot().Position-position) > 0.06 {
		t.Fatal("a pausa nativa não congelou a posição:", position, player.Snapshot())
	}
	player.SetPaused(false)
	if err := playbackResult(t, result); err != nil {
		t.Fatal("o Windows não retomou/concluiu a reprodução:", err)
	}
	if player.Snapshot().Active || player.Snapshot().Position < 1.9 {
		t.Fatal("a reprodução nativa não chegou ao fim")
	}
	files, err := os.ReadDir(filepath.Join(directory, "playback"))
	if err != nil || len(files) != 0 {
		t.Fatal("o Windows manteve o WAV temporário aberto")
	}

	ctx2, cancel2 := context.WithCancel(t.Context())
	result2 := make(chan error, 1)
	go func() { result2 <- player.Run(ctx2, source) }()
	awaitPlayback(t, func() bool { return player.Snapshot().Active })
	player.SetPaused(true)
	cancel2()
	if err := playbackResult(t, result2); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelar a segunda reprodução não liberou o dispositivo:", err)
	}
	t.Log("Windows: WAV com caminho acentuado, avanço, pausa/retomada, fim, nova reprodução, cancelamento e limpeza conferidos com silêncio.")
}

func TestWindowsConcurrentSilentPlayersKeepIndependentControl(t *testing.T) {
	if os.Getenv("AI_TALKER_PLAYBACK_TEST") != "1" {
		t.Skip("duas reproduções nativas de silêncio são uma integração opcional")
	}
	directory := t.TempDir()
	source := filepath.Join(directory, "silence.wav")
	before := silenceFixture(t, source, 4)
	first, second := New(directory), New(directory)
	first.SetVolume(27)
	second.SetVolume(84)
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	go func() { firstDone <- first.Run(ctx, source) }()
	go func() { secondDone <- second.Run(ctx, source) }()
	awaitPlayback(t, func() bool {
		select {
		case err := <-firstDone:
			t.Fatal("o primeiro player nativo não abriu:", err)
		case err := <-secondDone:
			t.Fatal("o segundo player nativo não abriu junto do primeiro:", err)
		default:
		}
		return first.Snapshot().Ready && second.Snapshot().Ready
	})
	first.SetPaused(true)
	first.SetVolume(0)
	at := second.Snapshot().Position
	awaitPlayback(t, func() bool {
		return first.Snapshot().Paused && second.Snapshot().Position > at+0.1
	})
	if !second.Snapshot().Active || second.Snapshot().Paused {
		t.Fatal("pausar ou silenciar um player alterou o outro")
	}
	cancel()
	for _, done := range []<-chan error{firstDone, secondDone} {
		if err := playbackResult(t, done); !errors.Is(err, context.Canceled) {
			t.Fatal("encerrar os players nativos não liberou ambos:", err)
		}
	}
	after, err := os.ReadFile(source)
	if err != nil || string(after) != string(before) {
		t.Fatal("dois players alteraram o áudio original", err)
	}
	remaining, err := os.ReadDir(filepath.Join(directory, "playback"))
	if err != nil || len(remaining) != 0 {
		t.Fatal("os players simultâneos deixaram cópias privadas abertas", err)
	}
}
