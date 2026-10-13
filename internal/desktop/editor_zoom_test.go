package desktop

import (
	"errors"
	"path/filepath"
	"testing"

	"ai-talker/internal/store"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
)

func TestEditorZoomIsIndependentPersistedAndInheritedFromLastActiveTab(t *testing.T) {
	h := newHarness(t, nil, nil)
	active := h.d.document
	active.transcriptZoom, active.processZoom = 150, 75
	h.d.syncEditorZoom(active, true)
	h.d.syncEditorZoom(active, false)
	if err := h.d.saveWorkspace(); err != nil {
		t.Fatal(err)
	}

	transcriptTheme := editorTheme{Theme: h.d.palette, desktop: h.d, document: active, transcript: true}
	processTheme := editorTheme{Theme: h.d.palette, desktop: h.d, document: active}
	if transcriptTheme.Size(theme.SizeNameText) != 21 ||
		processTheme.Size(theme.SizeNameText) != 10.5 {
		t.Fatal("cada editor não aplicou seu zoom independente à fonte")
	}

	created := h.d.addGroup()
	if created.transcriptZoom != 150 || created.processZoom != 75 {
		t.Fatal("a nova aba/grupo não herdou os zooms da última aba ativa")
	}

	var saved workspaceSnapshot
	if err := store.ReadJSON(filepath.Join(h.d.directory, "workspace-go-v1.json"), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Tabs[0].TranscriptZoom != 150 || saved.Tabs[0].ProcessZoom != 75 {
		t.Fatal("o workspace não guardou os zooms independentes da aba")
	}
}

func TestEditorZoomActionsResetPersistAndRecoverFromASaveFailure(t *testing.T) {
	h := newHarness(t, nil, nil)
	doc := h.d.document
	test.Tap(doc.transcriptZoomButton)
	form := doc.transcriptZoomForm
	if form == nil || !form.popup.Visible() {
		t.Fatal("a lupa de Transcrição não abriu o controle de zoom")
	}
	form.slider.SetValue(150)
	h.settle()
	if doc.transcriptZoom != 150 || doc.processZoom != 100 ||
		h.d.editorZoomValue(doc, false) != 100 {
		t.Fatal("a ação visível não manteve zoom independente nos dois editores")
	}
	form.slider.DoubleTapped(&fyne.PointEvent{Position: fyne.NewPos(
		form.slider.Size().Width*0.6, form.slider.Size().Height/2,
	)})
	if doc.transcriptZoom != 100 {
		t.Fatal("o duplo clique na bolinha não restaurou 100%")
	}
	var saved workspaceSnapshot
	if err := store.ReadJSON(filepath.Join(h.d.directory, "workspace-go-v1.json"), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Tabs[0].TranscriptZoom != 100 {
		t.Fatal("a restauração de zoom não foi salva")
	}
	h.d.deps.writeWorkspace = func(string, workspaceSnapshot) error { return errors.New("disco indisponível") }
	form.slider.SetValue(200)
	if doc.transcriptZoom != 100 || form.slider.Value != 100 || form.value.Text != "100%" {
		t.Fatal("a falha de salvamento não restaurou o valor salvo e o controle")
	}
}
