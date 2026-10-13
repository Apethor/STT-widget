package desktop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"ai-talker/internal/config"
	"ai-talker/internal/live"
	"ai-talker/internal/recorder"
	"ai-talker/internal/transcript"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func historyAudio(t *testing.T, directory, name string) string {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte("áudio sintético preservado"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestConsecutiveRecordingsCanRestoreBothAudiosWithoutDuplicatingRetry(t *testing.T) {
	sequence := 0
	h := newHarness(t, runnerFunc(func(_ context.Context, source string, _ config.Settings, _ transcript.Request) (transcript.Result, error) {
		return resultFor(source, filepath.Base(source)), nil
	}), func(deps *dependencies) {
		deps.newRecorder = func(_ int, directory string) recording {
			sequence++
			if err := os.MkdirAll(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			source := historyAudio(t, directory, fmt.Sprintf("audio-%d.wav", sequence))
			return &fakeRecording{state: recorder.Snapshot{Path: source, Duration: 5}}
		}
	})
	for range 2 {
		test.Tap(h.d.recordButton)
		h.await(func() bool { return h.d.capture == captureRunning })
		test.Tap(h.d.recordButton)
		h.await(func() bool { return h.d.capture == captureIdle && !h.d.busy })
	}
	newer := h.d.lastAudio
	newerText := h.d.editor.Text
	if newerText != "audio-1.wav\n\naudio-2.wav" || len(h.d.audioHistory) != 1 {
		t.Fatal("a segunda gravação não preservou a primeira no histórico", newerText)
	}
	h.d.processEditor.SetText("Resultado preservado ao escolher um áudio.")
	test.Tap(h.d.audioHistoryButton)
	menu := h.d.window.Canvas().Focused().(*widget.PopUpMenu)
	menu.TypedKey(&fyne.KeyEvent{Name: fyne.KeyDown})
	menu.TypedKey(&fyne.KeyEvent{Name: fyne.KeyDown})
	menu.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	h.settle()
	older := h.d.lastAudio
	if filepath.Base(older) != "audio-1.wav" || h.d.editor.Text != "audio-1.wav" ||
		h.d.processEditor.Text != "Resultado preservado ao escolher um áudio." || len(h.d.audioHistory) != 2 {
		t.Fatal("escolher o áudio anterior perdeu a transcrição correspondente ou o áudio mais novo")
	}
	h.d.retryDocument(h.d.document)
	h.await(func() bool { return !h.d.busy })
	if h.d.editor.Text != "audio-1.wav" {
		t.Fatal("retomar o áudio anterior duplicou sua transcrição", h.d.editor.Text)
	}
	h.d.restoreAudioSnapshot(h.d.document, newer)
	h.settle()
	if h.d.lastAudio != newer || h.d.editor.Text != newerText {
		t.Fatal("o histórico não permitiu voltar à gravação mais nova")
	}
	h.d.newAudio()
	if h.d.lastAudio != "" || h.d.editor.Text != "" || h.d.audioHistoryButton.Disabled() {
		t.Fatal("Novo áudio perdeu o histórico ou deixou de limpar a aba")
	}
	if err := h.d.saveWorkspace(); err != nil {
		t.Fatal(err)
	}
	if !h.d.loadWorkspace() || len(h.d.audioHistory) != 2 || h.d.lastAudio != "" {
		t.Fatal("reabrir o workspace não preservou o histórico da aba vazia")
	}
	h.d.restoreAudioSnapshot(h.d.document, newer)
	h.settle()
	if h.d.editor.Text != newerText {
		t.Fatal("reabrir impediu recuperar o texto da gravação mais nova")
	}
	requireFile(t, older, "áudio sintético preservado")
	requireFile(t, newer, "áudio sintético preservado")
}

func TestAudioHistoryPagesStayInsideCanvasAndReachTheOldestAudio(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.d.window.Resize(fyne.NewSize(320, 700))
	for index := range 25 {
		source := historyAudio(t, h.d.directory, fmt.Sprintf("audio-%02d.wav", index+1))
		result := resultFor(source, fmt.Sprintf("Fala %d", index+1))
		h.d.audioHistory = append(h.d.audioHistory, audioSnapshot{
			Source: source, SavedAt: h.d.deps.now(), Text: result.Text, Result: &result, Timer: "00:05",
		})
	}
	oldest := h.d.audioHistory[0].Source
	h.d.lastAudio = h.d.audioHistory[24].Source
	h.d.lastResult = h.d.audioHistory[24].Result
	h.d.editor.SetText(h.d.lastResult.Text)
	h.d.syncControls()
	test.Tap(h.d.audioHistoryButton)
	for page, count := range []int{11, 12, 6} {
		menu := h.d.window.Canvas().Focused().(*widget.PopUpMenu)
		if len(menu.Items) != count || menu.Position().X < 0 || menu.Position().Y < 0 ||
			menu.Position().X+menu.Size().Width > 321 || menu.Position().Y+menu.Size().Height > 701 {
			t.Fatal("a página do histórico saiu da janela mínima", page, len(menu.Items), menu.Position(), menu.Size())
		}
		for range len(menu.Items) {
			menu.TypedKey(&fyne.KeyEvent{Name: fyne.KeyDown})
		}
		menu.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
		h.settle()
	}
	if h.d.lastAudio != oldest || h.d.editor.Text != "Fala 1" || len(h.d.audioHistory) != 25 {
		t.Fatal("a navegação não recuperou o áudio mais antigo ou perdeu as outras entradas")
	}
}

func TestAudioHistoryRestoresPendingTimelineAndRejectsLatePreviousJob(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	h := newHarness(t, runnerFunc(func(_ context.Context, source string, _ config.Settings, _ transcript.Request) (transcript.Result, error) {
		if filepath.Base(source) == "anterior.wav" {
			started <- struct{}{}
			<-release
			return resultFor(source, "Resposta tardia que não pode substituir o histórico."), nil
		}
		return resultFor(source, "Áudio seguinte."), nil
	}), nil)
	folder := filepath.Join(h.d.directory, "gravacoes")
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	older := historyAudio(t, folder, "anterior.wav")
	newer := historyAudio(t, folder, "seguinte.wav")
	h.d.startDocumentJob(h.d.document, older)
	waitValue(t, started)
	h.d.editor.SetText("Trecho anterior já aceito.")
	archive := &liveArchive{
		Source: older, Prefix: "", Combined: h.d.editor.Text, TranscribeOnStop: true, NextID: 2,
		Tracks: []recorder.Track{{ID: "microphone", Snapshot: recorder.Snapshot{
			Path: older, SampleRate: 16000, WrittenSamples: 160000, Duration: 10,
		}}},
		Entries: []live.Entry{
			{ID: 1, Track: "microphone", Start: 0, End: 5, FirstSample: 0, LastSample: 80000, Text: "Trecho anterior já aceito."},
			{ID: 2, Track: "microphone", Start: 5, End: 10, FirstSample: 80000, LastSample: 160000, Pending: true, Error: "Tempo limite."},
		},
	}
	h.d.restoreLiveArchive(h.d.document, archive)
	h.d.startImportedJob(h.d.document, newer)
	h.await(func() bool { return !h.d.busy })
	h.d.restoreAudioSnapshot(h.d.document, older)
	h.await(func() bool { return !h.d.audioLoading })
	if h.d.lastAudio != older || h.d.editor.Text != "Trecho anterior já aceito." || h.d.live == nil ||
		len(h.d.live.Entries) != 2 || h.d.live.Entries[0].Pending || !h.d.live.Entries[1].Pending ||
		!h.d.retryButton.Visible() || h.d.live.Source != older {
		t.Fatal("o histórico esqueceu partes aceitas ou pendentes da gravação anterior")
	}
	unblock()
	h.settle()
	if h.d.editor.Text != "Trecho anterior já aceito." || h.d.live.Entries[0].Text != "Trecho anterior já aceito." {
		t.Fatal("a resposta tardia substituiu o estado restaurado")
	}
}

func TestMissingHistoryAudioKeepsCurrentFileEditorsAndRecoveryEntries(t *testing.T) {
	h := newHarness(t, nil, nil)
	current := historyAudio(t, h.d.directory, "atual.wav")
	h.d.showResult(resultFor(current, "Texto atual."), false)
	missing := filepath.Join(h.d.directory, "ausente.wav")
	h.d.audioHistory = []audioSnapshot{{Source: missing, Text: "Texto anterior.", Timer: "00:05"}}
	h.d.restoreAudioSnapshot(h.d.document, missing)
	h.settle()
	if h.d.lastAudio != current || h.d.editor.Text != "Texto atual." || len(h.d.audioHistory) != 2 ||
		!h.d.errorLabel.Visible() || !strings.Contains(h.d.errorLabel.Text, "áudio anterior") {
		t.Fatal("um arquivo ausente apagou o áudio/texto atual ou seu histórico")
	}
}

func TestAudioHistoryWriteFailurePreventsReplacingCurrentSource(t *testing.T) {
	h := newHarness(t, nil, nil)
	current := historyAudio(t, h.d.directory, "atual.wav")
	next := historyAudio(t, h.d.directory, "seguinte.wav")
	h.d.showResult(resultFor(current, "Texto atual."), false)
	if err := os.Mkdir(filepath.Join(h.d.directory, "workspace-go-v1.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	h.d.startImportedJob(h.d.document, next)
	if h.d.lastAudio != current || h.d.editor.Text != "Texto atual." || h.d.busy || !h.d.errorLabel.Visible() {
		t.Fatal("a falha de preservar o histórico permitiu substituir o áudio")
	}
	h.d.newAudio()
	if h.d.lastAudio != current || h.d.editor.Text != "Texto atual." {
		t.Fatal("Novo áudio ignorou a falha de preservar a origem")
	}
	h.d.startCapture()
	if h.d.capture != captureIdle || h.d.lastAudio != current {
		t.Fatal("uma nova captura ignorou a falha de preservar a origem")
	}
}

func TestHistoryLoadBelongsToItsTabAndNewAudioRejectsQueuedRestore(t *testing.T) {
	h := newHarness(t, nil, nil)
	first := h.d.document
	older := historyAudio(t, h.d.directory, "anterior.wav")
	current := historyAudio(t, h.d.directory, "atual.wav")
	h.d.showResult(resultFor(older, "Texto anterior."), false)
	h.d.startImportedJob(first, current)
	h.await(func() bool { return !first.busy })
	h.d.restoreAudioSnapshot(first, older)
	h.d.newAudio()
	h.d.editor.SetText("Edição depois de Novo áudio.")
	h.settle()
	if first.lastAudio != "" || first.editor.Text != "Edição depois de Novo áudio." {
		t.Fatal("um carregamento antigo ressuscitou o áudio depois de Novo áudio")
	}
	h.d.restoreAudioSnapshot(first, older)
	second := h.d.addGroup()
	second.editor.SetText("Outra aba preservada.")
	h.settle()
	if first.lastAudio != older || first.editor.Text != "Texto anterior." || h.d.document != second ||
		second.lastAudio != "" || second.editor.Text != "Outra aba preservada." {
		t.Fatal("trocar de aba redirecionou o carregamento do histórico")
	}
}

func TestDeletingScreenshotDoesNotResurrectItsLinkFromAudioHistory(t *testing.T) {
	h := newHarness(t, nil, nil)
	older := historyAudio(t, h.d.directory, "anterior.wav")
	current := historyAudio(t, h.d.directory, "atual.wav")
	first, second := filepath.Join(h.d.directory, "screenshots", "primeira.png"), filepath.Join(h.d.directory, "screenshots", "segunda.png")
	h.d.showResult(resultFor(current, "Texto atual."), false)
	h.d.audioHistory = []audioSnapshot{{
		Source: older, Text: "Fala anterior.\n\n" + screenshotFooter([]string{first, second}),
		ScreenshotFiles: []string{first, second},
		Append: &recordedAppend{
			Source: older, Combined: "Fala anterior.\n\n" + screenshotFooter([]string{first, second}),
			Content: "Fala anterior.", Screenshots: []string{first, second}, LinksReady: true,
		},
	}}
	h.d.removeScreenshotReference(nil, first)
	h.d.restoreAudioSnapshot(h.d.document, older)
	h.settle()
	if strings.Contains(h.d.editor.Text, first) || !strings.Contains(h.d.editor.Text, second) ||
		len(h.d.screenshotFiles) != 1 || len(h.d.recordedAppend.Screenshots) != 1 ||
		h.d.recordedAppend.Screenshots[0] != second {
		t.Fatal("o histórico ressuscitou a imagem excluída ou removeu a outra screenshot")
	}
}

func TestHistoryLoadKeepsTextEditedWhileFileValidationIsPending(t *testing.T) {
	h := newHarness(t, nil, nil)
	older := historyAudio(t, h.d.directory, "anterior.wav")
	current := historyAudio(t, h.d.directory, "atual.wav")
	h.d.showResult(resultFor(current, "Texto atual."), false)
	h.d.audioHistory = []audioSnapshot{{Source: older, Text: "Texto anterior."}}
	h.d.restoreAudioSnapshot(h.d.document, older)
	h.d.editor.SetText("Edição feita durante a troca.")
	h.settle()
	if h.d.lastAudio != current || h.d.editor.Text != "Edição feita durante a troca." {
		t.Fatal("a recuperação pendente substituiu uma edição nova")
	}
}

func TestUndoClearPreservesTheFollowingAudioInHistory(t *testing.T) {
	h := newHarness(t, nil, nil)
	older := historyAudio(t, h.d.directory, "anterior.wav")
	current := historyAudio(t, h.d.directory, "atual.wav")
	h.d.showResult(resultFor(older, "Texto anterior."), false)
	h.d.clearText(true)
	h.d.startImportedJob(h.d.document, current)
	h.await(func() bool { return !h.d.busy })
	currentText := h.d.editor.Text
	h.d.undoClear()
	if h.d.lastAudio != older || h.d.editor.Text != "Texto anterior." {
		t.Fatal("desfazer não restaurou a origem da limpeza")
	}
	h.d.restoreAudioSnapshot(h.d.document, current)
	h.settle()
	if h.d.lastAudio != current || h.d.editor.Text != currentText {
		t.Fatal("desfazer perdeu o áudio seguinte do histórico")
	}
}

func TestHistoryLoadUsesScreenshotLinksUpdatedDuringValidation(t *testing.T) {
	h := newHarness(t, nil, nil)
	older := historyAudio(t, h.d.directory, "anterior.wav")
	current := historyAudio(t, h.d.directory, "atual.wav")
	path := filepath.Join(h.d.directory, "screenshots", "excluida.png")
	h.d.showResult(resultFor(current, "Texto atual."), false)
	h.d.audioHistory = []audioSnapshot{{
		Source: older, Text: "Fala anterior.\n\n" + screenshotFooter([]string{path}),
		ScreenshotFiles: []string{path},
	}}
	h.d.restoreAudioSnapshot(h.d.document, older)
	h.d.removeScreenshotReference(nil, path)
	h.settle()
	if h.d.lastAudio != older || strings.Contains(h.d.editor.Text, path) || len(h.d.screenshotFiles) != 0 {
		t.Fatal("o carregamento recolocou uma screenshot excluída durante a validação")
	}
}

func TestPendingScreenshotFollowsItsAudioThroughHistoryRestoration(t *testing.T) {
	for _, beforeRestore := range []bool{false, true} {
		t.Run(fmt.Sprintf("finish-before-restore=%t", beforeRestore), func(t *testing.T) {
			testPendingHistoryScreenshot(t, beforeRestore)
		})
	}
}

func testPendingHistoryScreenshot(t *testing.T, beforeRestore bool) {
	h := newHarness(t, nil, nil)
	folder := filepath.Join(h.d.directory, "gravacoes")
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	older := historyAudio(t, folder, "anterior.wav")
	current := historyAudio(t, folder, "atual.wav")
	h.d.showResult(resultFor(older, "Fala anterior."), false)
	h.d.recordedAppend = &recordedAppend{Source: older, Combined: h.d.editor.Text, Content: h.d.editor.Text, LinksReady: true}
	h.d.restoreLiveArchive(h.d.document, &liveArchive{
		Source: older, Combined: h.d.editor.Text, InlineScreenshots: true, TranscribeOnStop: true, NextID: 2,
		Tracks: []recorder.Track{{ID: "microphone", Snapshot: recorder.Snapshot{
			Path: older, SampleRate: 16000, WrittenSamples: 16000, Duration: 1,
		}}},
		Entries: []live.Entry{
			{ID: 1, Track: "microphone", Start: 0, End: 1, FirstSample: 0, LastSample: 16000, Text: "Fala anterior."},
			{ID: 2, Start: 1, End: 1, Screenshot: true, Pending: true},
		},
	})
	owner := &screenshotAssociation{document: h.d.document, source: older, live: h.d.live, marker: 2}
	h.d.startImportedJob(h.d.document, current)
	h.await(func() bool { return !h.d.busy })
	path := filepath.Join(h.d.directory, "screenshots", "pendente.png")
	if beforeRestore {
		currentText := h.d.editor.Text
		h.d.attachScreenshot(owner, path)
		if h.d.lastAudio != current || h.d.editor.Text != currentText {
			t.Fatal("a seleção do áudio anterior alterou o áudio atual")
		}
	}
	h.d.restoreAudioSnapshot(h.d.document, older)
	h.settle()
	if !beforeRestore {
		h.d.attachScreenshot(owner, path)
	}
	h.settle()
	if h.d.live == nil || h.d.live == owner.live || h.d.live.Entries[1].Pending ||
		h.d.live.Entries[1].Path != path || !strings.Contains(h.d.editor.Text, path) {
		t.Fatal("a seleção pendente perdeu seu marcador ao restaurar o áudio")
	}
}

func TestHistoryScreenshotKeepsPreviousTextUntilRetranscriptionFinishes(t *testing.T) {
	for _, inline := range []bool{false, true} {
		for _, beforeRestore := range []bool{false, true} {
			for _, edited := range []bool{false, true} {
				for _, olderArchive := range []bool{false, true} {
					t.Run(fmt.Sprintf("inline=%t/before-restore=%t/edited=%t/older-archive=%t", inline, beforeRestore, edited, olderArchive), func(t *testing.T) {
						testHistoryScreenshotDuringRetranscription(t, inline, beforeRestore, edited, olderArchive)
					})
				}
			}
		}
	}
}

func testHistoryScreenshotDuringRetranscription(t *testing.T, inline, beforeRestore, edited, olderArchive bool) {
	t.Helper()
	requests := make(chan struct{}, 4)
	h := newHarness(t, runnerFunc(func(ctx context.Context, _ string, _ config.Settings, _ transcript.Request) (
		transcript.Result, error,
	) {
		requests <- struct{}{}
		<-ctx.Done()
		return transcript.Result{}, ctx.Err()
	}), nil)
	older := filepath.Join(recordingFolder(h.d.directory), "anterior.wav")
	streamFixtureSeconds(t, older, 6)
	current := historyAudio(t, h.d.directory, "atual.wav")
	archive := &liveArchive{
		Source: older, Prefix: "Texto mais antigo.", InlineScreenshots: inline, NextID: 3,
		Tracks: []recorder.Track{{ID: "microphone", Snapshot: recorder.Snapshot{
			Path: older, SampleRate: 16000, WrittenSamples: 96000, Duration: 6,
		}}},
		Entries: []live.Entry{
			{ID: 1, Track: "microphone", End: 2, LastSample: 32000, Text: "Primeira fala preservada."},
			{ID: 2, Start: 2, End: 2, Screenshot: true, Pending: true},
			{ID: 3, Track: "microphone", Start: 2, End: 6, FirstSample: 32000, LastSample: 96000,
				Text: "Segunda fala preservada."},
		},
	}
	archive.Combined = live.Render(archive.Prefix, (&liveRecording{liveArchive: *archive}).textBlocks())
	h.d.showResult(resultFor(older, archive.Combined), false)
	h.d.recordedAppend = &recordedAppend{Source: older, Combined: archive.Combined, Content: archive.Combined, LinksReady: true}
	h.d.restoreLiveArchive(h.d.document, archive)
	owner := &screenshotAssociation{document: h.d.document, source: older, live: h.d.live, marker: 2}
	firstSpeech := "Primeira fala preservada."
	if edited {
		firstSpeech = "Fala editada pelo usuário."
		h.d.editor.SetText(strings.Replace(h.d.editor.Text, "Primeira fala preservada.", firstSpeech, 1))
	}
	currentResult := resultFor(current, "Texto do áudio atual.")
	h.d.audioHistory = []audioSnapshot{{Source: current, Text: currentResult.Text, Result: &currentResult}}
	h.d.retranscribeDocument(h.d.document)
	waitValue(t, requests)
	if inline {
		waitValue(t, requests)
	}
	h.d.restoreAudioSnapshot(h.d.document, current)
	h.settle()
	if h.d.lastAudio != current {
		t.Fatal("a troca para outro áudio não terminou")
	}
	if olderArchive {
		for index := range h.d.audioHistory {
			if sameAudio(h.d.audioHistory[index].Source, older) {
				h.d.audioHistory[index].Live.PreviousEntries = nil
			}
		}
	}
	path := filepath.Join(h.d.directory, "screenshots", "pendente.png")
	if beforeRestore {
		h.d.attachScreenshot(owner, path)
		if h.d.editor.Text != "Texto do áudio atual." {
			t.Fatal("a screenshot do histórico alterou o áudio atual")
		}
	}
	h.d.restoreAudioSnapshot(h.d.document, older)
	h.settle()
	if !beforeRestore {
		h.d.attachScreenshot(owner, path)
	}
	h.settle()
	want := "Texto mais antigo.\n\n" + firstSpeech + " Segunda fala preservada.\n\n" + screenshotFooter([]string{path})
	if inline && !olderArchive {
		want = "Texto mais antigo.\n\n" + firstSpeech + "\n\n" + path + "\n\nSegunda fala preservada."
	} else if inline {
		want = "Texto mais antigo.\n\n" + firstSpeech + " Segunda fala preservada.\n\n" + path
	}
	if h.d.editor.Text != want {
		t.Fatalf("a screenshot substituiu a transcrição anterior durante a nova tentativa: %q", h.d.editor.Text)
	}
	if err := h.d.saveWorkspace(); err != nil {
		t.Fatal(err)
	}
	restored := newHarness(t, runnerFunc(func(_ context.Context, source string, _ config.Settings, _ transcript.Request) (
		transcript.Result, error,
	) {
		text := "Nova fala antes da imagem."
		if !inline {
			text = "Nova fala integral."
		} else if !strings.Contains(filepath.Base(source), "-000000000000-") {
			text = "Nova fala depois da imagem."
		}
		return resultFor(source, text), nil
	}), nil)
	restored.d.directory = h.d.directory
	if !restored.d.loadWorkspace() || restored.d.editor.Text != want {
		t.Fatal("reabrir perdeu a transcrição preservada e a screenshot")
	}
	test.Tap(restored.d.retryButton)
	restored.await(func() bool { return !restored.d.busy })
	restored.settle()
	got := restored.d.editor.Text
	if strings.Contains(got, "preservada") || edited && !strings.Contains(got, firstSpeech) ||
		strings.Count(got, path) != 1 || !strings.Contains(got, "Nova fala") ||
		len(restored.d.live.PreviousBlocks) != 0 || len(restored.d.live.PreviousEntries) != 0 {
		t.Fatalf("a retomada não substituiu somente a fala anterior: %q", got)
	}
}

func TestClearPendingRetranscriptionAndDeleteScreenshotDoesNotRestoreSpeech(t *testing.T) {
	for _, inline := range []bool{false, true} {
		for _, olderArchive := range []bool{false, true} {
			t.Run(fmt.Sprintf("inline=%t/older-archive=%t", inline, olderArchive), func(t *testing.T) {
				h := newHarness(t, nil, nil)
				source := filepath.Join(recordingFolder(h.d.directory), "anterior.wav")
				streamFixtureSeconds(t, source, 6)
				path := filepath.Join(h.d.directory, "screenshots", "excluida.png")
				previousEntries := []live.Entry{
					{ID: 1, Track: "microphone", End: 6, LastSample: 96000, Text: "Fala que foi limpa."},
					{ID: 2, Start: 2, End: 2, Screenshot: true, Path: path},
				}
				previous := &liveRecording{liveArchive: liveArchive{
					Entries: previousEntries, InlineScreenshots: inline,
				}}
				archive := &liveArchive{
					Source: source, Prefix: "Texto anterior.", InlineScreenshots: inline, NextID: 3,
					Tracks: []recorder.Track{{ID: "microphone", Snapshot: recorder.Snapshot{
						Path: source, SampleRate: 16000, WrittenSamples: 96000, Duration: 6,
					}}},
					Entries: []live.Entry{
						{ID: 2, Start: 2, End: 2, Screenshot: true, Path: path},
						{ID: 3, Track: "microphone", End: 6, LastSample: 96000, Pending: true},
					},
					PreviousBlocks: previous.textBlocks(), PreviousEntries: previousEntries,
				}
				if olderArchive {
					archive.PreviousEntries = nil
				}
				archive.Combined = live.Render(archive.Prefix, archive.PreviousBlocks)
				h.d.showResult(resultFor(source, archive.Combined), false)
				h.d.transcriptionRun = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
				h.d.restoreLiveArchive(h.d.document, archive)
				h.d.screenshotFiles = []string{path}
				h.d.clearText(true)
				h.d.removeScreenshotReference(nil, path)
				h.settle()
				if h.d.editor.Text != "" {
					t.Fatalf("excluir a screenshot republicou fala depois de Limpar: %q", h.d.editor.Text)
				}
				if err := h.d.saveWorkspace(); err != nil {
					t.Fatal(err)
				}
				restored := newHarness(t, nil, nil)
				restored.d.directory = h.d.directory
				if !restored.d.loadWorkspace() || restored.d.editor.Text != "" {
					t.Fatal("reabrir republicou a fala limpa")
				}
			})
		}
	}
}
