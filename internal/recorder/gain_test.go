package recorder

import (
	"bytes"
	"math"
	"path/filepath"
	"testing"
)

func TestRecordingGainChangesSavedPCMAndMeterWithoutMutatingInput(t *testing.T) {
	t.Parallel()
	input := &syntheticInput{}
	recorder := NewWithInput(0, t.TempDir(), input)
	recorder.SetGain(0.5)
	path, err := recorder.Start(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	pcm := pcmSamples(12000, -12000, 0)
	original := append([]byte(nil), pcm...)
	input.feed(t, pcm, nil)
	if !bytes.Equal(pcm, original) || recorder.Snapshot().Level != 6000.0/32768 {
		t.Fatal("gain changed the device buffer or the level meter ignored it")
	}
	recorder.SetGain(2)
	input.feed(t, pcmSamples(20000, -20000), nil)
	recorder.SetGain(0)
	input.feed(t, pcmSamples(16000, -16000), nil)
	if recorder.Snapshot().Level != 0 {
		t.Fatal("mute still showed input volume")
	}
	recorder.Pause()
	recorder.SetGain(1)
	input.feed(t, pcmSamples(5000), nil)
	recorder.Resume()
	input.feed(t, pcmSamples(1000), nil)
	if _, err := recorder.Stop(); err != nil {
		t.Fatal(err)
	}
	_, saved := readRecording(t, path)
	want := pcmSamples(6000, -6000, 0, 32767, -32768, 0, 0, 1000)
	if !bytes.Equal(saved, want) || recorder.Snapshot().WrittenSamples != 8 {
		t.Fatalf("gain clipped incorrectly, lost muted samples or recorded the paused interval: %v", saved)
	}
}

func TestRecordingGainRejectsNonFiniteValuesAndClampsItsRange(t *testing.T) {
	t.Parallel()
	input := &syntheticInput{}
	recorder := NewWithInput(0, t.TempDir(), input)
	recorder.SetGain(0.5)
	recorder.SetGain(math.NaN())
	recorder.SetGain(math.Inf(1))
	path, err := recorder.Start(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	input.feed(t, pcmSamples(1000), nil)
	recorder.SetGain(-10)
	input.feed(t, pcmSamples(1000), nil)
	recorder.SetGain(10)
	input.feed(t, pcmSamples(1000), nil)
	if _, err := recorder.Stop(); err != nil {
		t.Fatal(err)
	}
	_, saved := readRecording(t, path)
	if !bytes.Equal(saved, pcmSamples(500, 0, 2000)) {
		t.Fatal("invalid gains corrupted the recording")
	}
}

func TestTeamGainControlsEachSourceIndependently(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	microphone, playback := &syntheticInput{}, &syntheticInput{}
	first := NewWithInput(0, filepath.Join(directory, "mic"), microphone)
	second := NewWithInput(0, filepath.Join(directory, "others"), playback)
	team := newTeam(first, second)
	team.SetGains(0, 0.5)
	if _, err := team.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	microphone.feed(t, pcmSamples(10000), nil)
	playback.feed(t, pcmSamples(10000), nil)
	team.SetGains(2, 1)
	microphone.feed(t, pcmSamples(10000), nil)
	playback.feed(t, pcmSamples(10000), nil)
	if _, err := team.Stop(); err != nil {
		t.Fatal(err)
	}
	tracks := team.Tracks()
	_, mic := readRecording(t, tracks[0].Snapshot.Path)
	_, system := readRecording(t, tracks[1].Snapshot.Path)
	if !bytes.Equal(mic, pcmSamples(0, 20000)) || !bytes.Equal(system, pcmSamples(5000, 10000)) {
		t.Fatal("the two team volume controls were applied to the same source")
	}
}
