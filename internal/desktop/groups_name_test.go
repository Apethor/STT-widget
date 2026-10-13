package desktop

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func TestGroupRenameUsesRightClickOnItsOwnLabel(t *testing.T) {
	h := newHarness(t, nil, nil)
	first := h.d.document
	first.editor.SetText("Texto do grupo a renomear.")
	other := h.d.addGroup()
	row := h.d.tabs.renderer.rows[0]
	test.TapSecondary(row.label)
	menu, ok := h.d.window.Canvas().Focused().(*widget.PopUpMenu)
	if !ok || h.d.document != other {
		t.Fatal("o clique direito no nome não abriu o menu ou trocou a aba selecionada")
	}
	menu.TypedKey(&fyne.KeyEvent{Name: fyne.KeyDown})
	menu.TypedKey(&fyne.KeyEvent{Name: fyne.KeyDown})
	menu.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	var entry *widget.Entry
	var save *widget.Button
	overlays := h.d.window.Canvas().Overlays().List()
	if len(overlays) != 1 {
		t.Fatal("o menu não abriu o formulário de renomear")
	}
	for _, object := range test.LaidOutObjects(overlays[0]) {
		switch value := object.(type) {
		case *widget.Entry:
			entry = value
		case *widget.Button:
			if value.Text == "Renomear" {
				save = value
			}
		}
	}
	if entry == nil || save == nil || entry.Text != "Grupo 1" {
		t.Fatal("o formulário não pertence ao grupo que recebeu o clique")
	}
	entry.SetText("Projeto Mendes")
	test.Tap(save)
	if h.d.groupName(first.group) != "Projeto Mendes" || h.d.groupName(other.group) != "Grupo 2" ||
		h.d.document != other || first.editor.Text != "Texto do grupo a renomear." {
		t.Fatal("renomear pelo clique direito alterou outra linha ou seus textos")
	}
	if h.d.groupMenuFor(first.group).Items[0].Label != "Renomear grupo…" {
		t.Fatal("renomear não está no mesmo menu dos três pontinhos")
	}
}
