package desktop

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ai-talker/internal/live"
	"ai-talker/internal/recorder"
	"ai-talker/internal/store"
	"ai-talker/internal/transcript"

	"fyne.io/fyne/v2/test"
)

func TestCleanupScannerGroupsTeamTracksAndProtectsExportsAndImports(t *testing.T) {
	h := newHarness(t, nil, nil)
	screenshotRoot := filepath.Join(h.d.directory, "screenshots")
	screenshot := filepath.Join(screenshotRoot, "2026-10-01", "shot.png")
	writeCleanupFile(t, screenshot, []byte("png"))

	recordingRoot := recordingFolder(h.d.directory)
	microphone := filepath.Join(recordingRoot, "2026-10-01", "20261001-101010-mic.wav")
	system := filepath.Join(recordingRoot, "2026-10-01", "20261001-101010-sis.wav")
	writeCleanupFile(t, microphone, []byte("mic"))
	writeCleanupFile(t, system, []byte("system"))
	if err := store.WriteJSON(microphone+".team.json", recorder.TeamManifest{
		Version: 1,
		Tracks: []recorder.Track{
			{ID: "microphone", Snapshot: recorder.Snapshot{Path: microphone}},
			{ID: "system", Snapshot: recorder.Snapshot{Path: system}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	imported := filepath.Join(recordingRoot, "importados", "user-audio.wav")
	writeCleanupFile(t, imported, []byte("import"))
	export := filepath.Join(h.d.directory, "transcricoes", "export.txt")
	writeCleanupFile(t, export, []byte("export"))

	screenshots, err := h.d.scanCleanupFiles(cleanupScreenshots)
	if err != nil {
		t.Fatal(err)
	}
	if len(screenshots) != 1 || screenshots[0].Source != screenshot {
		t.Fatalf("a lista de screenshots saiu da pasta interna: %+v", screenshots)
	}
	recordings, err := h.d.scanCleanupFiles(cleanupRecordings)
	if err != nil {
		t.Fatal(err)
	}
	if len(recordings) != 1 || len(recordings[0].Paths) != 3 ||
		!slices.Contains(recordings[0].Paths, microphone) ||
		!slices.Contains(recordings[0].Paths, system) ||
		!slices.Contains(recordings[0].Paths, microphone+".team.json") {
		t.Fatalf("as trilhas de Equipe não foram agrupadas com o manifesto: %+v", recordings)
	}
	if slices.Contains(recordings[0].Paths, imported) || slices.Contains(recordings[0].Paths, export) {
		t.Fatal("a limpeza incluiu áudio importado ou exportação do usuário")
	}
}

func TestCleanupPeriodsUseRecentDaysAndInclusiveCustomDates(t *testing.T) {
	h := newHarness(t, nil, nil)
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.Local)
	_, _, cutoff, err := h.d.cleanupPeriod("Últimos 30 dias", "", "", now)
	if err != nil {
		t.Fatal(err)
	}
	if !cleanupPeriodMatches(now.AddDate(0, 0, -29), "Últimos 30 dias", time.Time{}, time.Time{}, cutoff, now) ||
		cleanupPeriodMatches(now.AddDate(0, 0, -31), "Últimos 30 dias", time.Time{}, time.Time{}, cutoff, now) ||
		cleanupPeriodMatches(now.Add(time.Hour), "Últimos 30 dias", time.Time{}, time.Time{}, cutoff, now) {
		t.Fatal("o filtro dos últimos 30 dias incluiu dados antigos/futuros ou excluiu dados recentes")
	}
	from, until, _, err := h.d.cleanupPeriod(cleanupCustomPeriod, "2026-10-01", "2026-10-03", now)
	if err != nil {
		t.Fatal(err)
	}
	for _, date := range []string{"2026-10-01", "2026-10-03"} {
		value, _ := time.ParseInLocation("2006-01-02", date, time.Local)
		if !cleanupPeriodMatches(value.Add(12*time.Hour), cleanupCustomPeriod, from, until, time.Time{}, now) {
			t.Fatalf("a data inclusiva %s foi excluída", date)
		}
	}
	if _, _, _, err := h.d.cleanupPeriod(cleanupCustomPeriod, "11/10/2026", "2026-10-11", now); err == nil {
		t.Fatal("uma data fora do formato AAAA-MM-DD foi aceita")
	}
}

func TestTranscriptCleanupKeepsAudioAndClearsTextCacheAndEvaluation(t *testing.T) {
	h := newHarness(t, nil, nil)
	doc := h.d.document
	doc.lastAudio = filepath.Join(h.d.directory, "gravacoes", "audio.wav")
	doc.lastResult = &transcript.Result{Source: doc.lastAudio, Text: "Texto reconhecido."}
	doc.editor.SetText("Texto reconhecido.")
	doc.recordedAppend = nil
	doc.audioHistory = []audioSnapshot{{
		Source: doc.lastAudio, Text: "Texto reconhecido.", Result: doc.lastResult,
		SavedAt: time.Now(),
	}}
	doc.evaluation = &transcriptEvaluation{
		Score: 5, Reference: h.d.transcriptEvaluationReference(doc), UpdatedAt: time.Now(),
	}

	h.d.clearTranscriptForCleanup(cleanupItem{
		Category: cleanupTranscriptions, DocID: doc.id, Source: doc.lastAudio, Current: true, Index: 0,
	})

	if doc.lastAudio == "" || doc.audioMissing || doc.editor.Text != "" || doc.lastResult != nil ||
		doc.evaluation != nil || doc.audioHistory[0].Result != nil || doc.audioHistory[0].Text != "" {
		t.Fatalf("a limpeza de transcrição apagou áudio ou deixou texto/avaliação: doc=%+v history=%+v",
			doc, doc.audioHistory[0])
	}
}

func TestScreenshotCleanupLeavesAVisibleRemovedMarkerAndDropsRating(t *testing.T) {
	h := newHarness(t, nil, nil)
	doc := h.d.document
	doc.lastAudio = filepath.Join(h.d.directory, "gravacoes", "aula.wav")
	path := filepath.Join(h.d.directory, "screenshots", "2026-10-01", "captura.png")
	text := "Fala reconhecida.\n\n" + screenshotLinkHeading + "\n" + path
	doc.lastResult = &transcript.Result{Source: doc.lastAudio, Text: text}
	doc.editor.SetText(text)
	doc.screenshotFiles = []string{path}
	doc.audioHistory = []audioSnapshot{{Source: doc.lastAudio, Text: text, ScreenshotFiles: []string{path}}}
	doc.evaluation = &transcriptEvaluation{
		Score: 3, Reference: h.d.transcriptEvaluationReference(doc), UpdatedAt: time.Now(),
	}

	h.d.markScreenshotRemoved(path)

	if !strings.Contains(doc.editor.Text, "[Screenshot removida]") ||
		strings.Contains(doc.editor.Text, path) || len(doc.screenshotFiles) != 0 ||
		doc.evaluation != nil || len(doc.audioHistory[0].ScreenshotFiles) != 0 ||
		!strings.Contains(doc.audioHistory[0].Text, "[Screenshot removida]") {
		t.Fatalf("a limpeza não preservou o histórico com marcador: texto=%q histórico=%+v",
			doc.editor.Text, doc.audioHistory[0])
	}
}

func TestAudioCleanupMarksCurrentAndHistoricalAudioUnavailable(t *testing.T) {
	h := newHarness(t, nil, nil)
	doc := h.d.document
	path := filepath.Join(h.d.directory, "gravacoes", "aula.wav")
	doc.lastAudio = path
	doc.lastResult = &transcript.Result{Source: path, Text: "Transcrição preservada."}
	doc.editor.SetText("Transcrição preservada.")
	doc.audioHistory = []audioSnapshot{{Source: path, Text: "Transcrição preservada."}}
	doc.evaluation = &transcriptEvaluation{
		Score: 5, Reference: h.d.transcriptEvaluationReference(doc), UpdatedAt: time.Now(),
	}

	h.d.markAudioRemoved(cleanupItem{Source: path, Paths: []string{path}})

	if !doc.audioMissing || doc.evaluation != nil || !doc.audioHistory[0].AudioMissing ||
		doc.lastAudio != path || doc.editor.Text != "Transcrição preservada." {
		t.Fatalf("a limpeza não marcou o áudio ausente preservando a transcrição: %+v", doc)
	}
}

func TestCleanupPreventsSelectingPlayerFileAndAllowsOtherRecordings(t *testing.T) {
	h := newHarness(t, nil, nil)
	doc := h.d.document
	current := filepath.Join(recordingFolder(h.d.directory), "current.wav")
	other := filepath.Join(recordingFolder(h.d.directory), "other.wav")
	doc.lastAudio = current
	h.d.playback = &audioPlayback{document: doc, source: current, player: newFakeAudioPlayer()}
	h.d.openSettings()
	form := h.d.settingsForm.cleanup
	h.await(func() bool { return !form.busy })
	form.items = []cleanupItem{
		{ID: "current", Category: cleanupRecordings, Source: current, Paths: []string{current}, Name: "Atual"},
		{ID: "other", Category: cleanupRecordings, Source: other, Paths: []string{other}, Name: "Outra"},
	}
	h.d.renderCleanupItems(form)
	if !form.checks["current"].Disabled() || form.selected["current"] {
		t.Fatal("o arquivo aberto no player permaneceu selecionável para limpeza")
	}
	if form.checks["other"].Disabled() || !form.selected["other"] {
		t.Fatal("a reprodução bloqueou também um arquivo independente")
	}
	test.Tap(form.all)
	test.Tap(form.all)
	if form.selected["current"] || !form.selected["other"] {
		t.Fatal("Selecionar todos incluiu o item em uso ou perdeu o item livre")
	}
	h.d.playback = nil
	h.d.renderCleanupItems(form)
	if form.checks["current"].Disabled() {
		t.Fatal("o arquivo continuou bloqueado após liberar o player")
	}
}

func TestTranscriptCleanupIncludesInternalLastResultAndProtectsExports(t *testing.T) {
	h := newHarness(t, nil, nil)
	doc := h.d.document
	doc.lastAudio = filepath.Join(recordingFolder(h.d.directory), "aula.wav")
	export := filepath.Join(h.d.directory, "transcricoes", "minha-aula.txt")
	doc.lastResult = &transcript.Result{Source: doc.lastAudio, Text: "Texto interno.", Exports: map[string]string{"txt": export}}
	doc.editor.SetText("Texto interno.")
	if err := h.d.persistDocumentResult(doc, *doc.lastResult); err != nil {
		t.Fatal(err)
	}
	seeds := h.d.cleanupTranscriptSeeds()
	if len(seeds) != 1 || !slices.Contains(seeds[0].item.Paths, h.d.documentLatestPath(doc)) ||
		!slices.Contains(seeds[0].item.Paths, h.d.latestPath()) {
		t.Fatalf("as cópias internas de last-transcript ficaram fora da limpeza: %+v", seeds)
	}
	if slices.Contains(seeds[0].item.Paths, export) {
		t.Fatal("uma exportação da pessoa foi incluída na limpeza")
	}
}

func TestHistoryCleanupDoesNotRecreateTheActiveEntryAfterSavingOrReopening(t *testing.T) {
	h := newHarness(t, nil, nil)
	doc := h.d.document
	source := filepath.Join(recordingFolder(h.d.directory), "ativo.wav")
	h.d.displayDocumentResult(doc, resultFor(source, "Texto e áudio mantidos."), false, "Texto e áudio mantidos.", false)
	if !h.d.preserveAudioHistory(doc) {
		t.Fatal("o histórico atual não foi preparado")
	}
	seeds := h.d.cleanupHistorySeeds()
	if len(seeds) != 1 {
		t.Fatal("a prévia não ofereceu o histórico atual")
	}
	h.d.removeAudioHistoryForCleanup(seeds[0].item)
	for range 2 {
		if err := h.d.saveWorkspace(); err != nil {
			t.Fatal(err)
		}
		if !h.d.loadWorkspace() || h.d.workspaceInvalid {
			t.Fatal("o workspace não reabriu após limpar o histórico")
		}
		if h.d.lastAudio != source || h.d.editor.Text != "Texto e áudio mantidos." ||
			len(h.d.cleanupHistorySeeds()) != 0 || len(h.d.audioHistoryMenu(h.d.document).Items) != 0 {
			t.Fatal("salvar/reabrir ressuscitou a entrada limpa ou apagou áudio/texto atual")
		}
	}
}

func TestCleanupMaterializesEmbeddedRecordsBeforeSendingToRecycleBin(t *testing.T) {
	h := newHarness(t, nil, nil)
	payload, err := json.Marshal(audioSnapshot{Source: "test.wav", Text: "Registro interno recuperável."})
	if err != nil {
		t.Fatal(err)
	}
	called := false
	err = h.d.recycleCleanupItem(h.d.ctx, cleanupItem{
		Category: cleanupHistory, Record: payload,
	}, func(_ context.Context, paths []string) error {
		called = true
		if len(paths) != 1 || filepath.Dir(paths[0]) != filepath.Join(h.d.directory, "limpeza") {
			t.Fatal("o registro não virou um arquivo interno exclusivo para a Lixeira")
		}
		got, err := os.ReadFile(paths[0])
		if err != nil || string(got) != string(payload) {
			t.Fatal("o registro recuperável chegou incompleto à Lixeira", err)
		}
		return nil
	})
	if err != nil || !called {
		t.Fatal("a exclusão do histórico ignorou a Lixeira", err)
	}
}

func TestTranscriptCleanupScansClosedTabResultsAndOrphanCachesOnly(t *testing.T) {
	h := newHarness(t, nil, nil)
	source := filepath.Join(h.d.directory, "external-original.wav")
	job := filepath.Join(h.d.directory, "jobs-go-v1", strings.Repeat("a", 64))
	closed := filepath.Join(h.d.directory, "abas", "17", "last-transcript.json")
	if err := store.WriteJSON(filepath.Join(job, "result.json"), transcript.Result{Source: source, Text: "Cache."}); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteJSON(closed, transcript.Result{Source: source, Text: "Aba fechada."}); err != nil {
		t.Fatal(err)
	}
	writeCleanupFile(t, source, []byte("original"))
	export := filepath.Join(h.d.directory, "transcricoes", "exportado.json")
	writeCleanupFile(t, export, []byte("export"))
	items, err := h.d.scanUnreferencedTranscriptFiles(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("o scanner perdeu um resultado fechado/cache órfão: %+v", items)
	}
	for _, item := range items {
		if slices.Contains(item.Paths, source) || slices.Contains(item.Paths, export) {
			t.Fatal("uma referência de resultado levou o original/exportação para limpeza")
		}
	}
}

func TestScreenshotCleanupMarkerSurvivesLiveWorkspaceReload(t *testing.T) {
	h := newHarness(t, nil, nil)
	doc := h.d.document
	source := filepath.Join(recordingFolder(h.d.directory), "aula.wav")
	path := filepath.Join(h.d.directory, "screenshots", "aula.png")
	text := "Fala reconhecida.\n\n" + path
	h.d.displayDocumentResult(doc, resultFor(source, text), false, text, false)
	doc.screenshotFiles = []string{path}
	h.d.restoreLiveArchive(doc, &liveArchive{
		Source: source, Combined: text, InlineScreenshots: true, NextID: 2,
		Tracks: []recorder.Track{{ID: "microphone", Snapshot: recorder.Snapshot{
			Path: source, SampleRate: 16000, Duration: 1, WrittenSamples: 16000,
		}}},
		Entries: []live.Entry{
			{ID: 1, Track: "microphone", End: 1, LastSample: 16000, Text: "Fala reconhecida."},
			{ID: 2, Start: 1, End: 1, Screenshot: true, Path: path},
		},
	})
	h.d.markScreenshotRemoved(path)
	if err := h.d.saveWorkspace(); err != nil {
		t.Fatal(err)
	}
	if !h.d.loadWorkspace() || h.d.workspaceInvalid || h.d.live == nil ||
		h.d.live.Entries[1].Path != "[Screenshot removida]" ||
		!strings.Contains(h.d.editor.Text, "[Screenshot removida]") {
		t.Fatal("a marca de screenshot removida tornou o workspace inválido ou perdeu a linha do tempo")
	}
}

func TestAudioCleanupCannotRestoreAvailabilityOrEvaluationThroughUndoClose(t *testing.T) {
	now := time.Now()
	h := newHarness(t, nil, func(deps *dependencies) { deps.now = func() time.Time { return now } })
	doc := h.d.document
	source := filepath.Join(recordingFolder(h.d.directory), "fechada.wav")
	h.d.displayDocumentResult(doc, resultFor(source, "Transcrição mantida."), false, "Transcrição mantida.", false)
	context := h.d.evaluationContext(doc)
	doc.evaluation = &transcriptEvaluation{
		Score: 4, Comment: "Avaliação apagada.", Reference: evaluationReference(context),
		Context: cloneEvaluationContext(context), UpdatedAt: h.d.deps.now(),
	}
	h.d.closeDocument(doc.item)
	if err := h.d.applyCleanupItems([]cleanupItem{{
		Category: cleanupRecordings, Source: source, Paths: []string{source},
	}}); err != nil {
		t.Fatal(err)
	}
	h.d.undoCloseDocument()
	if h.d.document.lastAudio != source || !h.d.document.audioMissing ||
		h.d.editor.Text != "Transcrição mantida." || h.d.document.evaluation != nil {
		t.Fatal("Desfazer recolocou disponibilidade/nota de um áudio removido ou perdeu o texto")
	}
}

func TestTranscriptCleanupOfClosedTabCannotRestoreTextThroughUndoClose(t *testing.T) {
	now := time.Now()
	h := newHarness(t, nil, func(deps *dependencies) { deps.now = func() time.Time { return now } })
	doc := h.d.document
	source := filepath.Join(recordingFolder(h.d.directory), "fechada.wav")
	h.d.displayDocumentResult(doc, resultFor(source, "Texto que foi limpo."), false, "Texto que foi limpo.", false)
	h.d.closeDocument(doc.item)
	if err := h.d.applyCleanupItems([]cleanupItem{{
		Category: cleanupTranscriptions, Source: source, Paths: []string{h.d.documentLatestPath(doc)},
	}}); err != nil {
		t.Fatal(err)
	}
	h.d.undoCloseDocument()
	if h.d.lastAudio != source || h.d.editor.Text != "" || h.d.lastResult != nil {
		t.Fatal("Desfazer ressuscitou texto/cache de uma aba que foi limpa")
	}
}

func TestTranscriptCleanupRemovesOlderStageFromLaterAudioBaseline(t *testing.T) {
	h := newHarness(t, nil, nil)
	doc := h.d.document
	first := filepath.Join(recordingFolder(h.d.directory), "primeiro.wav")
	second := filepath.Join(recordingFolder(h.d.directory), "segundo.wav")
	doc.lastAudio = second
	doc.editor.SetText("Primeira fala.\n\nSegunda fala.")
	result := resultFor(second, doc.editor.Text)
	doc.lastResult = &result
	doc.recordedAppend = &recordedAppend{
		Source: second, Prefix: "Primeira fala.", Content: doc.editor.Text, Combined: doc.editor.Text, LinksReady: true,
	}
	doc.audioHistory = []audioSnapshot{
		{Source: first, Text: "Primeira fala.", SavedAt: h.d.deps.now()},
		{Source: second, Text: doc.editor.Text, Append: doc.recordedAppend, Result: doc.lastResult, SavedAt: h.d.deps.now()},
	}
	h.d.clearTranscriptForCleanup(cleanupItem{Category: cleanupTranscriptions, DocID: doc.id, Source: first})
	if doc.lastAudio != second || doc.editor.Text != "Segunda fala." ||
		doc.recordedAppend.Prefix != "" || doc.audioHistory[1].Text != "Segunda fala." ||
		doc.lastResult == nil || doc.lastResult.Text != "Segunda fala." {
		t.Fatal("o texto limpo da primeira etapa permaneceu no editor/baseline/histórico de um áudio posterior")
	}
}

func writeCleanupFile(t *testing.T, path string, payload []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
}
