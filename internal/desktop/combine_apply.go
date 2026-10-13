package desktop

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

func (d *desktop) applyCombinedText(sources []combineSource, text string) error {
	return d.applyCombinedTextWithLabel(sources, text, "Juntar")
}

func (d *desktop) applyCombinedTextWithLabel(sources []combineSource, text, operation string) error {
	if !d.sourcesCurrent(sources) || strings.TrimSpace(text) == "" {
		return errors.New("as abas mudaram; junte novamente os textos atuais")
	}
	original, err := detachedWorkspace(d.snapshotWorkspace())
	if err != nil {
		return err
	}
	if err := d.writeWorkspace(original); err != nil {
		return fmt.Errorf("não foi possível preservar as abas: %w", err)
	}
	combined := &combinedDocuments{label: operation}
	sourceIDs := make(map[uint64]bool, len(sources))
	var screenshots []string
	for _, source := range sources {
		doc := source.document
		if err := d.writeDraft(source.text, "", &doc.draftText, "Texto"); err != nil {
			return err
		}
		if err := d.writeDraft(doc.processEditor.Text, "", &doc.processDraftText, "Processado"); err != nil {
			return err
		}
		sourceIDs[doc.id] = true
		combined.documents = append(combined.documents, doc)
		for _, state := range original.Tabs {
			if state.ID == doc.id {
				combined.states = append(combined.states, state)
				break
			}
		}
		for _, path := range doc.screenshotFiles {
			if !slices.Contains(screenshots, path) {
				screenshots = append(screenshots, path)
			}
		}
	}
	successor, err := detachedWorkspace(original)
	if err != nil {
		return err
	}
	first := sources[0].document
	closedEntry := &closedDocument{
		document: first, index: 0, group: first.group, groupIndex: slices.Index(d.groups, first.group),
		groupName: d.groupNames[first.group], groupNameWidth: d.groupNameWidths[first.group],
		groupAutomaticName: d.groupAutomaticNames[first.group], combined: combined,
	}
	selected := d.document
	if selected.group == first.group {
		selected = first
	}
	successor.Selected = selected.id
	successor.Tabs = slices.DeleteFunc(successor.Tabs, func(tab documentSnapshot) bool {
		return sourceIDs[tab.ID] && tab.ID != first.id
	})
	firstIndex := slices.IndexFunc(successor.Tabs, func(tab documentSnapshot) bool { return tab.ID == first.id })
	target := &successor.Tabs[firstIndex]
	target.Undo = &clearUndo{Text: target.Text, Result: target.Result, Source: target.Source,
		Append: target.Append, Live: target.Live, TranscriptionRun: target.TranscriptionRun}
	target.Text, target.Source, target.Result = text, "", nil
	target.Append, target.Live, target.TranscriptionRun = nil, nil, ""
	target.Retry, target.ScreenshotFiles = false, screenshots
	// A combined transcript is a new draft; historical sent pairs remain.
	if count := len(target.PromptSteps); count > 0 {
		last := &target.PromptSteps[count-1]
		if !last.Sent && !last.Processed {
			last.Prompt, last.Response = text, target.ProcessedText
		} else {
			target.PromptSteps = append(target.PromptSteps, promptStep{
				ID: last.ID + 1, Conversation: last.Conversation, Prompt: text, Response: target.ProcessedText,
			})
		}
	}
	// A failed write leaves the original widgets and task generations intact.
	closedEntries, closedArchive := d.prepareClosure(closedEntry)
	successor.ClosedTabs = closedArchive
	if err := d.writeWorkspace(successor); err != nil {
		return fmt.Errorf("não foi possível salvar o texto combinado; os originais foram mantidos: %w", err)
	}
	d.stopPlayback()
	for _, source := range sources {
		doc := source.document
		d.invalidateDocumentJob(doc)
		d.invalidateProcessing(doc)
		if doc.live != nil {
			d.cancelLiveRecording(doc.live)
		}
		if doc != first {
			doc.closed = true
		}
	}
	d.restoreDocumentSnapshot(first, *target)
	first.sourceLabel.SetText("Textos combinados das abas")
	first.view.status = "Textos na primeira aba do grupo. As outras abas desse grupo foram fechadas."
	d.documents = slices.DeleteFunc(d.documents, func(doc *document) bool { return doc.closed })
	d.publishRestoredDocuments(selected)
	d.publishClosedDocuments(closedEntries, closedEntry.until)
	for _, source := range sources {
		if err := d.removeDocumentLatest(source.document); err != nil {
			d.showError(err.Error(), "")
		}
	}
	return nil
}
