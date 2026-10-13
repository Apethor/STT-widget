package recorder

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"ai-talker/internal/store"
)

// Track describes one independent source on the recording's common timeline.
// Offset is measured from the earliest capture start.
type Track struct {
	ID       string   `json:"id"`
	Label    string   `json:"label"`
	Offset   float64  `json:"offset"`
	Snapshot Snapshot `json:"snapshot"`
}

type teamInput interface {
	Start(context.Context) (string, error)
	Stop() (string, error)
	Pause()
	Resume()
	Snapshot() Snapshot
}

// Team retains separate microphone and playback WAVs. It never mixes sources,
// so overlapping voices remain attributed to their source.
type Team struct {
	control sync.Mutex
	mu      sync.Mutex
	inputs  [2]teamInput
	started bool
	path    string
	err     error
}

func NewTeam(device int, microphone, playback, directory string) *Team {
	mic, system := NewShared(device, microphone, directory), NewSystem(playback, directory)
	mic.createFile = func(directory string) (recordingFile, string, error) {
		return createSourceRecordingFile(directory, "mic")
	}
	system.createFile = func(directory string) (recordingFile, string, error) {
		return createSourceRecordingFile(directory, "sis")
	}
	return newTeam(mic, system)
}

func newTeam(microphone, playback teamInput) *Team {
	return &Team{inputs: [2]teamInput{microphone, playback}}
}

func (t *Team) Start(ctx context.Context) (string, error) {
	t.control.Lock()
	defer t.control.Unlock()
	t.mu.Lock()
	if t.started {
		t.mu.Unlock()
		return "", ErrAlreadyRecording
	}
	t.mu.Unlock()
	if ctx == nil {
		return "", errors.New("contexto de gravação ausente")
	}
	type opened struct {
		index int
		path  string
		err   error
	}
	results := make(chan opened, 2)
	for index, input := range t.inputs {
		go func() {
			path, err := input.Start(ctx)
			results <- opened{index: index, path: path, err: err}
		}()
	}
	var paths [2]string
	var failure error
	for range t.inputs {
		result := <-results
		paths[result.index] = result.path
		if result.err != nil {
			failure = errors.Join(failure, fmt.Errorf("%s: %w", trackLabel(result.index), result.err))
		}
	}
	if failure != nil {
		for _, input := range t.inputs {
			_, stopErr := input.Stop()
			if !errors.Is(stopErr, ErrNotRecording) {
				failure = errors.Join(failure, stopErr)
			}
		}
	}
	t.mu.Lock()
	t.path, t.err, t.started = paths[0], failure, failure == nil
	t.mu.Unlock()
	if failure == nil {
		failure = t.saveManifest()
		if failure != nil {
			for _, input := range t.inputs {
				_, stopErr := input.Stop()
				failure = errors.Join(failure, stopErr)
			}
			t.mu.Lock()
			t.err, t.started = failure, false
			t.mu.Unlock()
		}
	}
	return paths[0], failure
}

func (t *Team) Stop() (string, error) {
	t.control.Lock()
	defer t.control.Unlock()
	results := make(chan error, 2)
	for _, input := range t.inputs {
		go func() { _, err := input.Stop(); results <- err }()
	}
	var failure error
	for range t.inputs {
		if err := <-results; err != nil && !errors.Is(err, ErrNoAudio) {
			failure = errors.Join(failure, err)
		}
	}
	t.mu.Lock()
	t.started = false
	t.err = errors.Join(t.err, failure)
	path, failure := t.path, t.err
	t.mu.Unlock()
	if path != "" {
		failure = errors.Join(failure, t.saveManifest())
	}
	tracks := t.Tracks()
	if failure == nil && slices.IndexFunc(tracks, func(track Track) bool { return track.Snapshot.WrittenSamples > 0 }) < 0 {
		failure = ErrNoAudio
	}
	return path, failure
}

func (t *Team) Pause() {
	for _, input := range t.inputs {
		input.Pause()
	}
}
func (t *Team) Resume() {
	for _, input := range t.inputs {
		input.Resume()
	}
}

func (t *Team) Tracks() []Track {
	tracks := []Track{
		{ID: "microphone", Label: "Você", Snapshot: t.inputs[0].Snapshot()},
		{ID: "system", Label: "Áudio do sistema", Snapshot: t.inputs[1].Snapshot()},
	}
	first := tracks[0].Snapshot.StartedAt
	if other := tracks[1].Snapshot.StartedAt; first.IsZero() || !other.IsZero() && other.Before(first) {
		first = other
	}
	for index := range tracks {
		if !first.IsZero() && !tracks[index].Snapshot.StartedAt.IsZero() {
			tracks[index].Offset = max(0, tracks[index].Snapshot.StartedAt.Sub(first).Seconds())
		}
	}
	return tracks
}

func (t *Team) Snapshot() Snapshot {
	tracks := t.Tracks()
	t.mu.Lock()
	path, failure := t.path, t.err
	t.mu.Unlock()
	result := Snapshot{Path: path, SampleRate: tracks[0].Snapshot.SampleRate}
	for _, track := range tracks {
		state := track.Snapshot
		result.Duration = max(result.Duration, track.Offset+state.Duration)
		result.Active = result.Active || state.Active
		result.Paused = result.Paused || state.Paused
		result.Level = max(result.Level, state.Level)
		if state.Error != "" && !(state.Duration == 0 && !state.Active) {
			result.Error += track.Label + ": " + state.Error + "\n"
		}
		if state.Warning != "" {
			result.Warning = state.Warning
		}
		if result.StartedAt.IsZero() || state.StartedAt.Before(result.StartedAt) {
			result.StartedAt = state.StartedAt
		}
	}
	if failure != nil {
		result.Error = failure.Error()
	}
	return result
}

type TeamManifest struct {
	Version int       `json:"version"`
	Created time.Time `json:"created"`
	Tracks  []Track   `json:"tracks"`
}

func (t *Team) saveManifest() error {
	t.mu.Lock()
	path := t.path
	t.mu.Unlock()
	if path == "" {
		return nil
	}
	return store.WriteJSON(path+".team.json", TeamManifest{Version: 1, Created: time.Now(), Tracks: t.Tracks()})
}

func trackLabel(index int) string {
	if index == 0 {
		return "Microfone"
	}
	return "Áudio do sistema"
}
