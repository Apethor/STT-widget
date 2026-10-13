package desktop

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"ai-talker/internal/config"
	"ai-talker/internal/processing"
	"ai-talker/internal/store"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

func seedThreePrompts(t *testing.T, h *testHarness) *document {
	t.Helper()
	doc := h.d.document
	for index := 1; index <= 2; index++ {
		prompt, response := fmt.Sprintf("Prompt %d", index), fmt.Sprintf("Resposta %d", index)
		doc.editor.SetText(prompt)
		id := h.d.recordPrompt(doc, false)
		doc.processEditor.SetText(response)
		h.d.finishProcessingPrompt(doc, id, processing.Result{Text: response})
		doc.processHistory = append(doc.processHistory, processing.Turn{Prompt: prompt, Response: response})
	}
	source := historyAudio(t, h.d.directory, "atual.wav")
	h.d.displayDocumentResult(doc, resultFor(source, "Prompt 3"), false, "Prompt 3", false)
	return doc
}

func TestWorkspaceAndTabSelectionWhileBrowsingKeepCurrentAudioTranscript(t *testing.T) {
	h := newHarness(t, nil, nil)
	doc := seedThreePrompts(t, h)
	h.d.navigatePrompt(doc, -1)
	h.d.navigatePrompt(doc, -1)
	snapshot := h.d.snapshotWorkspace().Tabs[0]
	if snapshot.Text != "Prompt 3" || len(snapshot.AudioHistory) != 1 ||
		snapshot.AudioHistory[0].Text != "Prompt 3" || doc.editor.Text != "Prompt 1" {
		t.Fatal("a consulta associou o texto antigo ao áudio atual", snapshot)
	}
	h.d.addDocument()
	h.d.selectDocument(doc)
	saved := readResult(t, h.d.latestPath())
	if saved.Source != doc.lastAudio || saved.Text != "Prompt 3" || doc.editor.Text != "Prompt 1" {
		t.Fatal("selecionar a aba misturou o prompt antigo aos metadados atuais", saved)
	}
}

func TestPendingPromptEditRoundTripKeepsStepIdentityAndCount(t *testing.T) {
	h := newHarness(t, nil, nil)
	doc := seedThreePrompts(t, h)
	id := doc.promptSteps[2].ID
	doc.editor.SetText("Prompt 3 editado, ainda não enviado")
	for range 2 {
		if err := h.d.saveWorkspace(); err != nil {
			t.Fatal(err)
		}
		if !h.d.loadWorkspace() {
			t.Fatal("o workspace não reabriu")
		}
		doc = h.d.document
		if len(doc.promptSteps) != 3 || doc.promptSteps[2].ID != id || doc.promptSteps[2].Sent ||
			doc.editor.Text != "Prompt 3 editado, ainda não enviado" ||
			doc.processEditor.Text != "Resposta 2" || doc.editor.Disabled() {
			t.Fatal("editar/reabrir criou uma etapa fantasma ou perdeu o rascunho")
		}
	}
}

func TestSentPromptRemainsImmutableAfterCancelAndDraftEdit(t *testing.T) {
	started := make(chan processing.Request, 1)
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.process = func(ctx context.Context, _ config.Settings, request processing.Request) (processing.Result, error) {
			started <- request
			<-ctx.Done()
			return processing.Result{}, ctx.Err()
		}
	})
	doc := seedThreePrompts(t, h)
	test.Tap(doc.processFollowButton)
	request := waitValue(t, started)
	doc.editor.SetText("Rascunho editado depois do envio")
	h.settle()
	h.d.navigatePrompt(doc, -1)
	if request.Text != "Prompt 3" || len(doc.promptSteps) != 4 ||
		!doc.promptSteps[2].Sent || doc.promptSteps[2].Processed ||
		doc.promptSteps[2].Prompt != "Prompt 3" || len(doc.processHistory) != 2 ||
		doc.editor.Text != "Prompt 3" {
		t.Fatal("editar/cancelar/navegar reescreveu o prompt enviado")
	}
	h.d.navigatePrompt(doc, 1)
	if doc.editor.Text != "Rascunho editado depois do envio" || doc.editor.Disabled() ||
		doc.promptSteps[3].Sent || doc.processEditor.Text != "Resposta 2" {
		t.Fatal("o rascunho não enviado não foi preservado separadamente")
	}
}

func TestAudioHistoryRestoreAfterBrowsingWhileValidationPendingKeepsPageIdentity(t *testing.T) {
	h := newHarness(t, nil, nil)
	doc := seedThreePrompts(t, h)
	older := historyAudio(t, h.d.directory, "anterior.wav")
	doc.audioHistory = []audioSnapshot{{Source: older, Text: "Texto do áudio anterior"}}
	h.d.navigatePrompt(doc, -1)
	h.d.restoreAudioSnapshot(doc, older)
	// The validation callback is queued by the harness. Browsing again must
	// not let it publish the restored audio under the old prompt's cursor.
	h.d.navigatePrompt(doc, -1)
	h.settle()
	if doc.lastAudio != older || doc.editor.Text != "Texto do áudio anterior" ||
		doc.browsingPrompts() || doc.editor.Disabled() || doc.processEditor.Text != "Resposta 2" {
		t.Fatal("restaurar áudio mudou o texto de uma página histórica")
	}
	h.d.navigatePrompt(doc, -1)
	if doc.editor.Text != "Prompt 2" || doc.processEditor.Text != "Resposta 2" {
		t.Fatal("a troca de áudio sobrescreveu um par histórico")
	}
}

func TestScreenshotDeletionWhileBrowsingPreservesCurrentDraftAndSentPrompt(t *testing.T) {
	h := newHarness(t, nil, nil)
	doc := seedThreePrompts(t, h)
	path := filepath.Join(h.d.directory, "screenshots", "imagem.png")
	doc.promptSteps[0].Prompt += "\n" + path
	doc.editor.SetText("Rascunho completo com texto adicional\n" + path)
	doc.screenshotFiles = []string{path}
	h.d.navigatePrompt(doc, -1)
	h.d.navigatePrompt(doc, -1)
	h.d.removeDocumentScreenshotReference(doc, path)
	if doc.editor.Text != "Prompt 1\n"+path || !doc.browsingPrompts() ||
		doc.currentPrompt != "Rascunho completo com texto adicional" {
		t.Fatal("a exclusão substituiu o rascunho pelo prompt antigo ou reescreveu o envio")
	}
	for range 2 {
		h.d.navigatePrompt(doc, 1)
	}
	if doc.editor.Text != "Rascunho completo com texto adicional" ||
		len(doc.screenshotFiles) != 0 || strings.Contains(doc.lastResult.Text, path) {
		t.Fatal("o link excluído voltou no áudio atual ou o texto adicional desapareceu")
	}
}

func TestCloseAllTabsClearsPromptHistoryAfterSecondSave(t *testing.T) {
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.confirmTabs = func(_ fyne.Window, complete func(bool)) { complete(true) }
	})
	doc := seedThreePrompts(t, h)
	h.d.navigatePrompt(doc, -1)
	h.d.requestCloseTabs()
	if len(doc.promptSteps) != 0 || len(doc.processHistory) != 0 || doc.currentPrompt != "" ||
		doc.currentResponse != "" || doc.editor.Disabled() || doc.processEditor.Disabled() ||
		doc.promptBack.Visible() {
		t.Fatal("fechar todas deixou o histórico antigo acessível")
	}
	if err := h.d.saveWorkspace(); err != nil {
		t.Fatal(err)
	}
	var saved workspaceSnapshot
	if err := store.ReadJSON(filepath.Join(h.d.directory, "workspace-go-v1.json"), &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Tabs[0].PromptSteps) != 0 || saved.Tabs[0].Text != "" || saved.Tabs[0].ProcessedText != "" ||
		!h.d.loadWorkspace() || h.d.editor.Text != "" || len(h.d.promptSteps) != 0 {
		t.Fatal("salvar/reabrir ressuscitou prompts depois de fechar todas")
	}
}

func TestBrowsingInflightKeepsExplicitCancelActionAvailable(t *testing.T) {
	started := make(chan struct{}, 1)
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.process = func(ctx context.Context, _ config.Settings, _ processing.Request) (processing.Result, error) {
			started <- struct{}{}
			<-ctx.Done()
			return processing.Result{}, ctx.Err()
		}
	})
	doc := seedThreePrompts(t, h)
	test.Tap(doc.processFollowButton)
	waitValue(t, started)
	h.d.navigatePrompt(doc, -1)
	if !doc.processBusy || !doc.processButton.Visible() || doc.processButton.Disabled() ||
		doc.processFollowButton.Visible() {
		t.Fatal("consultar uma etapa antiga escondeu a ação explícita de cancelar")
	}
	test.Tap(doc.processButton)
	h.settle()
	if doc.processBusy || doc.editor.Text != "Prompt 2" || doc.processEditor.Text != "Resposta 2" ||
		!doc.browsingPrompts() {
		t.Fatal("cancelar alterou a página consultada")
	}
}
