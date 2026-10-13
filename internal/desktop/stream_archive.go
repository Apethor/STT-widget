package desktop

import (
	"errors"
	"math"
	"path/filepath"
	"slices"
	"strings"

	"ai-talker/internal/live"
	"ai-talker/internal/recorder"
)

func (archive *liveArchive) needsRetry() bool {
	cursors := make(map[string]uint64, len(archive.Tracks))
	for _, entry := range archive.Entries {
		if !entry.Screenshot {
			cursors[entry.Track] = max(cursors[entry.Track], entry.LastSample)
		}
	}
	return slices.ContainsFunc(archive.Tracks, func(track recorder.Track) bool {
		return track.Snapshot.Active || track.Snapshot.WrittenSamples > cursors[track.ID]
	}) || slices.ContainsFunc(archive.Entries, func(entry live.Entry) bool {
		return !entry.Screenshot && !entry.Hidden && (entry.Pending || entry.Error != "" || entry.NeedsReview)
	})
}

func (d *desktop) validLiveArchive(archive *liveArchive) bool {
	if archive == nil {
		return true
	}
	inside := func(path, folder, extension string) bool {
		return path != "" && captureDirectory(filepath.Dir(path), filepath.Join(d.directory, folder)) &&
			strings.EqualFold(filepath.Ext(path), extension)
	}
	finite := func(value float64) bool { return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0) }
	if !inside(archive.Source, "gravacoes", ".wav") || len(archive.Tracks) < 1 || len(archive.Tracks) > 2 ||
		len(archive.Entries) > 100000 || len(archive.PreviousBlocks) > 100001 || len(archive.PreviousEntries) > 100000 {
		return false
	}
	if len(archive.PreviousEntries) > 0 {
		previous := *archive
		previous.Entries, previous.PreviousEntries = archive.PreviousEntries, nil
		if !d.validLiveArchive(&previous) {
			return false
		}
	}
	tracks := make(map[string]recorder.Track)
	for _, track := range archive.Tracks {
		if _, duplicate := tracks[track.ID]; duplicate {
			return false
		}
		if track.ID != "microphone" && track.ID != "system" || !finite(track.Offset) ||
			!finite(track.Snapshot.Duration) || !inside(track.Snapshot.Path, "gravacoes", ".wav") ||
			track.Snapshot.SampleRate != 16000 && track.Snapshot.SampleRate != 48000 ||
			track.Snapshot.WrittenSamples > math.MaxUint32/2 {
			return false
		}
		tracks[track.ID] = track
	}
	ids := make(map[uint64]bool)
	for _, entry := range archive.Entries {
		if entry.ID == 0 || entry.ID > archive.NextID || ids[entry.ID] || !finite(entry.Start) || !finite(entry.End) || entry.End < entry.Start {
			return false
		}
		ids[entry.ID] = true
		if entry.Screenshot {
			if entry.Path != "" && entry.Path != "[Screenshot removida]" && !inside(entry.Path, "screenshots", ".png") {
				return false
			}
			continue
		}
		track, exists := tracks[entry.Track]
		if !exists || entry.FirstSample >= entry.LastSample || entry.LastSample > track.Snapshot.WrittenSamples ||
			entry.ContextSamples > entry.FirstSample || entry.ContextSamples > uint64(streamContextSeconds*float64(track.Snapshot.SampleRate)) ||
			entry.ContextSamples > 0 && !archive.isStream() && !archive.Diarization ||
			!finite(entry.ContextSeconds) || entry.ContextSeconds > streamContextSeconds ||
			entry.ContextSeconds > 0 && entry.ContextSamples == 0 {
			return false
		}
	}
	type blockIdentity struct {
		id      uint64
		segment uint32
	}
	blocks := make(map[blockIdentity]bool, len(archive.PreviousBlocks))
	for _, block := range archive.PreviousBlocks {
		identity := blockIdentity{id: block.ID, segment: block.Segment}
		if block.ID == 0 || block.ID > archive.NextID && block.ID != math.MaxUint64 ||
			block.Segment > 100000 || blocks[identity] {
			return false
		}
		blocks[identity] = true
	}
	return true
}

// A session saved while capture was active can have more finalized samples than
// its last checkpoint. Recovery reads those bounds off the UI thread.
func (d *desktop) recoverLiveTracks(session *liveRecording, complete func(error)) {
	tracks := slices.Clone(session.Tracks)
	epoch, ctx := session.epoch, session.ctx
	needsRead := slices.ContainsFunc(tracks, func(track recorder.Track) bool { return track.Snapshot.Active })
	if !needsRead {
		d.queueLiveParts(session, true)
		complete(nil)
		return
	}
	d.operation(func() error {
		var failures error
		for index := range tracks {
			if err := ctx.Err(); err != nil {
				return err
			}
			snapshot, err := recorder.Inspect(tracks[index].Snapshot.Path)
			if err != nil {
				failures = errors.Join(failures, err)
				continue
			}
			snapshot.StartedAt = tracks[index].Snapshot.StartedAt
			tracks[index].Snapshot = snapshot
		}
		return failures
	}, func(err error) {
		if session.doc.closed || session.doc.live != session || session.canceled || session.epoch != epoch {
			return
		}
		if err == nil {
			session.Tracks = tracks
			d.queueLiveParts(session, true)
		}
		complete(err)
	})
}
