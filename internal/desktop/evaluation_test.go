package desktop

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ai-talker/internal/store"
	"ai-talker/internal/transcript"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func TestTranscriptEvaluationDistinguishesZeroAndInvalidatesChangedContext(t *testing.T) {
	h := newHarness(t, nil, nil)
	doc := h.d.document
	doc.lastAudio = filepath.Join(h.d.directory, "gravacoes", "aula.wav")
	doc.lastResult = &transcript.Result{Source: doc.lastAudio, Text: "Texto reconhecido."}
	doc.editor.SetText("Texto reconhecido.")
	reference := h.d.transcriptEvaluationReference(doc)
	if reference == "" {
		t.Fatal("a transcrição concluída não gerou uma referência")
	}
	doc.evaluation = &transcriptEvaluation{
		Score: 0, Comment: "Sem estrelas é nota zero.", Reference: reference, UpdatedAt: time.Now(),
	}
	if !validTranscriptEvaluation(doc.evaluation) {
		t.Fatal("a nota zero explícita foi rejeitada")
	}
	if err := h.d.saveWorkspace(); err != nil {
		t.Fatal(err)
	}
	var saved workspaceSnapshot
	if err := store.ReadJSON(filepath.Join(h.d.directory, "workspace-go-v1.json"), &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Tabs) != 1 || saved.Tabs[0].Evaluation == nil || saved.Tabs[0].Evaluation.Score != 0 {
		t.Fatal("a nota zero não foi persistida como avaliação")
	}

	doc.editor.SetText("Texto corrigido.")
	h.d.syncTranscriptEvaluation(doc)
	if doc.evaluation != nil {
		t.Fatal("editar o texto avaliado manteve a avaliação antiga")
	}
	if doc.evaluationButton.Text != "☆" {
		t.Fatalf("o botão não voltou ao estado sem avaliação: %q", doc.evaluationButton.Text)
	}
	if err := h.d.saveWorkspace(); err != nil {
		t.Fatal(err)
	}
	saved = workspaceSnapshot{}
	if err := store.ReadJSON(filepath.Join(h.d.directory, "workspace-go-v1.json"), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Tabs[0].Evaluation != nil {
		t.Fatal("a avaliação invalidada continuou no workspace")
	}
}

func TestClosingTabArchivesCurrentEvaluationAndItsTranscriptContext(t *testing.T) {
	h := newHarness(t, nil, nil)
	doc := h.d.document
	doc.lastAudio = filepath.Join(h.d.directory, "gravacoes", "fechamento.wav")
	doc.lastResult = &transcript.Result{Source: doc.lastAudio, Text: "Etapa final."}
	doc.editor.SetText("Etapa final.")
	doc.evaluation = &transcriptEvaluation{
		Score: 4, Comment: "Boa transcrição.", Reference: h.d.transcriptEvaluationReference(doc),
		UpdatedAt: time.Now(),
	}

	h.d.closeDocument(doc.item)

	var archive evaluationArchive
	path := filepath.Join(h.d.directory, "avaliacoes-fechadas-v1.json")
	if err := store.ReadJSON(path, &archive); err != nil {
		t.Fatal(err)
	}
	if archive.Version != 1 || len(archive.Entries) != 1 {
		t.Fatalf("arquivo interno de avaliações inesperado: %+v", archive)
	}
	entry := archive.Entries[0]
	if entry.TabID != doc.id || entry.Evaluation.Score != 4 ||
		entry.Evaluation.Comment != "Boa transcrição." || len(entry.Context) != 1 ||
		entry.Context[0].Source != doc.lastAudio || entry.Context[0].Text != "Etapa final." {
		t.Fatalf("o fechamento não preservou a referência completa: %+v", entry)
	}
	if h.d.document == doc {
		t.Fatal("a aba fechada continuou selecionada")
	}
}

func TestDeletingLinkedScreenshotAlsoRemovesClosedEvaluation(t *testing.T) {
	h := newHarness(t, nil, nil)
	doc := h.d.document
	doc.lastAudio = filepath.Join(recordingFolder(h.d.directory), "aula.wav")
	screenshot := filepath.Join(h.d.directory, "screenshots", "aula.png")
	doc.editor.SetText("Texto avaliado.")
	doc.screenshotFiles = []string{screenshot}
	doc.evaluation = &transcriptEvaluation{
		Score: 4, Comment: "Boa.", UpdatedAt: time.Now(),
		Reference: h.d.transcriptEvaluationReference(doc),
	}
	if err := h.d.archiveDocumentEvaluation(doc); err != nil {
		t.Fatal(err)
	}
	h.d.removeScreenshotReference(nil, screenshot)
	archive, err := h.d.readEvaluationArchive()
	if err != nil {
		t.Fatal(err)
	}
	if len(archive.Entries) != 0 || doc.evaluation != nil {
		t.Fatalf("excluir a screenshot manteve a nota/comentário do contexto incompleto: %+v", archive)
	}
}

func TestTranscriptEvaluationRequiresEnviarAndReplacesScoreAndComment(t *testing.T) {
	h := newHarness(t, nil, nil)
	doc := h.d.document
	source := filepath.Join(recordingFolder(h.d.directory), "avaliada.wav")
	h.d.displayDocumentResult(doc, resultFor(source, "Texto para avaliar."), false, "Texto para avaliar.", false)
	test.Tap(doc.evaluationButton)
	test.Tap(evaluationDialogButton(t, h, "Cancelar"))
	if doc.evaluation != nil {
		t.Fatal("Cancelar no seletor enviou uma avaliação")
	}

	test.Tap(doc.evaluationButton)
	test.Tap(evaluationDialogButton(t, h, "OK"))
	if doc.evaluation != nil {
		t.Fatal("OK enviou a avaliação antes do comentário e de Enviar")
	}
	test.Tap(evaluationDialogButton(t, h, "Cancelar"))
	if doc.evaluation != nil {
		t.Fatal("Cancelar no comentário salvou nota zero")
	}

	test.Tap(doc.evaluationButton)
	test.Tap(evaluationDialogButton(t, h, "OK"))
	test.Tap(evaluationDialogButton(t, h, "Enviar"))
	if doc.evaluation == nil || doc.evaluation.Score != 0 || doc.evaluation.Comment != "" ||
		doc.evaluationButton.Text != "★" {
		t.Fatal("Enviar vazio não distinguiu nota zero de sem avaliação")
	}
	first := doc.evaluation

	test.Tap(doc.evaluationButton)
	var stars []*widget.Button
	for _, object := range test.LaidOutObjects(h.d.window.Canvas().Overlays().Top()) {
		if button, ok := object.(*widget.Button); ok && button.Text == "☆" {
			stars = append(stars, button)
		}
	}
	if len(stars) != 5 {
		t.Fatal("a reavaliação não abriu cinco estrelas pequenas")
	}
	test.Tap(stars[3])
	test.Tap(evaluationDialogButton(t, h, "OK"))
	for _, object := range test.LaidOutObjects(h.d.window.Canvas().Overlays().Top()) {
		if entry, ok := object.(*widget.Entry); ok {
			entry.SetText("Comentário substituído.")
		}
	}
	test.Tap(evaluationDialogButton(t, h, "Enviar"))
	if doc.evaluation == first || doc.evaluation.Score != 4 ||
		doc.evaluation.Comment != "Comentário substituído." || len(doc.evaluation.Context) != 1 {
		t.Fatal("reavaliar não substituiu a nota/comentário com o contexto atual")
	}
	var saved workspaceSnapshot
	if err := store.ReadJSON(filepath.Join(h.d.directory, "workspace-go-v1.json"), &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Tabs) != 1 || saved.Tabs[0].Evaluation == nil ||
		saved.Tabs[0].Evaluation.Score != 4 || saved.Tabs[0].Evaluation.Comment != "Comentário substituído." {
		t.Fatal("a avaliação atual não foi a única avaliação persistida na aba")
	}
}

func TestEvaluationContextKeepsFinalAudioStagesAndExcludesProcessingHistory(t *testing.T) {
	h := newHarness(t, nil, nil)
	doc := h.d.document
	first := filepath.Join(recordingFolder(h.d.directory), "primeiro.wav")
	second := filepath.Join(recordingFolder(h.d.directory), "segundo.wav")
	h.d.displayDocumentResult(doc, resultFor(first, "Versão inicial."), false, "Versão inicial.", false)
	doc.editor.SetText("Revisão intermediária descartada.")
	doc.editor.SetText("Primeira etapa final.")
	if !h.d.preserveAudioHistory(doc) {
		t.Fatal("não foi possível preservar a etapa anterior")
	}
	h.d.displayDocumentResult(doc, resultFor(second, "Segunda etapa final."), false, "Segunda etapa final.", false)
	doc.nextPrompt++
	doc.promptSteps = append(doc.promptSteps, promptStep{
		ID: doc.nextPrompt, Conversation: 1,
		Prompt: "Prompt exclusivo de processamento.", Response: "Resultado exclusivo de processamento.",
		Sent: true, Processed: true,
	})
	doc.promptCursor = len(doc.promptSteps) - 1
	context := h.d.evaluationContext(doc)
	if len(context) != 2 || context[0].Source != first || context[0].Text != "Primeira etapa final." ||
		context[1].Source != second || context[1].Text != "Segunda etapa final." ||
		context[0].PromptID == 0 || context[1].PromptID == 0 {
		t.Fatalf("as etapas não mantiveram texto final e vínculo próprio com áudio/prompt: %+v", context)
	}
	payload, err := json.Marshal(context)
	if err != nil {
		t.Fatal(err)
	}
	for _, excluded := range []string{"Versão inicial.", "Revisão intermediária", "Prompt exclusivo", "Resultado exclusivo"} {
		if strings.Contains(string(payload), excluded) {
			t.Fatalf("o contexto incluiu revisão intermediária ou Processar/Mesclar: %s", excluded)
		}
	}
}

func TestTranscriptEvaluationKeepsImmutableContextAfterEditingAndClosingWhileBrowsing(t *testing.T) {
	h := newHarness(t, nil, nil)
	doc := seedThreePrompts(t, h)
	context := h.d.evaluationContext(doc)
	doc.evaluation = &transcriptEvaluation{
		Score: 3, Comment: "Sobre a versão avaliada.", Reference: evaluationReference(context),
		UpdatedAt: h.d.deps.now(), Context: cloneEvaluationContext(context),
	}
	reference := doc.evaluation.Reference
	doc.editor.SetText("Texto editado depois da nota.")
	h.d.syncTranscriptEvaluation(doc)
	if doc.evaluation == nil || doc.evaluation.Reference != reference ||
		!slices.ContainsFunc(doc.evaluation.Context, func(stage evaluationStage) bool { return stage.Text == "Prompt 3" }) {
		t.Fatal("editar modificou ou apagou o conjunto imutável da nota")
	}
	h.d.navigatePrompt(doc, -1)
	if !doc.browsingPrompts() {
		t.Fatal("o teste não consultou uma etapa histórica")
	}
	h.d.closeDocument(doc.item)
	archive, err := h.d.readEvaluationArchive()
	if err != nil {
		t.Fatal(err)
	}
	if len(archive.Entries) != 1 || archive.Entries[0].Evaluation.Reference != reference ||
		evaluationReference(archive.Entries[0].Context) != reference {
		t.Fatal("fechar durante consulta perdeu ou trocou a referência da avaliação")
	}
}

func TestLegacyTranscriptEvaluationMigratesReferenceWithoutLosingScoreOrComment(t *testing.T) {
	h := newHarness(t, nil, nil)
	doc := h.d.document
	source := filepath.Join(recordingFolder(h.d.directory), "legado.wav")
	h.d.displayDocumentResult(doc, resultFor(source, "Texto legado."), false, "Texto legado.", false)
	stages := h.d.evaluationContext(doc)
	for index := range stages {
		stages[index].PromptID = 0
		stages[index].ModelID, stages[index].JobID, stages[index].RunID = "", "", ""
	}
	doc.evaluation = &transcriptEvaluation{
		Score: 0, Comment: "Nota legada válida.", Reference: evaluationReference(stages), UpdatedAt: h.d.deps.now(),
	}
	h.d.syncTranscriptEvaluation(doc)
	if doc.evaluation == nil || doc.evaluation.Score != 0 ||
		doc.evaluation.Comment != "Nota legada válida." || len(doc.evaluation.Context) == 0 ||
		!validTranscriptEvaluation(doc.evaluation) {
		t.Fatal("a atualização do formato de contexto descartou uma avaliação legada válida")
	}
}

func evaluationDialogButton(t *testing.T, h *testHarness, text string) *widget.Button {
	t.Helper()
	overlay := h.d.window.Canvas().Overlays().Top()
	if overlay != nil {
		for _, object := range test.LaidOutObjects(overlay) {
			if button, ok := object.(*widget.Button); ok && button.Text == text {
				return button
			}
		}
	}
	t.Fatalf("o diálogo de avaliação não ofereceu %q", text)
	return nil
}
