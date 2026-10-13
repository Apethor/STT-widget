package desktop

import (
	"strings"

	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
)

func documentHasText(doc *document) bool {
	return strings.TrimSpace(doc.editor.Text) != "" || strings.TrimSpace(doc.processEditor.Text) != ""
}

func (d *desktop) requestCloseDocument(item *container.TabItem) {
	if d.closing || d.tabsDialog || d.discardDialog {
		return
	}
	var doc *document
	for _, candidate := range d.documents {
		if candidate.item == item {
			doc = candidate
			break
		}
	}
	if doc == nil || doc.closed {
		return
	}
	if d.capture != captureIdle && d.captureDocument == doc {
		d.status.SetText("Pare a gravação antes de fechar esta aba.")
		return
	}
	if !documentHasText(doc) {
		d.closeDocument(item)
		return
	}
	d.tabsDialog = true
	d.syncControls()
	complete := func(confirmed bool) {
		d.guard(func() {
			d.tabsDialog = false
			d.syncControls()
			if confirmed {
				d.closeDocument(item)
			}
		})()
	}
	if d.deps.confirmTabs != nil {
		d.deps.confirmTabs(d.window, complete)
		return
	}
	message := "A aba " + item.Text + " não está vazia.\nDeseja realmente fechá-la?\n" +
		"Os textos serão preservados nos rascunhos e as gravações serão mantidas."
	if len(d.groupDocuments(doc.group)) == 1 && len(d.groups) > 1 {
		message += "\nComo é a última aba, " + d.groupName(doc.group) + " também será fechado."
	}
	confirmation := dialog.NewConfirm(uiText("Fechar aba"), message, complete, d.window)
	confirmation.SetConfirmText(uiText("Fechar aba"))
	confirmation.SetDismissText(uiText("Cancelar"))
	confirmation.Show()
}
