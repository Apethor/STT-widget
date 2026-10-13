package desktop

import (
	"os"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
)

func TestTabNumbersFollowOpenOrderWithoutReusingInternalIDs(t *testing.T) {
	h := newHarness(t, nil, nil)
	first := h.d.document
	second := h.d.addDocument()
	third := h.d.addDocument()
	h.d.closeDocument(second.item)
	if first.item.Text != "Aba 1" || third.item.Text != "Aba 2" || third.id == second.id {
		t.Fatal("fechar uma aba não atualizou a numeração visível")
	}
	fourth := h.d.addDocument()
	if fourth.item.Text != "Aba 3" || fourth.id <= third.id || first.id != 1 {
		t.Fatal("o nome automático ainda usa o contador de identidade")
	}
}

func TestRenamedTabsAndAutomaticNumbersSurviveReopen(t *testing.T) {
	h := newHarness(t, nil, nil)
	first := h.d.document
	first.editor.SetText("Texto importante.")
	second := h.d.addDocument()
	second.editor.SetText("Segundo texto importante.")
	if err := h.d.renameDocument(first, "Projeto Mendes"); err != nil {
		t.Fatal(err)
	}
	if err := h.d.renameDocument(second, "Aba 99"); err != nil {
		t.Fatal(err)
	}
	h.d.finish()
	h.settle()
	restored := newHarness(t, nil, nil)
	restored.d.directory = h.d.directory
	if !restored.d.loadWorkspace() || restored.d.documents[0].item.Text != "Projeto Mendes" ||
		restored.d.documents[1].item.Text != "Aba 99" || restored.d.documents[0].editor.Text != "Texto importante." {
		t.Fatal("reabrir perdeu um nome explícito ou os textos")
	}
	next := restored.d.addDocument()
	if next.item.Text != "Aba 3" {
		t.Fatal("o nome explícito confundiu a numeração das novas abas")
	}
}

func TestLegacyTabNumbersAreRenumberedOnRestore(t *testing.T) {
	h := newHarness(t, nil, nil)
	first := h.d.document
	second := h.d.addDocument()
	first.item.Text, second.item.Text = "Aba 5", "Aba 99"
	if err := h.d.saveWorkspace(); err != nil {
		t.Fatal(err)
	}
	if !h.d.loadWorkspace() || h.d.documents[0].item.Text != "Aba 1" || h.d.documents[1].item.Text != "Aba 2" {
		t.Fatal("os nomes automáticos antigos não foram corrigidos")
	}
}

func TestRenameValidationAndWriteFailureKeepOriginalName(t *testing.T) {
	h := newHarness(t, nil, nil)
	doc := h.d.document
	for _, name := range []string{" ", "Nome\nquebrado", "1234567890123456789012345678901234567890123"} {
		if err := h.d.renameDocument(doc, name); err == nil || doc.item.Text != "Aba 1" {
			t.Fatal("um nome inválido foi aceito")
		}
	}
	if err := os.Mkdir(filepath.Join(h.d.directory, "workspace-go-v1.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := h.d.renameDocument(doc, "Novo nome"); err == nil || doc.item.Text != "Aba 1" {
		t.Fatal("a falha ao salvar não restaurou o nome anterior")
	}
	doc.closed = true
	if err := h.d.renameDocument(doc, "Fechada"); err == nil {
		t.Fatal("uma aba fechada foi renomeada")
	}
}

func TestRightClickFindsInactiveTabInScrolledBar(t *testing.T) {
	h := newHarness(t, nil, nil)
	second := h.d.addDocument()
	h.d.addDocument()
	renderer := h.d.tabs.renderer
	target := renderer.headers[1].title
	renderer.scroll.Offset.X = renderer.headers[1].box.Position().X
	renderer.scroll.Refresh()
	var clicked *container.TabItem
	h.d.tabs.onSecondary = func(item *container.TabItem, _ *fyne.PointEvent) { clicked = item }
	test.TapSecondary(target)
	if clicked != second.item {
		t.Fatal("o botão da aba não recebeu o clique direito após rolar a barra")
	}
	test.Tap(target)
	if h.d.document != second {
		t.Fatal("o mesmo cabeçalho não selecionou a aba com o clique normal")
	}
	test.Tap(renderer.headers[1].close)
	if len(h.d.documents) != 2 || h.d.documents[1].item.Text != "Aba 2" {
		t.Fatal("o botão de fechar não preservou as outras abas e sua numeração")
	}
}

func TestTabCreateButtonSelectsNewDocumentAndKeepsNames(t *testing.T) {
	h := newHarness(t, nil, nil)
	before := h.d.document
	h.d.tabs.CreateTab = func() *container.TabItem { return h.d.createDocument().item }
	test.Tap(h.d.tabs.renderer.create)
	if len(h.d.documents) != 2 || h.d.document == before || h.d.document.item.Text != "Aba 2" {
		t.Fatal("o botão + não criou e selecionou a nova aba")
	}
}
