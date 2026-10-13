package desktop

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"ai-talker/internal/config"
	"ai-talker/internal/processing"
	"ai-talker/internal/recorder"
	"ai-talker/internal/store"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func TestGroupsRenderSeparateRowsAndCreateTabsInTheirOwnRow(t *testing.T) {
	h := newHarness(t, nil, nil)
	first := h.d.document
	second := h.d.addDocument()
	newGroup := h.d.addGroup()
	last := h.d.addDocument()
	if len(h.d.groups) != 2 || first.group == newGroup.group ||
		first.item.Text != "Aba 1" || second.item.Text != "Aba 2" ||
		newGroup.item.Text != "Aba 1" || last.item.Text != "Aba 2" {
		t.Fatal("um grupo não começou uma linha com numeração independente")
	}
	renderer := h.d.tabs.renderer
	if len(renderer.rows) != 2 || renderer.rows[0].box.Position().Y >= renderer.rows[1].box.Position().Y ||
		renderer.rows[0].label.Text != "Grupo 1" || renderer.rows[1].label.Text != "Grupo 2" {
		t.Fatal("os grupos não apareceram em linhas separadas")
	}
	test.Tap(renderer.rows[0].create)
	created := h.d.document
	if created.group != first.group || created.item.Text != "Aba 3" ||
		!slices.Equal(h.d.documents, []*document{first, second, created, newGroup, last}) {
		t.Fatal("o + da primeira linha criou a aba no grupo selecionado de outra linha")
	}
	h.d.closeDocument(second.item)
	if created.item.Text != "Aba 2" || newGroup.item.Text != "Aba 1" || last.item.Text != "Aba 2" {
		t.Fatal("fechar uma aba mudou a numeração de outro grupo")
	}
}

func TestCreateGroupButtonOnLeftAddsEmptySelectedGroupAtEnd(t *testing.T) {
	h := newHarness(t, nil, nil)
	first := h.d.document
	first.editor.SetText("Texto do primeiro grupo.")
	second := h.d.addGroup()
	second.editor.SetText("Texto do segundo grupo.")
	button := h.d.tabs.renderer.createGroup
	if button == nil || button.Disabled() {
		t.Fatal("o botão lateral para criar grupo não ficou disponível")
	}
	test.Tap(button)
	created := h.d.document
	if len(h.d.groups) != 3 || h.d.groups[0] != first.group || h.d.groups[1] != second.group ||
		h.d.groups[2] != created.group || created.editor.Text != "" || created.processEditor.Text != "" ||
		created.item.Text != "Aba 1" || h.d.groupName(created.group) != "Grupo 3" ||
		h.d.tabs.Selected() != created.item || h.d.document != created {
		t.Fatal("o botão não criou um grupo vazio selecionado ao final")
	}
	if button.Position().X >= h.d.tabs.renderer.groupScroll.Position().X {
		t.Fatal("o botão para criar grupo não está à esquerda da lista")
	}
}

func TestGroupsMenuOffersNewGroupAndConfirmedGlobalClear(t *testing.T) {
	var complete func(bool)
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.confirmTabs = func(_ fyne.Window, callback func(bool)) { complete = callback }
	})
	first := h.d.document
	first.editor.SetText("Texto do primeiro grupo.")
	test.Tap(h.d.tabs.renderer.rows[0].more)
	overlays := h.d.window.Canvas().Overlays().List()
	if len(overlays) != 1 {
		t.Fatal("os três pontinhos não abriram o menu único do grupo")
	}
	menu, ok := h.d.window.Canvas().Focused().(*widget.PopUpMenu)
	if !ok {
		t.Fatal("o controle abriu algo diferente de um menu")
	}
	actions := h.d.groupMenu()
	if len(actions.Items) != 7 || actions.Items[0].Label != "Renomear grupo…" ||
		actions.Items[1].Label != "Fechar grupo…" ||
		actions.Items[2].Label != "Fechar abas deste grupo…" ||
		actions.Items[4].Label != "Novo grupo" ||
		actions.Items[5].Label != "Fechar abas de todos os grupos…" {
		t.Fatal("o menu não distinguiu fechar o grupo, fechar suas abas e fechar todas")
	}
	for range 5 {
		menu.TypedKey(&fyne.KeyEvent{Name: fyne.KeyDown})
	}
	menu.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	if len(h.d.groups) != 2 {
		t.Fatal("selecionar Novo grupo no menu não criou uma linha")
	}
	h.d.editor.SetText("Texto do segundo grupo.")
	h.d.groupMenu().Items[5].Action()
	if complete == nil || !h.d.tabsDialog || len(h.d.groups) != 2 {
		t.Fatal("fechar todas alterou grupos antes da confirmação")
	}
	complete(false)
	if first.editor.Text != "Texto do primeiro grupo." || len(h.d.groups) != 2 {
		t.Fatal("cancelar a confirmação apagou um grupo")
	}
	h.d.groupMenu().Items[5].Action()
	complete(true)
	if len(h.d.groups) != 1 || len(h.d.documents) != 1 ||
		h.d.document != first || first.editor.Text != "" || first.processEditor.Text != "" {
		t.Fatal("fechar todas não deixou uma única aba vazia no primeiro grupo")
	}
	var saved workspaceSnapshot
	if err := store.ReadJSON(filepath.Join(h.d.directory, "workspace-go-v1.json"), &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Groups) != 1 || len(saved.Tabs) != 1 || saved.Tabs[0].Text != "" {
		t.Fatal("o fechamento de todos os grupos não foi persistido")
	}
}

func TestGroupsRestoreNamesEditorsOptionsSelectionAndClearUndo(t *testing.T) {
	h := newHarness(t, nil, nil)
	first := h.d.document
	first.editor.SetText("Texto do primeiro grupo.")
	first.processEditor.SetText("Mensagem pronta do primeiro grupo.")
	h.d.addDocument()
	target := h.d.addGroup()
	target.editor.SetText("Texto antes da limpeza.")
	test.Tap(target.clearButton)
	target.editor.SetText("Prompt atual do segundo grupo.")
	target.processEditor.SetText("Resposta atual do segundo grupo.")
	target.processOptions.Mode = processing.ModeFree
	target.processOptions.WebSearchDisabled = true
	if err := h.d.renameDocument(target, "Mendes"); err != nil {
		t.Fatal(err)
	}
	h.d.addDocument()
	h.d.tabs.Select(target.item)
	if err := h.d.saveWorkspace(); err != nil {
		t.Fatal(err)
	}
	restored := newHarness(t, nil, nil)
	restored.d.directory = h.d.directory
	if !restored.d.loadWorkspace() || len(restored.d.groups) != 2 || len(restored.d.documents) != 4 ||
		restored.d.document.id != target.id || restored.d.document.group != target.group ||
		restored.d.document.item.Text != "Mendes" || restored.d.editor.Text != target.editor.Text ||
		restored.d.processEditor.Text != target.processEditor.Text ||
		restored.d.processOptions.Mode != processing.ModeFree || !restored.d.processOptions.WebSearchDisabled {
		t.Fatal("reabrir perdeu o grupo, a seleção, o nome, um editor ou suas opções")
	}
	test.Tap(restored.d.undoButton)
	if restored.d.editor.Text != "Texto antes da limpeza." ||
		restored.d.documents[0].editor.Text != first.editor.Text ||
		restored.d.documents[0].processEditor.Text != first.processEditor.Text ||
		len(restored.d.tabs.renderer.rows) != 2 {
		t.Fatal("o desfazer restaurado atravessou os grupos")
	}
}

func TestLegacyWorkspaceMigratesAllTabsToFirstGroup(t *testing.T) {
	h := newHarness(t, nil, nil)
	snapshot := workspaceSnapshot{
		Version: 1, Selected: 7, NextID: 7,
		Tabs: []documentSnapshot{
			{ID: 3, Name: "Aba 8", Text: "Primeiro texto antigo.", Options: h.d.settings.Processing},
			{ID: 7, Name: "Aba 99", Text: "Segundo texto antigo.", Options: h.d.settings.Processing},
		},
	}
	if err := store.WriteJSON(filepath.Join(h.d.directory, "workspace-go-v1.json"), snapshot); err != nil {
		t.Fatal(err)
	}
	if !h.d.loadWorkspace() || len(h.d.groups) != 1 || h.d.document.id != 7 ||
		h.d.documents[0].group != h.d.document.group || h.d.documents[0].item.Text != "Aba 1" ||
		h.d.document.item.Text != "Aba 2" || h.d.editor.Text != "Segundo texto antigo." {
		t.Fatal("a sessão anterior perdeu texto ou não migrou para o Grupo 1")
	}
}

func TestInvalidGroupsKeepWorkspaceAndAllowRecovery(t *testing.T) {
	for _, tc := range []struct {
		name   string
		groups []uint64
	}{
		{"grupo zero", []uint64{0}},
		{"grupo duplicado", []uint64{1, 1}},
		{"referência desconhecida", []uint64{2}},
		{"grupo vazio", []uint64{1, 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, nil, nil)
			snapshot := workspaceSnapshot{
				Version: 1, NextID: 1, Groups: tc.groups,
				Tabs: []documentSnapshot{{ID: 1, Group: 1, Text: "Original preservado.", Options: h.d.settings.Processing}},
			}
			path := filepath.Join(h.d.directory, "workspace-go-v1.json")
			if err := store.WriteJSON(path, snapshot); err != nil {
				t.Fatal(err)
			}
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !h.d.loadWorkspace() || !h.d.workspaceInvalid || !h.d.errorLabel.Visible() ||
				len(h.d.groups) != 1 || h.d.editor.Text != "" {
				t.Fatal("grupos inválidos foram carregados parcialmente")
			}
			current, err := os.ReadFile(path)
			if err != nil || string(current) != string(original) {
				t.Fatal("a sessão inválida foi alterada antes de sua preservação")
			}
			h.d.editor.SetText("Texto recuperado manualmente.")
			if err := h.d.saveWorkspace(); err != nil {
				t.Fatal(err)
			}
			backups, err := filepath.Glob(filepath.Join(h.d.directory, "workspace-go-v1-invalid-*.json"))
			if err != nil || len(backups) != 1 {
				t.Fatal("a sessão anterior não foi preservada ao salvar a recuperação")
			}
			requireFile(t, backups[0], string(original))
		})
	}
}

func TestGroupCreateWriteFailureRestoresPreviousRowsAndSelection(t *testing.T) {
	h := newHarness(t, nil, nil)
	first := h.d.document
	first.editor.SetText("Texto que precisa continuar aqui.")
	path := filepath.Join(h.d.directory, "workspace-go-v1.json")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if h.d.addGroup() != nil || len(h.d.groups) != 1 || len(h.d.documents) != 1 ||
		h.d.document != first || first.editor.Text != "Texto que precisa continuar aqui." ||
		len(h.d.tabs.renderer.rows) != 1 || !h.d.errorLabel.Visible() {
		t.Fatal("uma falha ao salvar deixou um grupo parcial ou perdeu o texto anterior")
	}
}

func TestJoinOnlyUsesSelectedGroupAndPreservesEveryOtherEditor(t *testing.T) {
	now := time.Now()
	h := newHarness(t, nil, func(deps *dependencies) { deps.now = func() time.Time { return now } })
	first := h.d.document
	first.editor.SetText("Primeiro grupo, primeira aba.")
	first.processEditor.SetText("Resposta do primeiro grupo.")
	second := h.d.addDocument()
	second.editor.SetText("Primeiro grupo, segunda aba.")
	target := h.d.addGroup()
	target.editor.SetText("Segundo grupo, primeira aba.")
	target.processEditor.SetText("Resposta do segundo grupo.")
	source := h.d.addDocument()
	source.editor.SetText("Segundo grupo, segunda aba.")
	source.processEditor.SetText("Resposta da origem fechada.")
	test.Tap(h.d.joinButton)
	if target.editor.Text != "Segundo grupo, primeira aba.\n\nSegundo grupo, segunda aba." ||
		h.d.document != target || !source.closed || len(h.d.documents) != 3 || len(h.d.groups) != 2 ||
		first.editor.Text != "Primeiro grupo, primeira aba." || second.editor.Text != "Primeiro grupo, segunda aba." ||
		first.processEditor.Text != "Resposta do primeiro grupo." ||
		target.processEditor.Text != "Resposta do segundo grupo." || source.processEditor.Text != "Resposta da origem fechada." {
		t.Fatal("Juntar incluiu, limpou ou fechou abas de outro grupo")
	}
	var saved workspaceSnapshot
	if err := store.ReadJSON(filepath.Join(h.d.directory, "workspace-go-v1.json"), &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Tabs) != 3 || len(saved.Groups) != 2 || saved.Tabs[0].Text != first.editor.Text ||
		saved.Tabs[2].Group != target.group || saved.Selected != target.id {
		t.Fatal("a sessão não manteve os outros grupos após Juntar")
	}
	test.Tap(h.d.undoCloseButton)
	test.Tap(source.undoButton)
	if source.editor.Text != "Segundo grupo, segunda aba." || h.d.document.group != target.group ||
		first.editor.Text != "Primeiro grupo, primeira aba." {
		t.Fatal("desfazer o fechamento ou a limpeza atravessou os grupos")
	}
}

func TestGroupMergeKeepsOriginWhileOtherGroupsChangeAndStaySelected(t *testing.T) {
	contexts := make(chan context.Context, 1)
	requests := make(chan processing.Request, 1)
	release := make(chan struct{})
	releaseCall := sync.OnceFunc(func() { close(release) })
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.process = func(ctx context.Context, _ config.Settings, request processing.Request) (processing.Result, error) {
			contexts <- ctx
			requests <- request
			<-release
			return processing.Result{Text: "Somente o segundo grupo foi mesclado."}, nil
		}
	})
	t.Cleanup(releaseCall)
	other := h.d.document
	other.editor.SetText("Original de outro grupo.")
	removed := h.d.addDocument()
	target := h.d.addGroup()
	target.editor.SetText("Parte um do grupo de origem.")
	source := h.d.addDocument()
	source.editor.SetText("Parte dois do grupo de origem.")
	test.Tap(h.d.mergeButton)
	ctx, request := waitValue(t, contexts), waitValue(t, requests)
	var inputs []struct {
		Text string `json:"texto"`
	}
	if err := json.Unmarshal([]byte(request.Text), &inputs); err != nil || len(inputs) != 2 ||
		inputs[0].Text != target.editor.Text || inputs[1].Text != source.editor.Text {
		t.Fatal("Mesclar enviou textos de outro grupo")
	}
	h.d.tabs.Select(other.item)
	other.editor.SetText("Edição independente durante a mesclagem.")
	added := h.d.addDocument()
	added.editor.SetText("Nova aba em outro grupo.")
	if err := h.d.renameDocument(added, "Projeto separado"); err != nil {
		t.Fatal(err)
	}
	h.d.closeDocument(removed.item)
	third := h.d.addGroup()
	h.d.tabs.Select(added.item)
	if ctx.Err() != nil || !h.d.mergeBusy {
		t.Fatal("editar, abrir, renomear ou fechar em outro grupo cancelou a mesclagem")
	}
	releaseCall()
	h.await(func() bool { return !h.d.mergeBusy })
	if target.editor.Text != "Somente o segundo grupo foi mesclado." || !source.closed ||
		len(h.d.groupDocuments(target.group)) != 1 || h.d.document != added ||
		other.editor.Text != "Edição independente durante a mesclagem." ||
		added.editor.Text != "Nova aba em outro grupo." || third.editor.Text != "" || third.closed {
		t.Fatal("uma mesclagem foi redirecionada pela seleção ou alterou outro grupo")
	}
}

func TestUndoClosedTabRestoresGroupPositionAfterOtherRowsGainTabs(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	h := newHarness(t, nil, func(deps *dependencies) { deps.now = func() time.Time { return now } })
	first := h.d.document
	target := h.d.addGroup()
	target.editor.SetText("Texto da primeira aba do segundo grupo.")
	middle := h.d.addDocument()
	middle.editor.SetText("Texto da aba fechada.")
	last := h.d.addDocument()
	h.d.closeDocument(middle.item)
	h.d.tabs.Select(first.item)
	added := h.d.addDocument()
	test.Tap(h.d.undoCloseButton)
	want := []*document{first, added, target, middle, last}
	if !slices.Equal(h.d.documents, want) || h.d.document != middle || middle.item.Text != "Aba 2" ||
		last.item.Text != "Aba 3" || middle.editor.Text != "Texto da aba fechada." {
		t.Fatal("restaurar inseriu a aba em outra linha ou na posição errada de seu grupo")
	}
	for index, header := range h.d.tabs.renderer.headers {
		if header.item != want[index].item {
			t.Fatal("a barra visual não acompanhou a ordem restaurada das abas")
		}
	}
	h.d.closeDocument(middle.item)
	h.d.closeDocument(last.item)
	h.d.closeDocument(target.item)
	if slices.Contains(h.d.groups, target.group) || h.d.document != first {
		t.Fatal("fechar a última aba manteve o grupo vazio ou não selecionou o grupo restante")
	}
	h.d.tabs.Select(first.item)
	h.d.addDocument()
	test.Tap(h.d.undoCloseButton)
	if first.closed || h.d.document != target || len(h.d.groupDocuments(target.group)) != 1 ||
		!slices.Contains(h.d.groups, target.group) ||
		target.editor.Text != "Texto da primeira aba do segundo grupo." {
		t.Fatal("restaurar a última aba não devolveu seu grupo ou alterou o grupo restante")
	}
}

func TestManyGroupRowsScrollWithoutIncreasingMinimumWidth(t *testing.T) {
	now := time.Now()
	h := newHarness(t, nil, func(deps *dependencies) { deps.now = func() time.Time { return now } })
	first := h.d.document
	for range 5 {
		h.d.addGroup()
	}
	last := h.d.document
	h.d.window.Resize(h.d.window.Content().MinSize())
	renderer := h.d.tabs.renderer
	if len(renderer.rows) != 6 || renderer.groupScroll.Offset.Y <= 0 ||
		h.d.window.Content().MinSize().Width > 320 {
		t.Fatal("muitos grupos não rolaram para a linha selecionada ou alargaram a coluna")
	}
	h.d.tabs.Select(first.item)
	if renderer.groupScroll.Offset.Y != 0 {
		t.Fatal("selecionar o primeiro grupo deixou sua linha fora da área visível")
	}
	h.d.tabs.Select(last.item)
	if renderer.groupScroll.Offset.Y <= 0 || h.d.editor.Size().Height < h.d.editor.MinSize().Height {
		t.Fatal("selecionar o último grupo deixou a linha ou o editor inacessível")
	}
	closed := h.d.addDocument()
	closed.editor.SetText("Texto para restaurar na última linha.")
	h.d.closeDocument(closed.item)
	undo := h.d.undoCloseButton
	position := h.app.Driver().AbsolutePositionForObject(undo).Add(fyne.NewPos(12, 12))
	test.TapCanvas(h.d.window.Canvas(), position)
	if h.d.document != closed || closed.closed || closed.editor.Text != "Texto para restaurar na última linha." {
		t.Fatal("o desfazer da última linha ficou fora da área visível dos grupos")
	}
}

func TestCompactGroupRowKeepsSelectedCloseButtonClickable(t *testing.T) {
	h := newHarness(t, nil, nil)
	first := h.d.document
	second := h.d.addDocument()
	other := h.d.addGroup()
	h.d.tabs.Select(first.item)
	h.d.closeDocument(second.item)
	h.d.window.Resize(fyne.NewSize(320, 700))
	var closeButton *widget.Button
	for _, header := range h.d.tabs.renderer.headers {
		if header.item == first.item {
			closeButton = header.close
		}
	}
	if closeButton == nil {
		t.Fatal("o grupo não mostrou o fechamento da aba selecionada")
	}
	position := h.app.Driver().AbsolutePositionForObject(closeButton).Add(fyne.NewPos(12, 12))
	test.TapCanvas(h.d.window.Canvas(), position)
	if !first.closed || other.closed || h.d.document != other || !slices.Equal(h.d.groups, []uint64{other.group}) {
		t.Fatal("a coluna estreita ocultou o fechamento da aba selecionada")
	}
}

func TestNewGroupDuringRecordingKeepsAudioAndAppendInOrigin(t *testing.T) {
	r := &fakeRecording{state: recorder.Snapshot{Path: "primeiro-grupo.wav"}}
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.newRecorder = func(int, string) recording { return r }
	})
	first := h.d.document
	first.editor.SetText("Texto anterior do primeiro grupo.")
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	if h.d.groupMenu().Items[4].Disabled || !h.d.groupMenu().Items[5].Disabled {
		t.Fatal("a gravação bloqueou criar grupo ou permitiu limpar todos")
	}
	second := h.d.addGroup()
	second.editor.SetText("Segundo grupo independente.")
	h.d.tick()
	if h.d.caption.Text != "primeiro-grupo.wav" {
		t.Fatal("o gravador deixou de identificar o arquivo de origem")
	}
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureIdle && !first.busy && first.lastResult != nil })
	if first.editor.Text != "Texto anterior do primeiro grupo.\n\nTexto desta gravação." ||
		second.editor.Text != "Segundo grupo independente." || first.lastAudio != "primeiro-grupo.wav" ||
		second.lastAudio != "" || h.d.document != second {
		t.Fatal("criar ou selecionar outro grupo redirecionou a gravação")
	}
}
