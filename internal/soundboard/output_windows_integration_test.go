//go:build windows && integration

package soundboard

import (
	"context"
	"os"
	"slices"
	"testing"
	"time"
)

// Sends only silence to the physical output (volume zero). First nonzero source
// PCM submission is still timed, but this is not an acoustic/microphone measure.
func TestPreparedNativeEffectSubmissionLatency(t *testing.T) {
	if os.Getenv("AI_TALKER_SOUNDBOARD_TEST") != "1" {
		t.Skip("native soundboard integration is opt-in")
	}
	board, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	engine := NewEngine()
	engine.SetVolume(0)
	if err := engine.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	for _, button := range board.Buttons() {
		path, err := board.Path(button.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := engine.Prepare(ctx, button.ID, path); err != nil {
			t.Fatalf("prepare %s: %v", button.ID, err)
		}
		t.Logf("prepared %s", button.ID)
	}
	var delays []time.Duration
	for round := range 6 {
		for _, button := range board.Buttons() {
			if err := engine.Play(button.ID); err != nil {
				t.Fatal(err)
			}
			deadline := time.NewTimer(250 * time.Millisecond)
			poll := time.NewTicker(time.Millisecond)
			for engine.Timing().Submitted.IsZero() {
				select {
				case <-ctx.Done():
					deadline.Stop()
					t.Fatal(ctx.Err())
				case <-deadline.C:
					t.Fatal("prepared PCM was not submitted within 250 ms")
				case <-poll.C:
				}
			}
			poll.Stop()
			deadline.Stop()
			timing := engine.Timing()
			delay := timing.Submitted.Sub(timing.Requested)
			delays = append(delays, delay)
			t.Logf("round=%d effect=%s request_to_PCM=%s device_latency_plus_buffer=%s",
				round+1, button.ID, delay, timing.DeviceLatency)
			if delay > 100*time.Millisecond {
				t.Error("prepared request exceeded 100 ms before device submission")
			}
			engine.Stop()
			timer := time.NewTimer(35 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				t.Fatal(ctx.Err())
			case <-timer.C:
			}
		}
	}
	slices.Sort(delays)
	t.Logf("n=%d min=%s median=%s p95=%s max=%s; physical acoustic onset was not measured",
		len(delays), delays[0], delays[len(delays)/2], delays[(len(delays)-1)*95/100], delays[len(delays)-1])
}
