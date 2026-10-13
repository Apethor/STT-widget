package desktop

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"ai-talker/internal/processing"
	"ai-talker/internal/store"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
)

func TestMoveTabAcrossGroupsPreservesDocumentStateAndRemovesEmptyGroup(t *testing.T) {
	h := newHarness(t, nil, nil)
	moving := h.d.document
	moving.editor.SetText("Transcrição preservada.")
	moving.processEditor.SetText("Resultado preservado.")
	moving.processOptions = processing.Options{Mode: "free"}
	secondGroup := h.d.addGroup()
	secondGroup.editor.SetText("Destino.")
	secondGroup.name = "Aula"
	secondGroup.item.Text = "Aula"
	h.d.groupNames[secondGroup.group] = "Aulas"
	if err := h.d.saveWorkspace(); err != nil {
		t.Fatal(err)
	}

	selected := h.d.document
	h.d.moveDocumentTab(moving.item, secondGroup.group, 1)
	if len(h.d.groups) != 1 || h.d.groups[0] != secondGroup.group ||
		!slices.Equal(h.d.documents, []*document{secondGroup, moving}) ||
		!slices.Equal(h.d.tabs.Items, []*container.TabItem{secondGroup.item, moving.item}) ||
		moving.group != secondGroup.group || h.d.document != selected ||
		h.d.tabs.Selected() != selected.item ||
		moving.editor.Text != "Transcrição preservada." ||
		moving.processEditor.Text != "Resultado preservado." ||
		moving.processOptions.Mode != "free" || moving.item.Text != "Aba 2" ||
		h.d.groupName(secondGroup.group) != "Aulas" {
		t.Fatal("mover a aba não preservou seu estado, ordem ou grupo de destino")
	}
	var saved workspaceSnapshot
	if err := store.ReadJSON(filepath.Join(h.d.directory, "workspace-go-v1.json"), &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Groups) != 1 || saved.Groups[0] != secondGroup.group ||
		len(saved.Tabs) != 2 || saved.Tabs[1].ID != moving.id ||
		saved.Tabs[1].Text != "Transcrição preservada." ||
		saved.Tabs[1].ProcessedText != "Resultado preservado." {
		t.Fatal("a nova ordem e o conteúdo da aba não foram persistidos")
	}
}

func TestMoveTabSaveFailureRestoresBothGroupsAndSelection(t *testing.T) {
	h := newHarness(t, nil, func(deps *dependencies) {
	})
	moving := h.d.document
	destination := h.d.addGroup()
	h.d.deps.writeWorkspace = func(_ string, _ workspaceSnapshot) error {
		return errors.New("disco indisponível")
	}
	beforeGroups := slices.Clone(h.d.groups)
	beforeDocuments := slices.Clone(h.d.documents)
	selected := h.d.document

	h.d.moveDocumentTab(moving.item, destination.group, 1)
	if moving.group != beforeGroups[0] || !slices.Equal(h.d.groups, beforeGroups) ||
		!slices.Equal(h.d.documents, beforeDocuments) || h.d.document != selected ||
		h.d.tabs.Selected() != selected.item {
		t.Fatal("a falha de persistência deixou a aba movida ou mudou a seleção")
	}
}

func TestGroupDragShowsInsertionMarkerAndReordersPersistedRows(t *testing.T) {
	h := newHarness(t, nil, nil)
	first := h.d.groups[0]
	h.d.addGroup()
	h.d.addGroup()
	middle := h.d.groups[1]
	last := h.d.groups[2]
	renderer := h.d.tabs.renderer
	driver := fyne.CurrentApp().Driver()
	source := renderer.rows[0].label
	lastRow := renderer.rows[2]
	lastPosition := driver.AbsolutePositionForObject(lastRow.box)
	target := fyne.NewPos(
		lastRow.box.Size().Width-1,
		lastPosition.Y+lastRow.box.Size().Height-1,
	)
	source.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{AbsolutePosition: target}})
	if renderer.groupDropIndex != 3 || !containsCanvasObject(renderer.rowsContent.Objects, renderer.groupMarker) {
		t.Fatal("arrastar o nome do grupo não mostrou o marcador de inserção")
	}
	source.DragEnd()
	if !slices.Equal(h.d.groups, []uint64{middle, last, first}) ||
		!slices.Equal(h.d.tabs.Items, []*container.TabItem{
			firstTabForGroupForTest(h.d, middle), firstTabForGroupForTest(h.d, last), firstTabForGroupForTest(h.d, first),
		}) ||
		renderer.groupDropIndex != -1 || containsCanvasObject(renderer.rowsContent.Objects, renderer.groupMarker) {
		t.Fatal("soltar o grupo não aplicou a nova ordem nem removeu o marcador")
	}
	var saved workspaceSnapshot
	if err := store.ReadJSON(filepath.Join(h.d.directory, "workspace-go-v1.json"), &saved); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(saved.Groups, h.d.groups) {
		t.Fatal("a ordem dos grupos não foi persistida")
	}
}

func firstTabForGroupForTest(d *desktop, group uint64) *container.TabItem {
	for _, doc := range d.documents {
		if doc.group == group {
			return doc.item
		}
	}
	return nil
}

func TestTabDragShowsInsertionMarkerAndMovesIntoTargetGroup(t *testing.T) {
	h := newHarness(t, nil, nil)
	moving := h.d.document
	h.d.addGroup()
	destination := h.d.document
	renderer := h.d.tabs.renderer
	driver := fyne.CurrentApp().Driver()
	var header *tabHeader
	for index := range renderer.headers {
		if renderer.headers[index].item == moving.item {
			header = &renderer.headers[index]
			break
		}
	}
	if header == nil {
		t.Fatal("a aba de origem não apareceu")
	}
	row := renderer.rows[1]
	rowPosition := driver.AbsolutePositionForObject(row.box)
	target := fyne.NewPos(rowPosition.X+row.box.Size().Width-1, rowPosition.Y+row.box.Size().Height/2)
	header.title.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{AbsolutePosition: target}})
	if renderer.tabMarker == nil || renderer.tabMarker.group != destination.group ||
		!containsCanvasObject(row.buttons.Objects, row.marker) {
		t.Fatal("arrastar a aba não mostrou o marcador no grupo de destino")
	}
	header.title.DragEnd()
	if moving.group != destination.group || !slices.Equal(h.d.documents, []*document{destination, moving}) ||
		!slices.Equal(h.d.tabs.Items, []*container.TabItem{destination.item, moving.item}) ||
		len(h.d.groups) != 1 || renderer.tabMarker != nil {
		t.Fatal("soltar a aba não mudou de grupo preservando os documentos")
	}
}

func containsCanvasObject(objects []fyne.CanvasObject, wanted fyne.CanvasObject) bool {
	return slices.ContainsFunc(objects, func(object fyne.CanvasObject) bool {
		return object == wanted
	})
}

func TestGroupMoveInOrderUsesInsertionIndexesAndRejectsUnknownGroups(t *testing.T) {
	groups := []uint64{1, 2, 3, 4}
	tests := []struct {
		name  string
		group uint64
		index int
		want  []uint64
		ok    bool
	}{
		{"começo para o fim", 1, 4, []uint64{2, 3, 4, 1}, true},
		{"fim para o começo", 4, 0, []uint64{4, 1, 2, 3}, true},
		{"posição atual", 2, 2, []uint64{1, 2, 3, 4}, false},
		{"desconhecido", 8, 2, nil, false},
		{"índice inválido", 1, 5, nil, false},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			got, changed := moveGroupInOrder(groups, testCase.group, testCase.index)
			if changed != testCase.ok || !slices.Equal(got, testCase.want) {
				t.Fatalf("moveGroupInOrder = %v, %v; esperado %v, %v", got, changed, testCase.want, testCase.ok)
			}
		})
	}
}

func TestTabDragOutsideRowsClearsTheInsertionMarker(t *testing.T) {
	h := newHarness(t, nil, nil)
	renderer := h.d.tabs.renderer
	header := renderer.headers[0]
	renderer.updateTabDrag(header.item, header.title, &fyne.DragEvent{
		PointEvent: fyne.PointEvent{AbsolutePosition: fyne.NewPos(0, h.d.window.Canvas().Size().Height+20)},
	})
	if renderer.tabMarker != nil {
		t.Fatal("soltar fora de um grupo manteve um destino válido")
	}
	test.Tap(renderer.createGroup)
	if len(h.d.groups) != 2 {
		t.Fatal("o botão lateral de criar grupo deixou de funcionar com o arraste")
	}
}

func TestTabDropOutsideTheWorkspaceCancelsAnExistingTarget(t *testing.T) {
	h := newHarness(t, nil, nil)
	moving := h.d.document
	destination := h.d.addGroup()
	renderer := h.d.tabs.renderer
	source := renderer.headers[0].title
	row := renderer.rows[1]
	position := h.app.Driver().AbsolutePositionForObject(row.box)
	source.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{
		AbsolutePosition: position.Add(fyne.NewPos(row.box.Size().Width/2, row.box.Size().Height/2)),
	}})
	if renderer.tabMarker == nil {
		t.Fatal("o arraste não apresentou um destino inicial válido")
	}
	source.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{
		AbsolutePosition: fyne.NewPos(h.d.window.Canvas().Size().Width+20, position.Y+row.box.Size().Height/2),
	}})
	source.DragEnd()
	if moving.group == destination.group || len(h.d.groups) != 2 {
		t.Fatal("soltar fora da janela moveu a aba para o último destino")
	}
}

func TestGroupNamesRemainWithTheGroupAfterReordering(t *testing.T) {
	h := newHarness(t, nil, nil)
	first := h.d.groups[0]
	second := h.d.addGroup()
	firstName, secondName := h.d.groupName(first), h.d.groupName(second.group)
	h.d.moveGroup(first, 2)
	if h.d.groupName(first) != firstName || h.d.groupName(second.group) != secondName {
		t.Fatal("reordenar grupos trocou seus nomes")
	}
}
