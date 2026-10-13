package desktop

import (
	"fmt"
	"slices"
	"time"
)

const closureUndoDuration = 30 * time.Second

// closedTabSnapshot stores data and identities, never running jobs or widgets.
// Until is absolute: loading this archive does not renew the remaining time.
type closedTabSnapshot struct {
	State                documentSnapshot   `json:"state"`
	Until                time.Time          `json:"until"`
	Index                int                `json:"index"`
	Group                uint64             `json:"group"`
	GroupIndex           int                `json:"group_index"`
	GroupName            string             `json:"group_name,omitempty"`
	GroupAutomaticName   string             `json:"group_automatic_name,omitempty"`
	GroupNameWidth       float32            `json:"group_name_width,omitempty"`
	GroupRemoved         bool               `json:"group_removed,omitempty"`
	Replacement          *documentSnapshot  `json:"replacement,omitempty"`
	ReplacementUntouched bool               `json:"replacement_untouched,omitempty"`
	Combined             []documentSnapshot `json:"combined,omitempty"`
	Label                string             `json:"label,omitempty"`
}

func closedDocumentGroup(entry *closedDocument) uint64 {
	if entry.group != 0 {
		return entry.group
	}
	return entry.document.group
}

func preserveClosedGroupName(entry *closedDocument) {
	if entry.groupName == "" {
		entry.groupName = fmt.Sprintf("Grupo %d", closedDocumentGroup(entry))
		entry.groupAutomaticName = entry.groupName
	}
}

func closedDocumentInGroup(entry *closedDocument, group uint64) bool {
	return entry.document.group == group || closedDocumentGroup(entry) == group
}

func closedSnapshotInGroup(entry closedTabSnapshot, group uint64) bool {
	return entry.State.Group == group || entry.Group == group
}

func (d *desktop) archiveClosedDocuments(entries []*closedDocument, renewed time.Time) []closedTabSnapshot {
	var archive []closedTabSnapshot
	now := d.deps.now()
	for _, entry := range entries {
		if renewed.IsZero() && !now.Before(entry.until) {
			continue
		}
		until := entry.until
		if !renewed.IsZero() {
			until = renewed
		}
		state := closedTabSnapshot{
			State: d.snapshotDocument(entry.document), Until: until.UTC(), Index: entry.index,
			Group: closedDocumentGroup(entry), GroupIndex: entry.groupIndex, GroupName: entry.groupName,
			GroupAutomaticName: entry.groupAutomaticName, GroupNameWidth: entry.groupNameWidth,
			GroupRemoved: entry.groupRemoved,
		}
		if entry.replacement != nil {
			replacement := d.snapshotDocument(entry.replacement)
			state.Replacement = &replacement
			state.ReplacementUntouched = !entry.replacementProtected && d.unusedReplacement(entry.replacement)
		}
		if entry.combined != nil {
			state.Combined = entry.combined.states
			state.Label = entry.combined.label
		}
		archive = append(archive, state)
	}
	return archive
}

// prepareClosure makes the queue part of the same atomic write as the visible
// tabs. Failed saves do not consume or renew the in-memory recovery entries.
func (d *desktop) prepareClosure(entry *closedDocument) ([]*closedDocument, []closedTabSnapshot) {
	preserveClosedGroupName(entry)
	now := d.deps.now()
	entries := slices.DeleteFunc(slices.Clone(d.closedTabs), func(previous *closedDocument) bool {
		return !now.Before(previous.until)
	})
	entry.until = now.Add(closureUndoDuration)
	entries = append(entries, entry)
	return entries, d.archiveClosedDocuments(entries, entry.until)
}

func (d *desktop) prepareGroupClosure(group uint64) ([]*closedDocument, []closedTabSnapshot, time.Time) {
	now := d.deps.now()
	entries := slices.DeleteFunc(slices.Clone(d.closedTabs), func(entry *closedDocument) bool {
		return closedDocumentInGroup(entry, group) || !now.Before(entry.until)
	})
	until := now.Add(closureUndoDuration)
	return entries, d.archiveClosedDocuments(entries, until), until
}

func (d *desktop) publishClosedDocuments(entries []*closedDocument, until time.Time) {
	for _, entry := range entries {
		entry.until = until
	}
	d.closedTabs = entries
	d.syncClosedDocuments()
}

func (d *desktop) validClosedDocuments(snapshot *workspaceSnapshot) bool {
	active := make(map[uint64]bool, len(snapshot.Tabs))
	for _, tab := range snapshot.Tabs {
		active[tab.ID] = true
	}
	validState := func(tab *documentSnapshot) bool {
		tab.Options = tab.Options.WithLegacyMerge(d.settings.Merge)
		return tab.ID != 0 && tab.ID <= snapshot.NextID && tab.Group != 0 && tab.Group <= snapshot.NextGroup &&
			tab.Options.Validate() == nil && d.validLiveArchive(tab.Live) && validTranscriptionRun(tab.TranscriptionRun) &&
			d.validAudioHistory(tab.AudioHistory) && validPromptSteps(tab.PromptSteps) && tab.PanelSizes.valid() &&
			validEditorZoom(tab.TranscriptZoom) && validEditorZoom(tab.ProcessZoom) && validTranscriptEvaluation(tab.Evaluation) &&
			(tab.Undo == nil || validTranscriptionRun(tab.Undo.TranscriptionRun))
	}
	ordinaryIDs := make(map[uint64]bool)
	for index := range snapshot.ClosedTabs {
		entry := &snapshot.ClosedTabs[index]
		if !d.deps.now().Before(entry.Until) {
			continue
		}
		if entry.Index < 0 || entry.GroupIndex < 0 ||
			entry.Group == 0 || entry.Group > snapshot.NextGroup || !validState(&entry.State) ||
			entry.GroupNameWidth != 0 && (entry.GroupNameWidth < minGroupNameWidth || entry.GroupNameWidth > maxGroupNameWidth) ||
			entry.GroupName != "" && validateGroupName(entry.GroupName) != nil ||
			entry.GroupAutomaticName != "" &&
				(entry.GroupAutomaticName != entry.GroupName || !automaticGroupName(entry.GroupAutomaticName)) ||
			len(entry.Combined) == 0 && (active[entry.State.ID] || ordinaryIDs[entry.State.ID]) {
			return false
		}
		if len(entry.Combined) == 0 {
			ordinaryIDs[entry.State.ID] = true
		}
		if entry.Replacement != nil && !validState(entry.Replacement) {
			return false
		}
		seen := make(map[uint64]bool)
		for position := range entry.Combined {
			tab := &entry.Combined[position]
			if !validState(tab) || seen[tab.ID] || tab.Group != entry.Group {
				return false
			}
			seen[tab.ID] = true
		}
		if len(entry.Combined) > 0 && entry.Combined[0].ID != entry.State.ID {
			return false
		}
	}
	return true
}

func (d *desktop) restoreClosedDocuments(archive []closedTabSnapshot) {
	now := d.deps.now()
	archive = slices.DeleteFunc(slices.Clone(archive), func(entry closedTabSnapshot) bool {
		return !now.Before(entry.Until)
	})
	byID := make(map[uint64]*document)
	for _, doc := range d.documents {
		byID[doc.id] = doc
	}
	collect := func(state documentSnapshot) *document {
		if doc := byID[state.ID]; doc != nil {
			return doc
		}
		doc := d.createDocumentInGroup(state.Group)
		doc.id = state.ID
		d.restoreDocumentSnapshot(doc, state)
		doc.item.Text = state.Name
		doc.closed = true
		d.documents = slices.DeleteFunc(d.documents, func(current *document) bool { return current == doc })
		byID[state.ID] = doc
		return doc
	}
	// A tab may occur in several closures/combined snapshots. The latest state
	// wins, while original combined states remain separate until explicit Undo.
	for index := len(archive) - 1; index >= 0; index-- {
		collect(archive[index].State)
	}
	for _, entry := range archive {
		if entry.Replacement != nil {
			collect(*entry.Replacement)
		}
		for _, state := range entry.Combined {
			collect(state)
		}
	}
	d.closedTabs = nil
	for _, state := range archive {
		entry := &closedDocument{
			document: byID[state.State.ID], index: state.Index, until: state.Until,
			group: state.Group, groupIndex: state.GroupIndex, groupName: state.GroupName,
			groupAutomaticName: state.GroupAutomaticName, groupNameWidth: state.GroupNameWidth,
			groupRemoved: state.GroupRemoved, replacementProtected: !state.ReplacementUntouched,
		}
		if state.Replacement != nil {
			entry.replacement = byID[state.Replacement.ID]
		}
		preserveClosedGroupName(entry)
		if len(state.Combined) > 0 {
			entry.combined = &combinedDocuments{states: state.Combined, label: state.Label}
			for _, original := range state.Combined {
				entry.combined.documents = append(entry.combined.documents, byID[original.ID])
			}
		}
		d.closedTabs = append(d.closedTabs, entry)
	}
	d.syncClosedDocuments()
}
