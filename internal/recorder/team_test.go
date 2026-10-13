package recorder

import (
	"bytes"
	"errors"
	"testing"

	"ai-talker/internal/store"
)

func TestTeamKeepsIndependentSourcesPausesBothAndPersistsTracks(t *testing.T) {
	t.Parallel()
	micInput, systemInput := new(syntheticInput), new(syntheticInput)
	mic := NewWithInput(-1, t.TempDir(), micInput)
	system := NewWithInput(-1, t.TempDir(), systemInput)
	team := newTeam(mic, system)
	path, err := team.Start(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = team.Stop() })
	micInput.feed(t, pcmSamples(10, 20), nil)
	systemInput.feed(t, pcmSamples(30, 40), nil)
	team.Pause()
	micInput.feed(t, pcmSamples(999), nil)
	systemInput.feed(t, pcmSamples(999), nil)
	if !mic.Snapshot().Paused || !system.Snapshot().Paused {
		t.Fatal("team did not pause both sources")
	}
	team.Resume()
	micInput.feed(t, pcmSamples(50), nil)
	systemInput.feed(t, pcmSamples(60), nil)
	if _, err := team.Stop(); err != nil {
		t.Fatal(err)
	}
	_, micPCM := readRecording(t, path)
	_, systemPCM := readRecording(t, system.Snapshot().Path)
	if !bytes.Equal(micPCM, pcmSamples(10, 20, 50)) || !bytes.Equal(systemPCM, pcmSamples(30, 40, 60)) {
		t.Fatal("team mixed sources or included paused audio")
	}
	var manifest TeamManifest
	if err := store.ReadJSON(path+".team.json", &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Tracks) != 2 || manifest.Tracks[0].ID != "microphone" || manifest.Tracks[1].ID != "system" ||
		manifest.Tracks[0].Snapshot.WrittenSamples != 3 || manifest.Tracks[1].Snapshot.WrittenSamples != 3 ||
		team.Snapshot().Active {
		t.Fatal("manifest lost a track or did not describe finalized audio")
	}
}

func TestTeamFailedSecondSourceClosesSuccessfulMicrophone(t *testing.T) {
	t.Parallel()
	micInput := &syntheticInput{startPCM: pcmSamples(12)}
	systemInput := &syntheticInput{startError: errors.New("output unavailable")}
	mic := NewWithInput(-1, t.TempDir(), micInput)
	system := NewWithInput(-1, t.TempDir(), systemInput)
	team := newTeam(mic, system)
	path, err := team.Start(t.Context())
	if path == "" || err == nil || mic.Snapshot().Active || system.Snapshot().Active {
		t.Fatal("failed team startup leaked a running source or lost the preserved audio")
	}
	assertStopped(t, mic, micInput)
	assertStopped(t, system, systemInput)
}
