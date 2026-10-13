package recorder

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestExportSliceReadsWrittenSamplesDuringCaptureAndExcludesPause(t *testing.T) {
	t.Parallel()
	input := new(syntheticInput)
	recording := NewWithInput(-1, t.TempDir(), input)
	if _, err := recording.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = recording.Stop() })
	input.feed(t, pcmSamples(11, 22, 33), nil)
	recording.Pause()
	input.feed(t, pcmSamples(999, 999), nil)
	recording.Resume()
	input.feed(t, pcmSamples(44, 55), nil)
	deadline := time.Now().Add(5 * time.Second)
	for recording.Snapshot().WrittenSamples < 5 {
		if time.Now().After(deadline) {
			t.Fatal("writer did not save the PCM")
		}
		time.Sleep(time.Millisecond)
	}
	destination := filepath.Join(t.TempDir(), "part.wav")
	if err := ExportSlice(t.Context(), recording.Snapshot(), 1, 4, destination); err != nil {
		t.Fatal(err)
	}
	rate, pcm := readRecording(t, destination)
	if rate != 16000 || !bytes.Equal(pcm, pcmSamples(22, 33, 44)) || !recording.Snapshot().Active {
		t.Fatal("slice changed samples, included the pause or stopped capture")
	}
	if err := ExportSlice(t.Context(), recording.Snapshot(), 0, 6, filepath.Join(t.TempDir(), "future.wav")); err == nil {
		t.Fatal("slice admitted samples not yet written")
	}
	original, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if err := ExportSlice(t.Context(), recording.Snapshot(), 0, 1, destination); !errors.Is(err, os.ErrExist) {
		t.Fatalf("an existing slice could be overwritten: %v", err)
	}
	unchanged, err := os.ReadFile(destination)
	if err != nil || !bytes.Equal(unchanged, original) {
		t.Fatal("existing slice was altered")
	}
}

func TestExportSliceCancellationAndShortSourceLeaveNoResult(t *testing.T) {
	t.Parallel()
	source := filepath.Join(t.TempDir(), "short.wav")
	if err := os.WriteFile(source, make([]byte, waveHeaderBytes+2), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot := Snapshot{Path: source, SampleRate: 16000, WrittenSamples: 2}
	destination := filepath.Join(t.TempDir(), "slice.wav")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := ExportSlice(ctx, snapshot, 0, 2, destination); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled extraction was admitted")
	}
	if err := ExportSlice(t.Context(), snapshot, 0, 2, destination); err == nil {
		t.Fatal("a truncated source was accepted")
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed extraction left a WAV that could be used as a complete slice")
	}
}
