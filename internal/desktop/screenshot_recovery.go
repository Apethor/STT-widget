package desktop

import (
	"slices"

	"ai-talker/internal/live"
)

// Deleting a photo also removes its presentation references from recoverable
// audio. Sent prompt/response pairs stay exact; speech and audio stay intact.
func removeAudioScreenshot(state *audioSnapshot, path string) bool {
	changed := false
	stripText := func(text *string) {
		updated := removeScreenshotLine(*text, path)
		changed = changed || updated != *text
		*text = updated
	}
	stripPaths := func(paths *[]string) {
		before := len(*paths)
		*paths = slices.DeleteFunc(*paths, func(item string) bool { return item == path })
		changed = changed || len(*paths) != before
	}
	stripText(&state.Text)
	stripPaths(&state.ScreenshotFiles)
	if state.Result != nil {
		stripText(&state.Result.Text)
	}
	if state.Append != nil {
		stripPaths(&state.Append.Screenshots)
		stripText(&state.Append.Prefix)
		stripText(&state.Append.Combined)
		stripText(&state.Append.Content)
	}
	if state.Live != nil {
		archive := state.Live
		removed := make(map[uint64]bool)
		stripEntries := func(entries *[]live.Entry) {
			before := len(*entries)
			*entries = slices.DeleteFunc(*entries, func(entry live.Entry) bool {
				if entry.Screenshot && entry.Path == path {
					removed[entry.ID] = true
					return true
				}
				return false
			})
			changed = changed || len(*entries) != before
		}
		stripEntries(&archive.Entries)
		stripEntries(&archive.PreviousEntries)
		before := len(archive.PreviousBlocks)
		archive.PreviousBlocks = slices.DeleteFunc(archive.PreviousBlocks, func(block live.Block) bool {
			return removed[block.ID]
		})
		changed = changed || len(archive.PreviousBlocks) != before
		for index := range archive.PreviousBlocks {
			stripText(&archive.PreviousBlocks[index].Text)
		}
		stripText(&archive.Prefix)
		stripText(&archive.Combined)
	}
	if state.Undo != nil {
		undo := audioSnapshot{Text: state.Undo.Text, Result: state.Undo.Result, Append: state.Undo.Append, Live: state.Undo.Live}
		changed = removeAudioScreenshot(&undo, path) || changed
		state.Undo.Text = undo.Text
	}
	return changed
}

func removeDraftScreenshot(steps []promptStep, path string) bool {
	changed := false
	for index := range steps {
		step := &steps[index]
		if !step.Sent && !step.Processed {
			text := removeScreenshotLine(step.Prompt, path)
			changed = changed || text != step.Prompt
			step.Prompt = text
		}
		if step.Draft != nil {
			text := removeScreenshotLine(*step.Draft, path)
			changed = changed || text != *step.Draft
			step.Draft = &text
		}
	}
	return changed
}

func removeDocumentSnapshotScreenshot(tab *documentSnapshot, path string) bool {
	state := audioSnapshot{Text: tab.Text, Result: tab.Result, Append: tab.Append, Live: tab.Live,
		Undo: tab.Undo, ScreenshotFiles: tab.ScreenshotFiles}
	changed := removeAudioScreenshot(&state, path)
	tab.Text, tab.ScreenshotFiles = state.Text, state.ScreenshotFiles
	for index := range tab.AudioHistory {
		changed = removeAudioScreenshot(&tab.AudioHistory[index], path) || changed
	}
	return removeDraftScreenshot(tab.PromptSteps, path) || changed
}

func (d *desktop) removeRecoveryScreenshot(path string) {
	for _, entry := range d.closedTabs {
		if entry.combined != nil {
			for index := range entry.combined.states {
				removeDocumentSnapshotScreenshot(&entry.combined.states[index], path)
			}
		}
		doc := entry.document
		if !doc.closed {
			continue
		}
		state := d.snapshotAudio(doc)
		changed := removeAudioScreenshot(&state, path)
		d.removeAudioHistoryScreenshot(doc, path)
		removeDraftScreenshot(doc.promptSteps, path)
		if !changed {
			continue
		}
		doc.lastResult, doc.recordedAppend, doc.undo = state.Result, state.Append, state.Undo
		doc.screenshotFiles, doc.currentPrompt = state.ScreenshotFiles, state.Text
		if !doc.browsingPrompts() {
			doc.historyApplying = true
			doc.editor.SetText(state.Text)
			doc.historyApplying = false
		}
		if doc.live != nil && state.Live != nil {
			doc.live.liveArchive = *state.Live
			doc.live.blocks, _ = doc.live.displayBlocks(doc.transcriptionRun != "")
			doc.live.rebuildScreenshotCuts()
		}
	}
}
