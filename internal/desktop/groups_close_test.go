package desktop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"ai-talker/internal/config"
	"ai-talker/internal/processing"
	"ai-talker/internal/recorder"
	"ai-talker/internal/store"
	"ai-talker/internal/transcript"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func TestCloseGroupUsesItsRowArchivesAndPersistsOtherGroups(t *testing.T) {
	var complete func(bool)
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.confirmTabs = func(_ fyne.Window, callback func(bool)) { complete = callback }
	})
	first := h.d.document
	first.editor.SetText("Transcrição da primeira aba.")
	first.processEditor.SetText("Resposta da primeira aba.")
	second := h.d.addDocument()
	second.editor.SetText("Transcrição da segunda aba.")
	second.processEditor.SetText("Resposta da segunda aba.")
	other := h.d.addGroup()
	other.editor.SetText("Texto do grupo preservado.")
	other.processEditor.SetText("Resposta do grupo preservado.")
	if err := h.d.renameGroup(first.group, "Grupo a fechar"); err != nil {
		t.Fatal(err)
	}
	if err := h.d.renameGroup(other.group, "Grupo preservado"); err != nil {
		t.Fatal(err)
	}
	audio := filepath.Join(h.d.directory, "gravacao.wav")
	export := filepath.Join(h.d.directory, "exportacao.txt")
	for path, content := range map[string]string{audio: "áudio preservado", export: "exportação preservada"} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	test.Tap(h.d.tabs.renderer.rows[0].more)
	menu, ok := h.d.window.Canvas().Focused().(*widget.PopUpMenu)
	if !ok {
		t.Fatal("os três pontinhos não abriram o menu da linha")
	}
	// The two tabs are followed by Renomear grupo and Fechar grupo.
	for range 4 {
		menu.TypedKey(&fyne.KeyEvent{Name: fyne.KeyDown})
	}
	menu.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	if complete == nil || !h.d.tabsDialog || len(h.d.groups) != 2 {
		t.Fatal("fechar grupo não pediu confirmação antes de remover a linha")
	}
	complete(false)
	if first.closed || second.closed || first.editor.Text != "Transcrição da primeira aba." {
		t.Fatal("cancelar fechou ou apagou o grupo")
	}
	h.d.groupMenuFor(first.group).Items[1].Action()
	complete(true)
	if !slices.Equal(h.d.groups, []uint64{other.group}) || !slices.Equal(h.d.documents, []*document{other}) ||
		h.d.document != other || !first.closed || !second.closed ||
		other.editor.Text != "Texto do grupo preservado." || other.processEditor.Text != "Resposta do grupo preservado." ||
		len(h.d.tabs.renderer.rows) != 1 || h.d.tabs.renderer.rows[0].label.Text != "Grupo preservado" {
		t.Fatal("fechar grupo manteve sua linha ou alterou o grupo preservado")
	}
	if _, exists := h.d.groupNames[first.group]; exists {
		t.Fatal("o nome do grupo fechado permaneceu na sessão")
	}
	requireFile(t, audio, "áudio preservado")
	requireFile(t, export, "exportação preservada")
	drafts, err := filepath.Glob(filepath.Join(h.d.directory, "rascunhos", "*.txt"))
	if err != nil || len(drafts) != 4 {
		t.Fatalf("os quatro textos do grupo fechado não foram arquivados: %v, %v", drafts, err)
	}
	var saved workspaceSnapshot
	if err := store.ReadJSON(filepath.Join(h.d.directory, "workspace-go-v1.json"), &saved); err != nil ||
		saved.Selected != other.id || len(saved.Tabs) != 1 || len(saved.Groups) != 1 ||
		saved.Tabs[0].Text != other.editor.Text || saved.GroupNames[other.group] != "Grupo preservado" {
		t.Fatalf("o fechamento não foi salvo com o restante da sessão: %v", err)
	}
	restored := newHarness(t, nil, nil)
	restored.d.directory = h.d.directory
	if !restored.d.loadWorkspace() || len(restored.d.groups) != 1 || restored.d.id != other.id ||
		restored.d.editor.Text != other.editor.Text {
		t.Fatal("reabrir restaurou o grupo fechado ou perdeu o grupo preservado")
	}
}

func TestCloseGroupConfirmationNamesItsScope(t *testing.T) {
	h := newHarness(t, nil, nil)
	group := h.d.document.group
	h.d.editor.SetText("Texto que exige confirmação.")
	h.d.addGroup()
	if err := h.d.renameGroup(group, "Projeto de outubro"); err != nil {
		t.Fatal(err)
	}
	h.d.requestCloseGroup(group)
	var labels []string
	var cancel, confirm *widget.Button
	for _, object := range test.LaidOutObjects(h.d.window.Canvas().Overlays().List()[0]) {
		switch value := object.(type) {
		case *widget.Label:
			labels = append(labels, value.Text)
		case *widget.Button:
			switch value.Text {
			case "Cancelar":
				cancel = value
			case "Fechar grupo":
				confirm = value
			}
		}
	}
	message := strings.Join(labels, " ")
	if !strings.Contains(message, "Projeto de outubro") || !strings.Contains(message, "demais grupos") ||
		cancel == nil || confirm == nil {
		t.Fatal("a confirmação não identificou o grupo nem ofereceu cancelar")
	}
	test.Tap(cancel)
	if h.d.tabsDialog || len(h.d.groups) != 2 {
		t.Fatal("cancelar não liberou os controles ou removeu o grupo")
	}
}

func TestCloseLastGroupOpensAndPersistsNewEmptyGroup(t *testing.T) {
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.confirmTabs = func(_ fyne.Window, complete func(bool)) { complete(true) }
	})
	first := h.d.document
	first.editor.SetText("Última transcrição.")
	first.processEditor.SetText("Última resposta.")
	if err := h.d.renameGroup(first.group, "Último projeto"); err != nil {
		t.Fatal(err)
	}
	h.d.requestCloseGroup(first.group)
	replacement := h.d.document
	if !first.closed || replacement == first || replacement.group == first.group ||
		len(h.d.groups) != 1 || len(h.d.documents) != 1 || len(h.d.groupNames) != 1 ||
		replacement.editor.Text != "" || replacement.processEditor.Text != "" ||
		h.d.groupNames[replacement.group] != "Grupo 1" ||
		h.d.tabs.renderer.rows[0].label.Text != "Grupo 1" || replacement.item.Text != "Aba 1" {
		t.Fatal("fechar o último grupo não abriu um novo grupo vazio")
	}
	var saved workspaceSnapshot
	if err := store.ReadJSON(filepath.Join(h.d.directory, "workspace-go-v1.json"), &saved); err != nil ||
		saved.Selected != replacement.id || saved.NextID != replacement.id ||
		len(saved.Tabs) != 1 || saved.Tabs[0].Group != replacement.group || saved.Tabs[0].Text != "" ||
		saved.GroupNames[replacement.group] != "Grupo 1" {
		t.Fatalf("o novo grupo vazio não foi persistido: %v", err)
	}
	restored := newHarness(t, nil, nil)
	restored.d.directory = h.d.directory
	if !restored.d.loadWorkspace() || restored.d.id != replacement.id || restored.d.editor.Text != "" ||
		restored.d.document.group != replacement.group || restored.d.groupName(replacement.group) != "Grupo 1" {
		t.Fatal("reabrir recuperou textos ou identidade do grupo fechado")
	}
}

func TestCloseGroupWriteFailuresKeepOriginalGroupsAndEditors(t *testing.T) {
	for _, name := range []string{"rascunhos indisponíveis", "sessão indisponível"} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, nil, func(deps *dependencies) {
				deps.confirmTabs = func(_ fyne.Window, complete func(bool)) { complete(true) }
			})
			first := h.d.document
			first.editor.SetText("Transcrição que deve continuar aberta.")
			first.processEditor.SetText("Resposta que deve continuar aberta.")
			other := h.d.addGroup()
			groups := slices.Clone(h.d.groups)
			if name == "rascunhos indisponíveis" {
				if err := os.WriteFile(filepath.Join(h.d.directory, "rascunhos"), []byte("arquivo"), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				path := filepath.Join(h.d.directory, "workspace-go-v1.json")
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			h.d.requestCloseGroup(first.group)
			if !slices.Equal(h.d.groups, groups) || !slices.Equal(h.d.documents, []*document{first, other}) ||
				first.closed || h.d.document != other || first.editor.Text != "Transcrição que deve continuar aberta." ||
				first.processEditor.Text != "Resposta que deve continuar aberta." || !h.d.errorLabel.Visible() {
				t.Fatal("uma falha de escrita removeu a linha, apagou textos ou não mostrou o erro")
			}
		})
	}
}

func TestCloseGroupPreservesRecordingInAnotherGroup(t *testing.T) {
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
	other.editor.SetText("Texto do grupo a fechar.")
	if !h.d.groupMenuFor(origin.group).Items[1].Disabled || h.d.groupMenuFor(other.group).Items[1].Disabled {
		t.Fatal("fechar grupo não respeitou a origem da gravação")
	}
	h.d.requestCloseGroup(origin.group)
	if origin.closed || !captured.Snapshot().Active {
		t.Fatal("o grupo da gravação foi fechado")
	}
	h.d.requestCloseGroup(other.group)
	if !other.closed || !captured.Snapshot().Active || h.d.captureDocument != origin || len(h.d.groups) != 1 {
		t.Fatal("fechar outro grupo interrompeu a captura ou manteve sua linha")
	}
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureIdle && !origin.busy && origin.lastResult != nil })
	if origin.editor.Text != "Texto anterior da captura.\n\nTexto desta gravação." {
		t.Fatal("o fechamento mudou o destino ou apagou o texto anterior à captura")
	}
}

func TestCloseGroupRejectsItsLateTranscriptionAndProcessing(t *testing.T) {
	release := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	audioStarted, textStarted := make(chan context.Context, 1), make(chan context.Context, 1)
	h := newHarness(t, runnerFunc(func(ctx context.Context, source string, _ config.Settings, _ transcript.Request) (
		transcript.Result, error,
	) {
		audioStarted <- ctx
		<-release
		return resultFor(source, "Transcrição antiga."), nil
	}), func(deps *dependencies) {
		deps.confirmTabs = func(_ fyne.Window, complete func(bool)) { complete(true) }
		deps.process = func(ctx context.Context, _ config.Settings, _ processing.Request) (processing.Result, error) {
			textStarted <- ctx
			<-release
			return processing.Result{Text: "Resposta antiga."}, nil
		}
	})
	t.Cleanup(unblock)
	first := h.d.document
	h.d.startJob("audio.wav")
	audio := waitValue(t, audioStarted)
	second := h.d.addDocument()
	second.editor.SetText("Texto em processamento.")
	test.Tap(h.d.processButton)
	text := waitValue(t, textStarted)
	other := h.d.addGroup()
	other.editor.SetText("Outro grupo intacto.")
	h.d.requestCloseGroup(first.group)
	unblock()
	h.settle()
	if audio.Err() == nil || text.Err() == nil || !first.closed || !second.closed ||
		h.d.hasJobs() || h.d.document != other || other.editor.Text != "Outro grupo intacto." ||
		other.processEditor.Text != "" || len(h.d.groups) != 1 {
		t.Fatal("uma resposta antiga reapareceu ou alterou o grupo preservado")
	}
	if _, err := os.Stat(h.d.latestPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a transcrição antiga reapareceu como último resultado")
	}
}

func TestCloseGroupKeepsMergeRunningInAnotherGroup(t *testing.T) {
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
	first.editor.SetText("Grupo que será fechado.")
	target := h.d.addGroup()
	target.editor.SetText("Parte um.")
	source := h.d.addDocument()
	source.editor.SetText("Parte dois.")
	test.Tap(h.d.mergeButton)
	ctx := waitValue(t, started)
	h.d.requestCloseGroup(first.group)
	if ctx.Err() != nil || !h.d.mergeBusy || !first.closed {
		t.Fatal("fechar outro grupo cancelou a mesclagem")
	}
	unblock()
	h.await(func() bool { return !h.d.mergeBusy })
	if target.editor.Text != "Mesclagem do segundo grupo." || !source.closed || len(h.d.groups) != 1 {
		t.Fatal("a mesclagem foi perdida ou recuperou o grupo fechado")
	}
}

func TestCloseGroupForgetsOnlyItsOwnClosedTab(t *testing.T) {
	for _, name := range []string{"desfazer do grupo fechado", "desfazer de outro grupo"} {
		t.Run(name, func(t *testing.T) {
			now := time.Now()
			h := newHarness(t, nil, func(deps *dependencies) {
				deps.now = func() time.Time { return now }
				deps.confirmTabs = func(_ fyne.Window, complete func(bool)) { complete(true) }
			})
			first := h.d.document
			toRestore := h.d.addDocument()
			toRestore.editor.SetText("Texto recuperável.")
			other := h.d.addGroup()
			h.d.closeDocument(toRestore.item)
			group := first.group
			if name == "desfazer de outro grupo" {
				group = other.group
			}
			h.d.requestCloseGroup(group)
			if name == "desfazer do grupo fechado" {
				if h.d.closedTab != nil || h.d.undoCloseButton.Visible() {
					t.Fatal("desfazer ainda aponta para um grupo que já foi fechado")
				}
			} else {
				h.d.undoCloseDocument()
				if toRestore.closed || h.d.document != toRestore || toRestore.editor.Text != "Texto recuperável." {
					t.Fatal("fechar outra linha perdeu o desfazer da aba preservada")
				}
			}
		})
	}
}
