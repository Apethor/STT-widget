package desktop

import (
	"ai-talker/internal/config"
	"ai-talker/internal/live"
)

const cloudTeamPartSeconds = 60

func offlinePartSeconds(settings config.Settings) float64 {
	seconds := min(teamPartSeconds, settings.ChunkSeconds)
	model, _ := config.CloudModelByID(settings.CloudModelID())
	if settings.TranscriptionProvider() == config.ProviderBedrock && model.Backend == config.CloudConverse {
		seconds = min(seconds, cloudTeamPartSeconds)
	}
	return float64(seconds)
}

// Old Team recordings retain their completed entries. Only pending ranges are
// rebuilt with the current bound; sample coverage and screenshot cuts stay exact.
func (session *liveRecording) rechunkPendingParts() {
	if session.isStream() {
		return
	}
	var entries []live.Entry
	for _, entry := range session.Entries {
		if entry.Screenshot || !entry.Pending {
			entries = append(entries, entry)
			continue
		}
		var rate int
		var offset float64
		for _, track := range session.Tracks {
			if track.ID == entry.Track {
				rate, offset = track.Snapshot.SampleRate, track.Offset
				break
			}
		}
		if rate <= 0 {
			entries = append(entries, entry)
			continue
		}
		limit := uint64(offlinePartSeconds(session.settings) * float64(rate))
		if limit == 0 || entry.LastSample-entry.FirstSample <= limit {
			entries = append(entries, entry)
			continue
		}
		for first := entry.FirstSample; first < entry.LastSample; {
			part := entry
			part.FirstSample, part.LastSample = first, min(first+limit, entry.LastSample)
			part.Text, part.Error, part.NeedsReview, part.ContextSamples = "", "", false, 0
			if first > entry.FirstSample {
				session.NextID++
				part.ID = session.NextID
				part.Start = offset + float64(first)/float64(rate)
			}
			if part.LastSample < entry.LastSample {
				part.End = offset + float64(part.LastSample)/float64(rate)
			}
			entries = append(entries, part)
			first = part.LastSample
		}
	}
	session.Entries = entries
}
