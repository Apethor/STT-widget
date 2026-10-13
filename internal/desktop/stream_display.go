package desktop

import (
	"math"
	"slices"

	"ai-talker/internal/live"
)

// Pending retranscription replaces speech only after the complete attempt
// succeeds. Late screenshots still update the previous published timeline.
func (session *liveRecording) displayBlocks(retranscribing bool) ([]live.Block, bool) {
	retain := retranscribing && len(session.PreviousBlocks) > 0 &&
		slices.ContainsFunc(session.Entries, func(entry live.Entry) bool {
			return !entry.Screenshot && (entry.Pending || entry.Error != "")
		})
	if !retain {
		return session.textBlocks(), false
	}
	if len(session.PreviousEntries) > 0 {
		previous := &liveRecording{liveArchive: session.liveArchive}
		previous.Entries = session.previousDisplayEntries()
		return previous.textBlocks(), true
	}

	// Older archives kept only rendered blocks, without speech positions.
	// Preserve those blocks until retry completes and append late photos.
	blocks := slices.Clone(session.PreviousBlocks)
	var paths []string
	for _, entry := range session.Entries {
		if !entry.Screenshot || entry.Hidden || entry.Path == "" {
			continue
		}
		if !session.InlineScreenshots {
			paths = append(paths, entry.Path)
			continue
		}
		index := slices.IndexFunc(blocks, func(block live.Block) bool { return block.ID == entry.ID })
		if index < 0 {
			blocks = append(blocks, live.Block{ID: entry.ID, Text: entry.Path})
		} else {
			blocks[index].Text = entry.Path
		}
	}
	if !session.InlineScreenshots {
		blocks = slices.DeleteFunc(blocks, func(block live.Block) bool { return block.ID == math.MaxUint64 })
		if footer := screenshotFooter(paths); footer != "" {
			blocks = append(blocks, live.Block{ID: math.MaxUint64, Text: footer})
		}
	}
	return blocks, true
}

func (session *liveRecording) previousDisplayEntries() []live.Entry {
	screenshots := make(map[uint64]live.Entry)
	for _, entry := range session.Entries {
		if entry.Screenshot {
			screenshots[entry.ID] = entry
		}
	}
	entries := slices.Clone(session.PreviousEntries)
	entries = slices.DeleteFunc(entries, func(entry live.Entry) bool {
		_, exists := screenshots[entry.ID]
		return entry.Screenshot && !exists
	})
	for index, entry := range entries {
		if entry.Screenshot {
			entries[index] = screenshots[entry.ID]
		}
	}
	return entries
}
