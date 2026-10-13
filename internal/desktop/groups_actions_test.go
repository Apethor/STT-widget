package desktop

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"ai-talker/internal/config"
	"ai-talker/internal/processing"
	"ai-talker/internal/recorder"
	"ai-talker/internal/store"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func TestGroupClearUsesMenuRowAndPreservesSelectedOtherGroup(t *testing.T) {
	var complete func(bool)
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.confirmTabs = func(_ fyne.Window, callback func(bool)) { complete = callback }
	})
	first := h.d.document
	first.editor.SetText("Primeiro grupo, aba um.")
	second := h.d.addDocument()
	second.editor.SetText("Primeiro grupo, aba dois.")
	second.processEditor.SetText("Resultado a arquivar.")
	other := h.d.addGroup()
	other.editor.SetText("Outro grupo independente.")
	other.processEditor.SetText("Outra resposta preservada.")
	if err := h.d.renameGroup(first.group, "Projeto Mendes"); err != nil {
		t.Fatal(err)
	}
	test.Tap(h.d.tabs.renderer.rows[0].more)
	menu, ok := h.d.window.Canvas().Focused().(*widget.PopUpMenu)
	if !ok {
		t.Fatal("os três pontinhos não abriram o menu da linha")
	}
	// Two tabs, a separator, then the group actions. The action belongs to the
	// first row even though a tab in the second row is currently selected.
	for range 5 {
		menu.TypedKey(&fyne.KeyEvent{Name: fyne.KeyDown})
	}
	menu.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	if complete == nil || !h.d.tabsDialog || len(h.d.documents) != 3 {
		t.Fatal("fechar o grupo não pediu confirmação antes de alterar os textos")
	}
	complete(false)
	if second.closed || second.editor.Text != "Primeiro grupo, aba dois." {
		t.Fatal("cancelar apagou ou fechou uma aba")
	}
	h.d.groupMenuFor(first.group).Items[2].Action()
	complete(true)
	if !slices.Equal(h.d.documents, []*document{first, other}) || h.d.document != other ||
		first.editor.Text != "" || first.processEditor.Text != "" || !second.closed ||
		other.editor.Text != "Outro grupo independente." || other.processEditor.Text != "Outra resposta preservada." ||
		h.d.groupName(first.group) != "Projeto Mendes" || len(h.d.groups) != 2 {
		t.Fatal("fechar este grupo alterou outra linha, perdeu seu nome ou mudou a seleção")
	}
	var saved workspaceSnapshot
	if err := store.ReadJSON(filepath.Join(h.d.directory, "workspace-go-v1.json"), &saved); err != nil ||
		saved.Selected != other.id || len(saved.Tabs) != 2 || saved.Tabs[0].Text != "" ||
		saved.Tabs[1].Text != other.editor.Text || saved.GroupNames[first.group] != "Projeto Mendes" {
		t.Fatalf("a limpeza do grupo não foi persistida com o restante da sessão: %v", err)
	}
}

func TestGroupClearConfirmationNamesItsScope(t *testing.T) {
	h := newHarness(t, nil, nil)
	first := h.d.document
	h.d.addGroup()
	if err := h.d.renameGroup(first.group, "Projeto de outubro"); err != nil {
		t.Fatal(err)
	}
	h.d.requestCloseGroupTabs(first.group)
	var text []string
	var cancel *widget.Button
	for _, object := range test.LaidOutObjects(h.d.window.Canvas().Overlays().List()[0]) {
		switch value := object.(type) {
		case *widget.Label:
			text = append(text, value.Text)
		case *widget.Button:
			if value.Text == "Cancelar" {
				cancel = value
			}
		}
	}
	message := strings.Join(text, " ")
	if !strings.Contains(message, "Projeto de outubro") || !strings.Contains(message, "outros grupos") || cancel == nil {
		t.Fatal("a confirmação não explicou qual grupo seria limpo e quais seriam preservados")
	}
	test.Tap(cancel)
}

func TestGroupClearDuringOtherGroupRecordingKeepsCaptureAndAppend(t *testing.T) {
	captured := &fakeRecording{state: recorder.Snapshot{Path: "captura-preservada.wav"}}
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.newRecorder = func(int, string) recording { return captured }
		deps.confirmTabs = func(_ fyne.Window, complete func(bool)) { complete(true) }
	})
	origin := h.d.document
	origin.editor.SetText("Texto anterior da captura.")
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	other := h.d.addGroup()
	other.editor.SetText("Texto de outro grupo.")
	h.d.addDocument().editor.SetText("Outra aba a limpar.")
	if !h.d.groupMenuFor(origin.group).Items[2].Disabled ||
		h.d.groupMenuFor(other.group).Items[2].Disabled || !h.d.groupMenu().Items[5].Disabled {
		t.Fatal("as ações de fechar não respeitaram o grupo da gravação")
	}
	h.d.requestCloseGroupTabs(other.group)
	if !captured.Snapshot().Active || h.d.captureDocument != origin || other.editor.Text != "" ||
		len(h.d.groupDocuments(other.group)) != 1 {
		t.Fatal("limpar outra linha interrompeu a captura ou não limpou seu grupo")
	}
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureIdle && !origin.busy && origin.lastResult != nil })
	if origin.editor.Text != "Texto anterior da captura.\n\nTexto desta gravação." || other.editor.Text != "" {
		t.Fatal("a limpeza de outra linha mudou o destino da transcrição")
	}
}

func TestGroupClearKeepsMergeRunningInAnotherGroup(t *testing.T) {
	started := make(chan context.Context, 1)
	release := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.confirmTabs = func(_ fyne.Window, complete func(bool)) { complete(true) }
		deps.process = func(ctx context.Context, _ config.Settings, _ processing.Request) (processing.Result, error) {
			started <- ctx
			<-release
			return processing.Result{Text: "Mesclagem do segundo grupo."}, nil
		}
	})
	t.Cleanup(unblock)
	first := h.d.document
	first.editor.SetText("Grupo que será limpo.")
	target := h.d.addGroup()
	target.editor.SetText("Parte um.")
	source := h.d.addDocument()
	source.editor.SetText("Parte dois.")
	test.Tap(h.d.mergeButton)
	ctx := waitValue(t, started)
	h.d.requestCloseGroupTabs(first.group)
	if ctx.Err() != nil || !h.d.mergeBusy || first.editor.Text != "" {
		t.Fatal("limpar outro grupo cancelou a mesclagem")
	}
	unblock()
	h.await(func() bool { return !h.d.mergeBusy })
	if target.editor.Text != "Mesclagem do segundo grupo." || !source.closed || first.editor.Text != "" {
		t.Fatal("a mesclagem foi perdida ou alterou o grupo limpo")
	}
}

func TestGroupRenamePersistsAndKeepsCompactRowActions(t *testing.T) {
	h := newHarness(t, nil, nil)
	first := h.d.document
	first.editor.SetText("Texto preservado.")
	h.d.addGroup()
	name := "  Planejamento das entregas de outubro  "
	if err := h.d.renameGroup(first.group, name); err != nil {
		t.Fatal(err)
	}
	h.d.window.Resize(fyne.NewSize(320, 900))
	row := h.d.tabs.renderer.rows[0]
	if row.label.Text != strings.TrimSpace(name) || h.d.window.Content().MinSize().Width > 320 ||
		h.app.Driver().AbsolutePositionForObject(row.more).X+row.more.Size().Width > h.d.window.Canvas().Size().Width {
		t.Fatal("renomear não atualizou a linha ou ocultou os três pontinhos na coluna estreita",
			h.d.window.Content().MinSize(), h.d.recordPanel.MinSize(), first.processPanel.MinSize(),
			h.d.window.Canvas().Size(), row.more.Position(), row.more.Size(),
			h.d.footer.MinSize(), h.d.tabs.MinSize(), h.d.soundboardButton.MinSize(),
			h.d.videoButton.MinSize(), h.d.screenshotButton.MinSize())
	}
	restored := newHarness(t, nil, nil)
	restored.d.directory = h.d.directory
	if !restored.d.loadWorkspace() || restored.d.groupName(first.group) != strings.TrimSpace(name) ||
		restored.d.documents[0].editor.Text != "Texto preservado." || len(restored.d.groups) != 2 {
		t.Fatal("reabrir perdeu o nome do grupo ou sua transcrição")
	}
}

func TestGroupRenameRejectsInvalidNameAndRestoresNameOnWriteFailure(t *testing.T) {
	h := newHarness(t, nil, nil)
	group := h.d.document.group
	for _, name := range []string{"", "  ", "duas\nlinhas", strings.Repeat("á", 41)} {
		if err := h.d.renameGroup(group, name); err == nil {
			t.Fatal("um nome inválido foi aceito")
		}
	}
	if err := h.d.renameGroup(group, "Nome original"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(h.d.directory, "workspace-go-v1.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := h.d.renameGroup(group, "Nome que não foi salvo"); err == nil || h.d.groupName(group) != "Nome original" ||
		h.d.tabs.renderer.rows[0].label.Text != "Nome original" {
		t.Fatal("uma falha de escrita deixou um nome parcial na tela")
	}
}
