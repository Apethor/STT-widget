package desktop

import (
	"maps"
	"slices"
	"time"
)

// Entries share the renewed deadline of a closure sequence. Restoring content
// never restores cancelled task generations.
type closedDocument struct {
	document             *document
	index                int
	until                time.Time
	group                uint64
	replacement          *document
	replacementProtected bool
	groupIndex           int
	groupName            string
	groupAutomaticName   string
	groupNameWidth       float32
	groupRemoved         bool
	combined             *combinedDocuments
}

type combinedDocuments struct {
	documents []*document
	states    []documentSnapshot
	label     string
}

func (d *desktop) syncClosedDocuments() {
	d.closedTab = nil
	if len(d.closedTabs) == 0 {
		d.undoCloseButton.hint.hide()
		d.undoCloseButton.Hide()
	} else {
		d.closedTab = d.closedTabs[len(d.closedTabs)-1]
		hint := "Desfazer fechamento · " + d.closedTab.document.item.Text
		if d.closedTab.combined != nil {
			hint = "Desfazer " + d.closedTab.combined.label
		}
		d.undoCloseButton.setHint(hint)
		d.undoCloseButton.Show()
	}
	d.tabs.Refresh()
}

func (d *desktop) forgetClosedDocument() {
	d.closedTabs = nil
	d.syncClosedDocuments()
}

func (d *desktop) forgetClosedGroup(group uint64) {
	d.closedTabs = slices.DeleteFunc(d.closedTabs, func(entry *closedDocument) bool {
		return closedDocumentInGroup(entry, group)
	})
	d.syncClosedDocuments()
}

func (d *desktop) expireClosedDocument() {
	if d.closedTab != nil && !d.deps.now().Before(d.closedTab.until) {
		d.forgetClosedDocument()
	}
}

func (d *desktop) unusedReplacement(doc *document) bool {
	if doc == nil || doc.closed || d.captureDocument == doc && d.capture != captureIdle {
		return false
	}
	return doc.generation == 1 && doc.processGeneration == 0 && doc.name == "" &&
		doc.editor.Text == "" && doc.processEditor.Text == "" && doc.undo == nil &&
		doc.lastAudio == "" && doc.lastResult == nil && doc.processResult == nil &&
		!doc.processCollapsed && !doc.transcriptCollapsed && doc.presentationEpoch == 0
}

func snapshotGroupInsertion(snapshot workspaceSnapshot, group uint64, position int) int {
	groupIndex := slices.Index(snapshot.Groups, group)
	count := 0
	for index, tab := range snapshot.Tabs {
		if slices.Index(snapshot.Groups, tab.Group) > groupIndex {
			return index
		}
		if tab.Group == group {
			if count >= position {
				return index
			}
			count++
		}
	}
	return len(snapshot.Tabs)
}

func (d *desktop) undoCloseDocument() {
	d.expireClosedDocument()
	closed := d.closedTab
	if closed == nil {
		return
	}
	if closed.combined != nil {
		d.undoCombinedDocuments(closed)
		return
	}
	d.rememberPanelSizes(closed.replacement)
	removeReplacement := !closed.replacementProtected && d.unusedReplacement(closed.replacement)
	if len(d.documents) >= maxDocuments && !removeReplacement {
		d.status.SetText("Limite de 32 abas. Não foi possível restaurar esta aba.")
		return
	}
	successor := d.snapshotWorkspace()
	successor.Groups = slices.Clone(successor.Groups)
	successor.GroupNames = maps.Clone(successor.GroupNames)
	successor.GroupAutomaticNames = maps.Clone(successor.GroupAutomaticNames)
	successor.GroupNameWidths = maps.Clone(successor.GroupNameWidths)
	if successor.GroupNames == nil {
		successor.GroupNames = make(map[uint64]string)
	}
	if successor.GroupAutomaticNames == nil {
		successor.GroupAutomaticNames = make(map[uint64]string)
	}
	if successor.GroupNameWidths == nil {
		successor.GroupNameWidths = make(map[uint64]float32)
	}
	doc := closed.document
	if !slices.Contains(successor.Groups, doc.group) {
		successor.Groups = slices.Insert(successor.Groups, min(closed.groupIndex, len(successor.Groups)), doc.group)
		successor.GroupNames[doc.group] = closed.groupName
		if closed.groupAutomaticName != "" {
			successor.GroupAutomaticNames[doc.group] = closed.groupAutomaticName
		}
		if closed.groupNameWidth > 0 {
			successor.GroupNameWidths[doc.group] = closed.groupNameWidth
		}
	}
	if removeReplacement {
		successor.Tabs = slices.DeleteFunc(successor.Tabs, func(tab documentSnapshot) bool {
			return tab.ID == closed.replacement.id
		})
	}
	index := snapshotGroupInsertion(successor, doc.group, closed.index)
	successor.Tabs = slices.Insert(successor.Tabs, index, d.snapshotDocument(doc))
	successor.Selected = doc.id
	successor.ClosedTabs = d.archiveClosedDocuments(d.closedTabs[:len(d.closedTabs)-1], time.Time{})
	if err := d.writeWorkspace(successor); err != nil {
		d.showError("Não foi possível restaurar a aba; Desfazer continua disponível: "+err.Error(), "")
		return
	}
	d.cancelMergeForGroup(doc.group, "As abas do grupo mudaram. Clique em Mesclar para usar os textos atuais.")
	d.hints.hide()
	if removeReplacement {
		closed.replacement.closed = true
		d.documents = slices.DeleteFunc(d.documents, func(current *document) bool { return current == closed.replacement })
	}
	d.groups, d.groupNames, d.groupNameWidths = successor.Groups, successor.GroupNames, successor.GroupNameWidths
	d.groupAutomaticNames = successor.GroupAutomaticNames
	doc.closed = false
	d.arrangeDocumentEditors(doc)
	d.documents = slices.Insert(d.documents, index, doc)
	d.publishRestoredDocuments(doc)
	d.closedTabs = d.closedTabs[:len(d.closedTabs)-1]
	d.syncClosedDocuments()
}

func (d *desktop) publishRestoredDocuments(selected *document) {
	d.selectingTabs = true
	d.tabs.SetItems(documentItems(d.documents))
	d.tabs.Select(selected.item)
	d.selectingTabs = false
	d.selectDocument(selected)
	d.restoreOuterPanelSizes(selected)
	d.renumberDocuments()
	d.renderDocument(selected)
	d.syncControls()
}

func (d *desktop) undoCombinedDocuments(closed *closedDocument) {
	combined := closed.combined
	for _, doc := range combined.documents {
		if d.captureDocument == doc && d.capture != captureIdle {
			d.status.SetText("Pare a gravação dessa aba antes de desfazer.")
			return
		}
	}
	current := d.snapshotWorkspace()
	successor := current
	successor.Tabs = slices.Clone(current.Tabs)
	originalIDs := make(map[uint64]bool, len(combined.states))
	for _, state := range combined.states {
		originalIDs[state.ID] = true
	}
	successor.Tabs = slices.DeleteFunc(successor.Tabs, func(tab documentSnapshot) bool { return originalIDs[tab.ID] })
	if len(successor.Tabs)+len(combined.states) > maxDocuments {
		d.status.SetText("Limite de 32 abas. Feche uma aba antes de desfazer.")
		return
	}
	group := closedDocumentGroup(closed)
	if !slices.Contains(successor.Groups, group) {
		successor.Groups = slices.Insert(slices.Clone(successor.Groups), min(closed.groupIndex, len(successor.Groups)), group)
		successor.GroupNames = maps.Clone(successor.GroupNames)
		if successor.GroupNames == nil {
			successor.GroupNames = make(map[uint64]string)
		}
		successor.GroupNames[group] = closed.groupName
		successor.GroupAutomaticNames = maps.Clone(successor.GroupAutomaticNames)
		if closed.groupAutomaticName != "" {
			successor.GroupAutomaticNames[group] = closed.groupAutomaticName
		}
		successor.GroupNameWidths = maps.Clone(successor.GroupNameWidths)
		if successor.GroupNameWidths == nil {
			successor.GroupNameWidths = make(map[uint64]float32)
		}
		if closed.groupNameWidth > 0 {
			successor.GroupNameWidths[group] = closed.groupNameWidth
		}
	}
	index := snapshotGroupInsertion(successor, group, closed.index)
	successor.Tabs = slices.Insert(successor.Tabs, index, combined.states...)
	populated := make(map[uint64]bool)
	for _, tab := range successor.Tabs {
		populated[tab.Group] = true
	}
	successor.Groups = slices.DeleteFunc(slices.Clone(successor.Groups), func(group uint64) bool {
		return !populated[group]
	})
	successor.GroupNames = maps.Clone(successor.GroupNames)
	successor.GroupAutomaticNames = maps.Clone(successor.GroupAutomaticNames)
	successor.GroupNameWidths = maps.Clone(successor.GroupNameWidths)
	for group := range successor.GroupNames {
		if !populated[group] {
			delete(successor.GroupNames, group)
		}
	}
	for group := range successor.GroupAutomaticNames {
		if !populated[group] {
			delete(successor.GroupAutomaticNames, group)
		}
	}
	for group := range successor.GroupNameWidths {
		if !populated[group] {
			delete(successor.GroupNameWidths, group)
		}
	}
	successor.Selected = combined.documents[0].id
	successor.ClosedTabs = d.archiveClosedDocuments(d.closedTabs[:len(d.closedTabs)-1], time.Time{})
	// Preserve the combined version and later work, including audio/history,
	// before replacing the original group.
	if err := d.archiveRecoveryWorkspace(current); err != nil {
		d.showError("Não foi possível preservar as alterações posteriores: "+err.Error(), "")
		return
	}
	if err := d.writeWorkspace(successor); err != nil {
		d.showError("Não foi possível desfazer; a operação continua recuperável: "+err.Error(), "")
		return
	}
	d.cancelMergeForGroup(group, "Mesclagem cancelada ao restaurar as abas.")
	d.stopPlayback()
	for index, doc := range combined.documents {
		d.invalidateDocumentJob(doc)
		d.invalidateProcessing(doc)
		d.restoreDocumentSnapshot(doc, combined.states[index])
	}
	d.documents = slices.DeleteFunc(d.documents, func(doc *document) bool { return originalIDs[doc.id] })
	d.documents = slices.Insert(d.documents, index, combined.documents...)
	d.groups, d.groupNames, d.groupNameWidths = successor.Groups, successor.GroupNames, successor.GroupNameWidths
	d.groupAutomaticNames = successor.GroupAutomaticNames
	d.publishRestoredDocuments(combined.documents[0])
	d.closedTabs = d.closedTabs[:len(d.closedTabs)-1]
	d.syncClosedDocuments()
}
