//go:build windows && cgo && integration

package pcmoutput

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"testing"
	"time"
)

// This opt-in test exercises the real response device at zero output volume.
// It never records microphone/loopback audio or changes CoreAudio volumes.
func TestNativeResponseGrowingPauseSeekDrainAndCancel(t *testing.T) {
	if os.Getenv("AI_TALKER_PCMOUTPUT_TEST") != "1" {
		t.Skip("set AI_TALKER_PCMOUTPUT_TEST=1 to test the native output silently")
	}
	const rate = 24000
	pcm := make([]byte, rate*2/5)
	for frame := range len(pcm) / 2 {
		value := int16(12000 * math.Sin(2*math.Pi*440*float64(frame)/rate))
		binary.LittleEndian.PutUint16(pcm[frame*2:], uint16(value))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	player := New("", rate, 0)
	if err := player.Append(pcm[:len(pcm)/2]); err != nil {
		t.Fatal(err)
	}
	done := runNativeResponse(ctx, player)
	defer awaitNativeResponseCleanup(t, cancel, done)
	awaitNativeResponse(t, ctx, done, func() bool { return player.Snapshot().Position > 0 })
	player.SetPaused(true)
	if err := player.Append(pcm[len(pcm)/2:]); err != nil {
		t.Fatal(err)
	}
	player.Finish()
	player.Seek(0)
	time.Sleep(30 * time.Millisecond)
	if snapshot := player.Snapshot(); !snapshot.Active || !snapshot.Paused || snapshot.Position != 0 {
		t.Fatalf("paused growing response was not retained: %+v", snapshot)
	}
	player.SetPaused(false)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("native response failed to drain: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("native response did not finish after its PCM drained")
	}
	if snapshot := player.Snapshot(); snapshot.Active || math.Abs(snapshot.Position-0.2) > 0.001 {
		t.Fatalf("native EOS did not release the completed response: %+v", snapshot)
	}

	replayCtx, replayCancel := context.WithTimeout(context.Background(), 5*time.Second)
	replay := New("", rate, 0)
	if err := replay.Append(pcm); err != nil {
		replayCancel()
		t.Fatal(err)
	}
	replayDone := runNativeResponse(replayCtx, replay)
	defer awaitNativeResponseCleanup(t, replayCancel, replayDone)
	awaitNativeResponse(t, replayCtx, replayDone, func() bool { return replay.Snapshot().Position > 0 })
	replayCancel()
	select {
	case err := <-replayDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("native response cancellation returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("native response did not release the canceled output")
	}
	t.Log("native mono PCM24k: growing input, pause/seek, EOS drain, device reopen and cancellation passed at volume zero")
}

func runNativeResponse(ctx context.Context, player *Player) <-chan error {
	done := make(chan error, 1)
	go func() {
		done <- player.Run(ctx)
		close(done)
	}()
	return done
}

func awaitNativeResponse(t *testing.T, ctx context.Context, done <-chan error, ready func() bool) {
	t.Helper()
	for !ready() {
		select {
		case err := <-done:
			t.Fatalf("native response ended before rendering: %v", err)
		case <-ctx.Done():
			t.Fatal("native response did not render within its deadline")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func awaitNativeResponseCleanup(t *testing.T, cancel context.CancelFunc, done <-chan error) {
	t.Helper()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("native output worker did not terminate after cancellation")
	}
}
