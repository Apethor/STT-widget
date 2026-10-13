package desktop

import (
	"maps"
	"slices"
	"strings"

	"fyne.io/fyne/v2/dialog"
)

func (d *desktop) requestCloseGroup(group uint64) {
	if d.closing || d.tabsDialog || d.discardDialog ||
		len(d.groupDocuments(group)) == 0 || !d.canClearGroup(group) {
		return
	}
	if !slices.ContainsFunc(d.groupDocuments(group), documentHasText) {
		d.closeGroup(group)
		return
	}
	d.tabsDialog = true
	d.syncControls()
	complete := func(confirmed bool) {
		d.guard(func() {
			d.tabsDialog = false
			d.syncControls()
			if confirmed {
				d.closeGroup(group)
			}
		})()
	}
	if d.deps.confirmTabs != nil {
		d.deps.confirmTabs(d.window, complete)
		return
	}
	message := d.groupName(group) + " contém abas com texto.\nFechar o grupo e todas as suas abas?\n" +
		"Os textos serão preservados nos rascunhos e as gravações serão mantidas."
	if len(d.groups) == 1 {
		message += "\nUm novo grupo vazio será aberto."
	} else {
		message += "\nOs demais grupos serão preservados."
	}
	confirmation := dialog.NewConfirm(uiText("Fechar grupo"), message, complete, d.window)
	confirmation.SetConfirmText(uiText("Fechar grupo"))
	confirmation.SetDismissText(uiText("Cancelar"))
	confirmation.Show()
}

func (d *desktop) closeGroup(group uint64) {
	index := slices.Index(d.groups, group)
	targets := d.groupDocuments(group)
	if index < 0 || len(targets) == 0 {
		return
	}
	if !d.canClearGroup(group) {
		d.status.SetText("Pare a gravação desse grupo antes de fechá-lo.")
		return
	}
	if err := d.saveWorkspace(); err != nil {
		d.showError("Não foi possível preservar as abas: "+err.Error(), "")
		return
	}
	for _, doc := range targets {
		if err := d.saveDocumentDrafts(doc); err != nil {
			d.showError(err.Error(), "")
			return
		}
		if err := d.writeDraft(strings.TrimSpace(doc.editor.Text), "", &doc.draftText, "Texto"); err != nil {
			d.showError(err.Error(), "")
			return
		}
		if err := d.writeDraft(strings.TrimSpace(doc.processEditor.Text), "", &doc.processDraftText, "Processado"); err != nil {
			d.showError(err.Error(), "")
			return
		}
	}
	original := d.snapshotWorkspace()
	closed := original
	closed.Groups = slices.Delete(slices.Clone(original.Groups), index, index+1)
	closed.GroupNames = maps.Clone(original.GroupNames)
	closed.GroupAutomaticNames = maps.Clone(original.GroupAutomaticNames)
	delete(closed.GroupNames, group)
	delete(closed.GroupAutomaticNames, group)
	closed.Tabs = nil
	remaining := make([]*document, 0, len(d.documents)-len(targets))
	for position, doc := range d.documents {
		if doc.group != group {
			remaining = append(remaining, doc)
			closed.Tabs = append(closed.Tabs, original.Tabs[position])
		}
	}
	selected := d.document
	if len(remaining) == 0 {
		closed.NextGroup++
		closed.NextID++
		closed.Groups = []uint64{closed.NextGroup}
		closed.GroupNames[closed.NextGroup] = "Grupo 1"
		closed.GroupAutomaticNames[closed.NextGroup] = "Grupo 1"
		closed.Selected = closed.NextID
		closed.Tabs = []documentSnapshot{{
			ID: closed.NextID, Group: closed.NextGroup, Name: "Aba 1", Options: d.settings.Processing,
		}}
	} else if selected.group == group {
		neighbor := closed.Groups[min(index, len(closed.Groups)-1)]
		for _, doc := range remaining {
			if doc.group == neighbor {
				selected = doc
				break
			}
		}
		closed.Selected = selected.id
	}
	if err := d.archiveDocumentEvaluations(targets); err != nil {
		d.showError("Não foi possível preservar as avaliações internas: "+err.Error(), "")
		return
	}
	closedEntries, closedArchive, renewed := d.prepareGroupClosure(group)
	closed.ClosedTabs = closedArchive
	// Save the complete successor session before cancelling tasks or removing
	// any editor. An archive or atomic-write failure leaves the group open.
	if err := d.writeWorkspace(closed); err != nil {
		d.showError("Não foi possível salvar o fechamento do grupo: "+err.Error(), "")
		return
	}
	d.cancelMergeForGroup(group, "Mesclagem cancelada ao fechar o grupo.")
	d.hints.hide()
	for _, doc := range targets {
		d.invalidateDocumentJob(doc)
		d.invalidateProcessing(doc)
		doc.closed = true
	}
	d.selectingTabs = true
	d.documents = remaining
	d.groups, d.groupNames, d.nextGroup = closed.Groups, closed.GroupNames, closed.NextGroup
	d.groupAutomaticNames = closed.GroupAutomaticNames
	if len(remaining) == 0 {
		selected = d.createDocumentInGroup(closed.NextGroup)
	}
	d.tabs.SetItems(documentItems(d.documents))
	d.tabs.Select(selected.item)
	d.selectingTabs = false
	d.selectDocument(selected)
	d.renumberDocuments()
	d.publishClosedDocuments(closedEntries, renewed)
	d.syncControls()
	if d.capture == captureIdle {
		d.wave.reset()
	}
}
