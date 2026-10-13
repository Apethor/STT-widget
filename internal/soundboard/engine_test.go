package soundboard

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testRenderDevice struct{ closed atomic.Bool }

func (*testRenderDevice) Wait() error               { time.Sleep(time.Millisecond); return nil }
func (*testRenderDevice) Write(func([]int16)) error { return nil }
func (d *testRenderDevice) Close() error            { d.closed.Store(true); return nil }
func (*testRenderDevice) Latency() time.Duration    { return 20 * time.Millisecond }

func readyTestEngine(t *testing.T) (*Engine, *testRenderDevice) {
	t.Helper()
	device := &testRenderDevice{}
	engine := NewEngine()
	engine.open = func() (renderDevice, error) { return device, nil }
	if err := engine.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(engine.Close)
	return engine, device
}

func TestPreparedEffectsReplaceRestartStopWithoutDecodeOrQueue(t *testing.T) {
	t.Parallel()
	engine, _ := readyTestEngine(t)
	var decodes atomic.Int32
	engine.decode = func(_ context.Context, path string) ([]int16, error) {
		decodes.Add(1)
		if path == "a" {
			return []int16{1000, 1000, 2000, 2000, 3000, 3000}, nil
		}
		return []int16{-1000, -1000, -2000, -2000}, nil
	}
	for _, id := range []string{"a", "b"} {
		if err := engine.Prepare(t.Context(), id, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := engine.Play("missing"); err == nil {
		t.Fatal("unprepared sound was playable")
	}
	target := make([]int16, 2)
	engine.SetVolume(50)
	if err := engine.Play("a"); err != nil {
		t.Fatal(err)
	}
	engine.fill(target)
	if !slices.Equal(target, []int16{500, 500}) {
		t.Fatal("independent PCM volume was not applied:", target)
	}
	if err := engine.Play("a"); err != nil {
		t.Fatal(err)
	}
	engine.fill(target)
	if !slices.Equal(target, []int16{500, 500}) {
		t.Fatal("same sound did not restart:", target)
	}
	if err := engine.Play("a"); err != nil {
		t.Fatal(err)
	}
	if err := engine.Play("b"); err != nil {
		t.Fatal(err)
	}
	engine.fill(target)
	if !slices.Equal(target, []int16{-500, -500}) {
		t.Fatal("latest click did not replace pending sound:", target)
	}
	timing := engine.Timing()
	if timing.ID != "b" || timing.Submitted.IsZero() || timing.Submitted.Before(timing.Requested) {
		t.Fatal("first PCM submission was not measured:", timing)
	}
	engine.Stop()
	engine.fill(target)
	if engine.Active() || !slices.Equal(target, []int16{0, 0}) || decodes.Load() != 2 {
		t.Fatal("stop retained audio or clicks decoded files again:", target, decodes.Load())
	}
}

func TestEngineOwnershipCloseAndConcurrentClicks(t *testing.T) {
	t.Parallel()
	engine, device := readyTestEngine(t)
	engine.decode = func(context.Context, string) ([]int16, error) {
		return []int16{1000, 1000, -1000, -1000}, nil
	}
	if err := engine.Prepare(t.Context(), "effect", "unused"); err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for range 4 {
		workers.Go(func() {
			samples := make([]int16, 2)
			for range 500 {
				_ = engine.Play("effect")
				engine.SetVolume(44)
				engine.fill(samples)
				_ = engine.Timing()
				engine.Stop()
			}
		})
	}
	workers.Wait()
	engine.Close()
	if !device.closed.Load() || engine.Ready("effect") {
		t.Fatal("the owning worker did not release its device")
	}
	if err := engine.Play("effect"); err == nil {
		t.Fatal("a closed device accepted playback")
	}
}

func TestEnginePreparationFailureAndRemovalPreserveOtherSounds(t *testing.T) {
	t.Parallel()
	engine, _ := readyTestEngine(t)
	engine.decode = func(_ context.Context, path string) ([]int16, error) {
		if path == "bad" {
			return nil, errors.New("invalid audio")
		}
		return []int16{1, 1, 2, 2}, nil
	}
	if err := engine.Prepare(t.Context(), "bad", "bad"); err == nil || engine.Ready("bad") {
		t.Fatal("failed preparation created a ready button")
	}
	for _, id := range []string{"first", "second"} {
		if err := engine.Prepare(t.Context(), id, "good"); err != nil {
			t.Fatal(err)
		}
	}
	_ = engine.Play("first")
	engine.Remove("first")
	if engine.Ready("first") || engine.Active() || !engine.Ready("second") {
		t.Fatal("removal retained active PCM or invalidated another effect")
	}
}

func TestLeadingSilenceDoesNotRetainTheDiscardedBackingArray(t *testing.T) {
	t.Parallel()
	pcm := make([]int16, outputRate*outputChannels)
	pcm[len(pcm)-4], pcm[len(pcm)-3] = 1000, 1000
	trimmed := trimEffectSilence(pcm)
	maxSamples := outputRate*outputChannels/500 + 4 // 2 ms prefix and two audible frames.
	if len(trimmed) > maxSamples || len(trimmed) < 4 || cap(trimmed) > maxSamples+128 {
		t.Fatalf("short prepared effect retained excessive storage: len=%d cap=%d", len(trimmed), cap(trimmed))
	}
	if &trimmed[0] == &pcm[len(pcm)-len(trimmed)] {
		t.Fatal("discarded silence remains reachable through the prepared slice")
	}
	if trimmed[len(trimmed)-4] != 1000 || trimmed[len(trimmed)-3] != 1000 {
		t.Fatal("compacting the prepared effect lost its audible frames")
	}
	prompt := []int16{1000, 1000, 0, 0}
	if direct := trimEffectSilence(prompt); &direct[0] != &prompt[0] {
		t.Fatal("an effect without a discarded prefix was copied unnecessarily")
	}
}
