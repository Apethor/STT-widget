package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ai-talker/internal/recorder"
	"ai-talker/internal/store"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
)

func TestGroupNameUsesNaturalWidthAndKeepsAnIndependentPersistentCap(t *testing.T) {
	h := newHarness(t, nil, nil)
	first := h.d.document
	other := h.d.addGroup()
	if err := h.d.renameGroup(first.group, "A"); err != nil {
		t.Fatal(err)
	}
	row := h.d.tabs.renderer.rows[0]
	h.d.window.Content().Refresh()
	shortWidth := row.label.Size().Width
	if shortWidth >= 50 || row.scroll.Position().X > shortWidth+15 {
		t.Fatal("um nome curto continuou reservando uma faixa fixa", shortWidth, row.scroll.Position())
	}
	if err := h.d.renameGroup(first.group, strings.Repeat("W", 40)); err != nil {
		t.Fatal(err)
	}
	if row.label.Size().Width != defaultGroupNameWidth {
		t.Fatal("o nome comprido não respeitou o limite sugerido", row.label.Size(),
			row.box.Size(), row.scroll.Size(), row.actions.Size(),
			h.d.tabs.renderer.header.Size(), h.d.tabs.renderer.groupScroll.Size(),
			h.d.window.Canvas().Size())
	}
	h.d.openGroupNameWidth(first.group)
	form := h.d.groupWidthForm
	assertPopupInCanvas(t, h, form.popup)
	form.slider.SetValue(180)
	if h.d.groupNameWidth(first.group) != 180 || h.d.groupNameWidth(other.group) != defaultGroupNameWidth ||
		row.label.Size().Width != 180 || h.d.document != other {
		t.Fatal("o slider não ajustou somente o limite do grupo de origem")
	}
	form.popup.Hide()
	if !h.d.loadWorkspace() || h.d.groupNameWidth(first.group) != 180 || h.d.groupNameWidth(other.group) != defaultGroupNameWidth {
		t.Fatal("o limite por grupo não foi restaurado do workspace")
	}
	if err := h.d.renameGroup(first.group, "Curto"); err != nil {
		t.Fatal(err)
	}
	if h.d.tabs.renderer.rows[0].label.Size().Width >= 100 || h.d.groupNameWidth(first.group) != 180 {
		t.Fatal("um nome curto não liberou espaço mantendo o limite escolhido")
	}
	for _, object := range test.LaidOutObjects(h.d.tabs.renderer.rows[0].label) {
		if text, ok := object.(*canvas.Text); ok && strings.Contains(text.Text, "…") {
			t.Fatal("o nome curto foi abreviado mesmo ocupando sua largura natural", text.Text)
		}
	}
}

func TestShortGroupNamesRemainCompleteWhenSelectedOrUnselected(t *testing.T) {
	h := newHarness(t, nil, nil)
	first := h.d.document
	second := h.d.addGroup()
	for _, selected := range []*document{first, second, first, second} {
		h.d.tabs.Select(selected.item)
		h.settle()
		for _, row := range h.d.tabs.renderer.rows {
			for _, object := range test.LaidOutObjects(row.label) {
				if text, ok := object.(*canvas.Text); ok && strings.Contains(text.Text, "…") {
					t.Fatal("selecionar o grupo abreviou um nome curto", row.label.Text, text.Text,
						row.label.Size(), row.label.MinSize(), row.box.Size(), row.scroll.Size(),
						h.d.tabs.renderer.header.Size(), h.d.window.Canvas().Size())
				}
			}
		}
	}
}

func TestGroupWidthDragFitsMinimumWindowAndRollsBackFailedSave(t *testing.T) {
	h := newHarness(t, nil, nil)
	group := h.d.group
	if err := h.d.renameGroup(group, strings.Repeat("W", 40)); err != nil {
		t.Fatal(err)
	}
	row := h.d.tabs.renderer.rows[0]
	row.resize.Dragged(&fyne.DragEvent{Dragged: fyne.Delta{DX: 208}})
	row.resize.DragEnd()
	h.d.window.Resize(fyne.NewSize(320, 700))
	h.d.window.Content().Refresh()
	if h.d.groupNameWidth(group) != 320 || h.d.window.Content().MinSize().Width > 320 ||
		row.scroll.Size().Width < 47 || row.actions.Position().X+row.actions.Size().Width > row.box.Size().Width+1 {
		t.Fatal("a largura escolhida ocultou abas/ações ou ampliou a janela", row.box.Size(), row.label.Size())
	}
	row.resize.DoubleTapped(&fyne.PointEvent{})
	if h.d.groupNameWidth(group) != defaultGroupNameWidth {
		t.Fatal("o duplo clique na alça não restaurou a sugestão")
	}
	path := filepath.Join(h.d.directory, "workspace-go-v1.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	row.resize.Dragged(&fyne.DragEvent{Dragged: fyne.Delta{DX: 60}})
	row.resize.DragEnd()
	if h.d.groupNameWidth(group) != defaultGroupNameWidth || !h.d.errorLabel.Visible() {
		t.Fatal("a falha de persistência deixou uma largura falsa")
	}
}

func TestGroupWidthChangeDuringRecordingKeepsAudioAndSurvivesClosingAnotherGroup(t *testing.T) {
	captured := &fakeRecording{state: recorder.Snapshot{Path: "captura.wav"}}
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.newRecorder = func(int, string) recording { return captured }
	})
	first := h.d.document
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	other := h.d.addGroup()
	row := h.d.tabs.renderer.rows[0]
	row.resize.Dragged(&fyne.DragEvent{Dragged: fyne.Delta{DX: 48}})
	row.resize.DragEnd()
	h.d.closeGroup(other.group)
	var workspace workspaceSnapshot
	if err := store.ReadJSON(filepath.Join(h.d.directory, "workspace-go-v1.json"), &workspace); err != nil ||
		!captured.Snapshot().Active || h.d.captureDocument != first ||
		h.d.groupNameWidth(first.group) != 160 || workspace.GroupNameWidths[first.group] != 160 {
		t.Fatal("ajustar o grupo interrompeu a captura ou perdeu seu limite", err)
	}
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureIdle && !first.busy })
}

func TestTabScrollKeepsThinThumbAndTransparentBackgroundOnHover(t *testing.T) {
	h := newHarness(t, nil, nil)
	for range 8 {
		h.d.addDocument()
	}
	row := h.d.tabs.renderer.rows[0]
	if row.buttons.Size().Width <= row.scroll.Size().Width {
		t.Fatal("o cenário não produziu overflow nas abas")
	}
	th := theme.CurrentForWidget(row.scroll)
	before := th.Size(theme.SizeNameScrollBarSmall)
	if before != 3 || th.Size(theme.SizeNameScrollBar) != before {
		t.Fatal("o hover usa uma barra mais grossa")
	}
	_, _, _, alpha := th.Color(theme.ColorNameScrollBarBackground, theme.VariantDark).RGBA()
	if alpha != 0 {
		t.Fatal("o hover ainda desenha uma faixa escura sobre as abas")
	}
}
