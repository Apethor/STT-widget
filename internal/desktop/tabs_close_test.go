package desktop

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ai-talker/internal/store"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func tapDocumentClose(t *testing.T, h *testHarness, doc *document) {
	t.Helper()
	for _, header := range h.d.tabs.renderer.headers {
		if header.item == doc.item {
			test.Tap(header.close)
			return
		}
	}
	t.Fatal("a aba não ofereceu o botão de fechar")
}

func TestTabCloseConfirmsOnlyWhenEitherEditorContainsText(t *testing.T) {
	for _, texts := range []struct {
		name, source, processed string
		confirm                 bool
	}{
		{name: "vazia"},
		{name: "espaços", source: " \n\t", processed: "\r\n "},
		{name: "transcrição", source: "Texto da transcrição.", confirm: true},
		{name: "resultado", processed: "Texto processado.", confirm: true},
		{name: "ambos", source: "Transcrição preservada.", processed: "Resultado preservado.", confirm: true},
	} {
		t.Run(texts.name, func(t *testing.T) {
			var complete func(bool)
			h := newHarness(t, nil, func(deps *dependencies) {
				deps.confirmTabs = func(_ fyne.Window, callback func(bool)) { complete = callback }
			})
			target := h.d.document
			target.editor.SetText(texts.source)
			target.processEditor.SetText(texts.processed)
			other := h.d.addDocument()
			h.d.tabs.Select(target.item)
			tapDocumentClose(t, h, target)
			if texts.confirm {
				if complete == nil || !h.d.tabsDialog || target.closed || len(h.d.documents) != 2 {
					t.Fatal("a aba com texto fechou antes da confirmação")
				}
				complete(false)
				if target.closed || h.d.tabsDialog || target.editor.Text != texts.source ||
					target.processEditor.Text != texts.processed {
					t.Fatal("cancelar o fechamento alterou o conteúdo ou manteve os controles bloqueados")
				}
				tapDocumentClose(t, h, target)
				complete(true)
			} else if complete != nil || h.d.tabsDialog {
				t.Fatal("a aba vazia pediu confirmação")
			}
			if !target.closed || h.d.document != other || len(h.d.documents) != 1 || !h.d.undoCloseButton.Visible() {
				t.Fatal("o fechamento não selecionou a aba restante ou não ofereceu desfazer")
			}
		})
	}
}

func TestLastTabConfirmationRemovesGroupAndUndoRestoresNameAndPosition(t *testing.T) {
	now := time.Now()
	h := newHarness(t, nil, func(deps *dependencies) { deps.now = func() time.Time { return now } })
	first := h.d.document
	first.editor.SetText("Outro grupo preservado.")
	target := h.d.addGroup()
	target.processEditor.SetText("Resultado do grupo fechado.")
	if err := h.d.renameGroup(target.group, "Reunião de outubro"); err != nil {
		t.Fatal(err)
	}
	tapDocumentClose(t, h, target)
	overlays := h.d.window.Canvas().Overlays().List()
	if len(overlays) != 1 || target.closed {
		t.Fatal("o fechamento não abriu a confirmação")
	}
	var labels []string
	var confirm, cancel *widget.Button
	for _, object := range test.LaidOutObjects(overlays[0]) {
		switch value := object.(type) {
		case *widget.Label:
			labels = append(labels, value.Text)
		case *widget.Button:
			switch value.Text {
			case "Fechar aba":
				confirm = value
			case "Cancelar":
				cancel = value
			}
		}
	}
	message := strings.Join(labels, " ")
	if !strings.Contains(message, "não está vazia") || !strings.Contains(message, "Reunião de outubro") ||
		!strings.Contains(message, "última aba") || confirm == nil || cancel == nil {
		t.Fatal("a confirmação não explicou o conteúdo nem a remoção do grupo")
	}
	test.Tap(confirm)
	if !target.closed || h.d.document != first || !slices.Equal(h.d.groups, []uint64{first.group}) ||
		len(h.d.tabs.renderer.rows) != 1 || first.editor.Text != "Outro grupo preservado." {
		t.Fatal("fechar a última aba deixou o grupo vazio ou alterou o grupo restante")
	}
	var saved workspaceSnapshot
	if err := store.ReadJSON(filepath.Join(h.d.directory, "workspace-go-v1.json"), &saved); err != nil ||
		!slices.Equal(saved.Groups, []uint64{first.group}) || saved.Selected != first.id || len(saved.Tabs) != 1 {
		t.Fatalf("a remoção do grupo não foi salva: %v", err)
	}
	test.Tap(h.d.undoCloseButton)
	if target.closed || h.d.document != target ||
		!slices.Equal(h.d.groups, []uint64{first.group, target.group}) ||
		h.d.groupName(target.group) != "Reunião de outubro" ||
		target.processEditor.Text != "Resultado do grupo fechado." ||
		h.d.tabs.renderer.rows[1].label.Text != "Reunião de outubro" {
		t.Fatal("desfazer não restaurou o grupo, o nome e o conteúdo da última aba")
	}
}

func TestClosingEmptyLastTabRemovesOnlyItsGroup(t *testing.T) {
	for _, scope := range []struct {
		name       string
		groupIndex int
		selected   bool
	}{
		{name: "primeiro selecionado", groupIndex: 0, selected: true},
		{name: "meio selecionado", groupIndex: 1, selected: true},
		{name: "último selecionado", groupIndex: 2, selected: true},
		{name: "grupo não selecionado", groupIndex: 1},
	} {
		t.Run(scope.name, func(t *testing.T) {
			now := time.Now()
			h := newHarness(t, nil, func(deps *dependencies) {
				deps.now = func() time.Time { return now }
				deps.confirmTabs = func(_ fyne.Window, _ func(bool)) {
					t.Fatal("uma aba vazia pediu confirmação")
				}
			})
			first := h.d.document
			middle := h.d.addGroup()
			last := h.d.addGroup()
			original := []*document{first, middle, last}
			target := original[scope.groupIndex]
			expectedGroups := slices.Delete(slices.Clone(h.d.groups), scope.groupIndex, scope.groupIndex+1)
			selected := original[0]
			if scope.selected {
				selected = target
			}
			h.d.tabs.Select(selected.item)
			h.d.tabs.CloseIntercept(target.item)
			if scope.selected {
				next := expectedGroups[min(scope.groupIndex, len(expectedGroups)-1)]
				for _, doc := range original {
					if doc.group == next {
						selected = doc
					}
				}
			}
			if !target.closed || !slices.Equal(h.d.groups, expectedGroups) ||
				len(h.d.documents) != 2 || h.d.document != selected {
				t.Fatal("fechar a última aba manteve uma linha vazia ou alterou a seleção incorretamente")
			}
			test.Tap(h.d.undoCloseButton)
			if !slices.Equal(h.d.documents, original) || h.d.document != target || target.closed {
				t.Fatal("desfazer não recolocou o grupo na posição original")
			}
		})
	}
}

func TestClosingOnlyEmptyTabKeepsOneEmptyTabAvailable(t *testing.T) {
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.confirmTabs = func(_ fyne.Window, _ func(bool)) { t.Fatal("uma aba vazia pediu confirmação") }
	})
	target := h.d.document
	tapDocumentClose(t, h, target)
	if !target.closed || h.d.document == target || len(h.d.groups) != 1 ||
		len(h.d.documents) != 1 || h.d.editor.Text != "" || h.d.processEditor.Text != "" {
		t.Fatal("fechar a única aba deixou o aplicativo sem uma aba para gravar")
	}
}

func TestTabCloseWriteFailureKeepsGroupAndContent(t *testing.T) {
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.confirmTabs = func(_ fyne.Window, complete func(bool)) { complete(true) }
	})
	first := h.d.document
	target := h.d.addGroup()
	target.editor.SetText("Texto preservado quando salvar falha.")
	path := filepath.Join(h.d.directory, "workspace-go-v1.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	tapDocumentClose(t, h, target)
	if target.closed || h.d.document != target || len(h.d.documents) != 2 ||
		!slices.Equal(h.d.groups, []uint64{first.group, target.group}) ||
		target.editor.Text != "Texto preservado quando salvar falha." ||
		h.d.closedTab != nil || !target.errorLabel.Visible() || h.d.tabsDialog {
		t.Fatal("uma falha ao salvar fechou a aba, removeu o grupo ou perdeu o conteúdo")
	}
}

func TestEmptyGroupClosesWithoutConfirmation(t *testing.T) {
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.confirmTabs = func(_ fyne.Window, _ func(bool)) { t.Fatal("um grupo vazio pediu confirmação") }
	})
	first := h.d.document
	target := h.d.addGroup()
	second := h.d.addDocument()
	h.d.requestCloseGroup(target.group)
	if !target.closed || !second.closed || h.d.document != first ||
		!slices.Equal(h.d.groups, []uint64{first.group}) {
		t.Fatal("fechar o grupo vazio não removeu sua linha e suas abas")
	}
}
