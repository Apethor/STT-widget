package desktop

import (
	"slices"
	"strings"

	"fyne.io/fyne/v2/dialog"
)

func (d *desktop) requestCloseTabs() {
	d.confirmCloseTabs(0)
}

func (d *desktop) requestCloseGroupTabs(group uint64) {
	if len(d.groupDocuments(group)) == 0 || !d.canClearGroup(group) {
		return
	}
	d.confirmCloseTabs(group)
}

func (d *desktop) canClearGroup(group uint64) bool {
	return d.capture == captureIdle || (d.captureDocument != nil && d.captureDocument.group != group)
}

// Zero identifies the global action; every other value is a stable group ID.
func (d *desktop) confirmCloseTabs(group uint64) {
	if d.tabsDialog || d.discardDialog ||
		(group == 0 && d.capture != captureIdle) || (group != 0 && !d.canClearGroup(group)) {
		return
	}
	d.tabsDialog = true
	d.syncControls()
	complete := func(confirmed bool) {
		d.guard(func() {
			d.tabsDialog = false
			d.syncControls()
			if confirmed {
				d.clearDocuments(group)
			}
		})()
	}
	if d.deps.confirmTabs != nil {
		d.deps.confirmTabs(d.window, complete)
		return
	}
	title := "Fechar abas de todos os grupos"
	message := "Fechar as abas de TODOS os grupos e limpar os dois editores?\nFicará apenas a primeira aba, vazia, no primeiro grupo."
	if group != 0 {
		title = "Fechar abas de " + d.groupName(group)
		message = "Fechar as abas de " + d.groupName(group) + " e limpar os dois editores?\n" +
			"Ficará a primeira aba vazia nesse grupo. Os outros grupos serão preservados."
	}
	confirmation := dialog.NewConfirm(title, message, complete, d.window)
	confirmation.SetConfirmText(uiText("Fechar e limpar"))
	confirmation.SetDismissText(uiText("Cancelar"))
	confirmation.Show()
}

func (d *desktop) clearAllDocuments() {
	d.clearDocuments(0)
}

func (d *desktop) clearDocuments(group uint64) {
	if (group == 0 && d.capture != captureIdle) || (group != 0 && !d.canClearGroup(group)) {
		d.status.SetText("Pare a gravação desse grupo antes de fechar suas abas.")
		return
	}
	targets := d.documents
	if group != 0 {
		targets = d.groupDocuments(group)
	}
	if len(targets) == 0 {
		return
	}
	if err := d.saveWorkspace(); err != nil {
		d.showError("Não foi possível preservar as abas: "+err.Error(), "")
		return
	}
	// Archive every editor before changing any tab, including generated text
	// that has not yet been edited or exported. A write failure keeps them open.
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
	first, selected := targets[0], d.document
	if err := d.archiveDocumentEvaluations(targets[1:]); err != nil {
		d.showError("Não foi possível preservar as avaliações internas: "+err.Error(), "")
		return
	}
	if group == 0 || selected.group == group {
		selected = first
	}
	if err := d.removeDocumentLatest(first); err != nil {
		d.showError(err.Error(), "")
		return
	}
	if selected == first {
		if err := d.removeLatest(); err != nil {
			d.showError(err.Error(), "")
			return
		}
	}
	// Commit the empty session before changing any visible editor. If the
	// atomic write fails, all originals remain open and in the prior session.
	original := d.snapshotWorkspace()
	empty := original
	empty.ClosedTabs = slices.DeleteFunc(slices.Clone(original.ClosedTabs), func(entry closedTabSnapshot) bool {
		return group == 0 || closedSnapshotInGroup(entry, group)
	})
	empty.Selected = selected.id
	empty.Tabs = nil
	var preservedHistory []audioSnapshot
	remaining := make([]*document, 0, len(d.documents))
	for index, doc := range d.documents {
		if doc == first {
			preservedHistory = original.Tabs[index].AudioHistory
			empty.Tabs = append(empty.Tabs, documentSnapshot{
				ID: first.id, Group: first.group, Name: first.item.Text, Renamed: first.name != "", Options: first.processOptions,
				AudioHistory: preservedHistory,
			})
			remaining = append(remaining, doc)
		} else if group != 0 && doc.group != group {
			empty.Tabs = append(empty.Tabs, original.Tabs[index])
			remaining = append(remaining, doc)
		}
	}
	if group == 0 {
		empty.Groups = []uint64{first.group}
		empty.GroupNames = make(map[uint64]string)
		empty.GroupAutomaticNames = make(map[uint64]string)
		if name := d.groupNames[first.group]; name != "" {
			empty.GroupNames[first.group] = name
			if automatic := d.groupAutomaticNames[first.group]; automatic != "" {
				empty.GroupAutomaticNames[first.group] = automatic
			}
		}
	}
	if err := d.writeWorkspace(empty); err != nil {
		d.showError("Não foi possível salvar a limpeza das abas: "+err.Error(), "")
		return
	}
	if group == 0 {
		d.cancelMerge("Mesclagem cancelada.")
	} else {
		d.cancelMergeForGroup(group, "Mesclagem cancelada.")
	}
	d.hints.hide()
	if group == 0 {
		d.forgetClosedDocument()
	} else {
		d.forgetClosedGroup(group)
	}
	for _, doc := range targets {
		d.invalidateDocumentJob(doc)
		d.invalidateProcessing(doc)
		doc.processHistory = nil
		doc.promptSteps = nil
		doc.promptCursor, doc.nextPrompt, doc.currentConversation = 0, 0, 0
		doc.currentPrompt, doc.currentResponse = "", ""
		doc.historyApplying = false
		doc.closed = doc != first
	}
	first.editor.SetText("")
	first.processEditor.SetText("")
	first.evaluation = nil
	first.lastAudio, first.lastResult, first.processResult, first.undo = "", nil, nil, nil
	first.recordedAppend = nil
	first.audioHistory = preservedHistory
	first.live = nil
	first.view = defaultDocumentView()
	first.view.status = "Abas limpas. Pronto para um novo áudio."
	first.sourceLabel.SetText("Português + inglês · sem traduzir sua fala")
	setLabel(first.transcriptName, "Transcrição", textColor)
	first.errorLabel.Hide()
	first.retryButton.Hide()
	first.processError.Hide()
	first.processStatus.SetText("Opcional · usa o texto desta aba.")
	d.selectingTabs = true
	d.documents = remaining
	d.groups, d.groupNames = empty.Groups, empty.GroupNames
	d.groupAutomaticNames = empty.GroupAutomaticNames
	d.document = selected
	d.tabs.SetItems(documentItems(remaining))
	d.tabs.Select(selected.item)
	d.selectingTabs = false
	d.renumberDocuments()
	if selected == first && d.capture == captureIdle {
		d.wave.reset()
	}
	d.renderDocument(selected)
	d.syncControls()
	if d.columnScroll != nil && selected == first {
		d.columnScroll.ScrollToTop()
	}
}
