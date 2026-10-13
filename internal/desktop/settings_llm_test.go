package desktop

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"ai-talker/internal/apiconfig"
	"ai-talker/internal/config"
	"ai-talker/internal/processing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

func TestGeneralLLMSettingsApplyCurrentTabAndKeepProcessingMergeModelsIndependent(t *testing.T) {
	requests := make(chan processing.Request, 2)
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.process = func(_ context.Context, _ config.Settings, request processing.Request) (processing.Result, error) {
			requests <- request
			return processing.Result{Text: "Texto combinado por API"}, nil
		}
	})
	first := h.d.document
	first.editor.SetText("Primeira fala")
	second := h.d.addDocument()
	second.editor.SetText("Segunda fala")
	firstOptions := first.processOptions
	h.d.openSettingsSection("LLM")
	main := h.d.settingsForm
	h.await(func() bool { return !main.loading })
	h.settle()
	assertLLMControlsShareRow(t, h, main.llm.model, main.llm.reasoning)
	assertLLMControlsShareRow(t, h, main.mergeModel, main.mergeReasoning)
	main.llm.provider.SetSelected("API")
	main.llm.api.provider.SetSelected(genericAPIChoice)
	main.llm.api.endpoint.SetText("https://provider.example/v1")
	main.llm.model.SetText("process-model")
	main.llm.reasoning.SetSelected(lowReasoning)
	main.mergeModel.SetText("merge-model")
	main.mergeReasoning.SetSelected(highReasoning)
	main.llm.outputTokens.SetSelected(processing.OutputMaxTokens)
	main.mergeOutputTokens.SetSelected(processing.OutputMaxCompletionTokens)
	test.Tap(main.save)
	h.await(func() bool { return h.d.settingsForm == nil })
	if first.processOptions != firstOptions || second.processOptions.ModelID != "process-model" ||
		second.processOptions.MergeModelID != "merge-model" || second.processOptions.Provider != "api" ||
		second.processOptions.ReasoningEffort != "low" || second.processOptions.MergeReasoning != "high" ||
		second.processOptions.OutputTokenParameter != processing.OutputMaxTokens ||
		second.processOptions.MergeOutputTokenParameter != processing.OutputMaxCompletionTokens {
		t.Fatal("salvar mudou outras abas ou confundiu os modelos/raciocínios")
	}
	before := second.processOptions
	for _, mode := range []string{freeModeChoice, teamsModeChoice, mergeModeChoice} {
		second.processMode.SetSelected(mode)
		h.settle()
		if second.processOptions.Provider != before.Provider || second.processOptions.API != before.API ||
			second.processOptions.ModelID != before.ModelID || second.processOptions.MergeModelID != before.MergeModelID ||
			second.processOptions.ReasoningEffort != before.ReasoningEffort || second.processOptions.MergeReasoning != before.MergeReasoning {
			t.Fatal("selecionar uma instrução mudou a configuração de LLM")
		}
	}
	if second.processReasoning.Selected != "Alto" || second.processSearch.Visible() || second.processFollowButton.Visible() {
		t.Fatal("o painel não apresentou o raciocínio contextual de Mesclar")
	}
	test.Tap(second.processButton)
	request := waitValue(t, requests)
	h.await(func() bool { return !h.d.mergeBusy })
	if request.Options.Provider != "api" ||
		request.Options.API.Provider != apiconfig.Generic || request.Options.API.Endpoint != "https://provider.example/v1" ||
		request.Options.ModelID != "merge-model" || request.Options.ReasoningEffort != "high" ||
		request.Options.OutputTokenParameter != processing.OutputMaxCompletionTokens ||
		!request.Options.WebSearchDisabled || request.Options.Mode != processing.ModeMerge ||
		first.editor.Text != "Texto combinado por API" || second.closed != true {
		t.Fatal("Mesclar não usou o provider comum ou não devolveu à Transcrição",
			request, first.editor.Text, second.closed, h.d.errorLabel.Text)
	}
}

func assertLLMControlsShareRow(
	t *testing.T,
	h *testHarness,
	model fyne.CanvasObject,
	reasoning fyne.CanvasObject,
) {
	t.Helper()
	modelPosition := h.app.Driver().AbsolutePositionForObject(model)
	reasoningPosition := h.app.Driver().AbsolutePositionForObject(reasoning)
	if delta := modelPosition.Y - reasoningPosition.Y; delta < -1 || delta > 1 ||
		model.Size().Width <= reasoning.Size().Width {
		t.Fatalf("Modelo e Raciocínio não dividiram a linha com o modelo mais largo: modelo=%v %v, raciocínio=%v %v",
			modelPosition, model.Size(), reasoningPosition, reasoning.Size())
	}
}

func TestCustomInstructionLimitRenamingDeletionAndHistoricalSnapshot(t *testing.T) {
	requests := make(chan processing.Request, 1)
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.process = func(_ context.Context, _ config.Settings, request processing.Request) (processing.Result, error) {
			requests <- request
			return processing.Result{Text: "Resposta pronta"}, nil
		}
	})
	h.d.openSettingsSection("Instruções")
	main := h.d.settingsForm
	h.await(func() bool { return !main.loading })
	f := main.instructions
	for index := range 10 {
		f.selectedID = ""
		f.name.SetText(fmt.Sprintf("Instrução %d", index))
		f.prompt.SetText(fmt.Sprintf("Prompt personalizado %d", index))
		test.Tap(f.save)
	}
	f.selectedID = ""
	f.name.SetText("Décima primeira")
	f.prompt.SetText("Não pode ultrapassar dez.")
	test.Tap(f.save)
	if len(f.values) != 10 {
		t.Fatal("o limite de dez não foi respeitado")
	}
	test.Tap(main.save)
	h.await(func() bool { return h.d.settingsForm == nil })
	doc := h.d.document
	before := doc.processOptions
	doc.processMode.SetSelected("Instrução 0")
	h.settle()
	if doc.processOptions.ModelID != before.ModelID || doc.processOptions.Provider != before.Provider {
		t.Fatal("o modo mudou o LLM")
	}
	doc.editor.SetText("Pedido com instrução")
	test.Tap(doc.processButton)
	request := waitValue(t, requests)
	h.await(func() bool { return !doc.processBusy })
	if request.Options.SystemPrompt() != "Prompt personalizado 0" {
		t.Fatal("a instrução não foi enviada")
	}
	frozen := *doc.promptSteps[0].Options
	h.d.openSettingsSection("Instruções")
	main = h.d.settingsForm
	h.await(func() bool { return !main.loading })
	f = main.instructions
	f.selection.SetSelected("Instrução 0")
	f.name.SetText("Novo nome")
	f.prompt.SetText("Prompt novo")
	test.Tap(f.save)
	test.Tap(main.save)
	h.await(func() bool { return h.d.settingsForm == nil })
	if *doc.promptSteps[0].Options != frozen || doc.processMode.Selected != "Novo nome" {
		t.Fatal("renomear alterou a definição histórica ou perdeu a seleção")
	}
	h.d.openSettingsSection("Instruções")
	main = h.d.settingsForm
	h.await(func() bool { return !main.loading })
	f = main.instructions
	f.selection.SetSelected("Novo nome")
	test.Tap(f.remove)
	if len(f.values) != 10 {
		t.Fatal("a instrução em uso foi excluída sem escolher o próximo modo")
	}
	f.replacement.SetSelected(freeModeChoice)
	test.Tap(f.remove)
	test.Tap(main.save)
	h.await(func() bool { return h.d.settingsForm == nil })
	if len(h.d.settings.Instructions) != 9 || doc.processOptions.Mode != processing.ModeFree || *doc.promptSteps[0].Options != frozen {
		t.Fatal("excluir perdeu o histórico ou não aplicou o próximo modo explícito")
	}
	if !h.d.loadWorkspace() || *h.d.document.promptSteps[0].Options != frozen {
		t.Fatal("a definição histórica não persistiu")
	}
}

func TestWhisperSourceSelectionPersistsWithoutChangingRecognitionSettings(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.d.openSettingsSection("Whisper")
	main := h.d.settingsForm
	h.await(func() bool { return !main.loading })
	before := h.d.settings.Whisper
	main.whisper.downloadSource.SetSelected("Hugging Face")
	test.Tap(main.save)
	h.await(func() bool { return h.d.settingsForm == nil })
	saved, err := config.Load(h.d.directory)
	if err != nil || saved.WhisperDownloadSource != "huggingface" || saved.Whisper != before {
		t.Fatal("a origem não persistiu ou alterou a configuração/cache de reconhecimento", err)
	}
	h.d.openSettingsSection("Whisper")
	main = h.d.settingsForm
	h.await(func() bool { return !main.loading })
	if main.whisper.downloadSource.Selected != "Hugging Face" || !slices.Contains(main.whisper.downloadSource.Options, "GitHub") {
		t.Fatal("a seleção exclusiva não foi restaurada")
	}
}
