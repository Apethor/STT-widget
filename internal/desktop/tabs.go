package desktop

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

// documentTabs keeps DocTabs' selection API and renders headers with buttons
// that handle both primary and secondary clicks. Native tab buttons consume
// secondary clicks before an enclosing widget can receive them.
type documentTabs struct {
	container.DocTabs
	renderer         *documentTabsRenderer
	onSecondary      func(*container.TabItem, *fyne.PointEvent)
	onGroupSecondary func(uint64, *fyne.PointEvent)
	undoClose        fyne.CanvasObject
	groupMenu        func(uint64) *fyne.Menu
	groupIDs         func() []uint64
	groupFor         func(*container.TabItem) uint64
	groupName        func(uint64) string
	groupNameLimit   func(uint64) float32
	groupNameHandle  func(uint64) *groupWidthHandle
	createInGroup    func(uint64) *container.TabItem
	createGroup      func()
	moveTab          func(*container.TabItem, uint64, int)
	moveGroup        func(uint64, int)
}

func newDocumentTabs() *documentTabs {
	tabs := &documentTabs{}
	tabs.ExtendBaseWidget(tabs)
	return tabs
}

func (t *documentTabs) CreateRenderer() fyne.WidgetRenderer {
	t.renderer = newDocumentTabsRenderer(t)
	return t.renderer
}

func automaticTabName(name string) bool {
	number, ok := strings.CutPrefix(uiSource(name), "Aba ")
	if !ok {
		return false
	}
	value, err := strconv.ParseUint(number, 10, 64)
	return err == nil && value > 0
}

func (d *desktop) renumberDocuments() {
	numbers := make(map[uint64]int)
	for _, doc := range d.documents {
		numbers[doc.group]++
		name := doc.name
		if name == "" {
			name = uiTextf("Aba %d", numbers[doc.group])
		}
		doc.item.Text = name
	}
	d.tabs.Refresh()
	d.syncControls()
}

func (d *desktop) tabContextMenu(item *container.TabItem, event *fyne.PointEvent) {
	for _, doc := range d.documents {
		if doc.item != item {
			continue
		}
		menu := widget.NewPopUpMenu(fyne.NewMenu(uiText("Aba"),
			fyne.NewMenuItem(uiText("Renomear aba…"), d.guard(func() { d.openRenameTab(doc) })),
		), d.window.Canvas())
		menu.ShowAtPosition(event.AbsolutePosition)
		return
	}
}

func (d *desktop) openRenameTab(doc *document) {
	if doc.closed {
		return
	}
	entry := widget.NewEntry()
	entry.SetText(doc.item.Text)
	entry.SetPlaceHolder(uiText("Nome da aba"))
	errorLabel := widget.NewLabel("")
	errorLabel.Importance = widget.DangerImportance
	errorLabel.Wrapping = fyne.TextWrapWord
	errorLabel.Hide()
	var form *dialog.CustomDialog
	save := widget.NewButton(uiText("Renomear"), d.guard(func() {
		if err := d.renameDocument(doc, entry.Text); err != nil {
			errorLabel.SetText(err.Error())
			errorLabel.Show()
			return
		}
		form.Hide()
	}))
	save.Importance = widget.HighImportance
	form = dialog.NewCustom(uiText("Renomear aba"), uiText("Cancelar"),
		container.NewVBox(entry, errorLabel, save), d.window)
	form.Resize(fyne.NewSize(360, 180))
	form.Show()
	d.window.Canvas().Focus(entry)
}

func (d *desktop) renameDocument(doc *document, name string) error {
	if doc.closed {
		return errors.New("esta aba já foi fechada")
	}
	name = strings.TrimSpace(name)
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 40 ||
		strings.ContainsAny(name, "\r\n\t\x00") {
		return errors.New("informe um nome de 1 a 40 caracteres, em uma linha")
	}
	previous := doc.name
	d.cancelMergeForGroup(doc.group, "O nome de uma aba mudou. Clique em Mesclar novamente.")
	doc.name = name
	d.renumberDocuments()
	if err := d.saveWorkspace(); err != nil {
		doc.name = previous
		d.renumberDocuments()
		return fmt.Errorf("não foi possível salvar o nome da aba: %w", err)
	}
	return nil
}
