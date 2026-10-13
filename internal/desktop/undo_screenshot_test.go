package desktop

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ai-talker/internal/config"
	"ai-talker/internal/live"
	"ai-talker/internal/processing"
	"ai-talker/internal/recorder"

	"fyne.io/fyne/v2/test"
)

func seedScreenshotTimeline(t *testing.T, h *testHarness) (*document, *screenshotAssociation, string) {
	t.Helper()
	doc := h.d.document
	source := filepath.Join(h.d.directory, "gravacoes", "source.wav")
	path := filepath.Join(h.d.directory, "screenshots", "source.png")
	streamFixtureSeconds(t, source, 1)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("synthetic image content"), 0o600); err != nil {
		t.Fatal(err)
	}
	doc.lastAudio = source
	doc.editor.SetText("Primeira transcrição.\n" + path)
	result := resultFor(source, doc.editor.Text)
	result.DurationSeconds = 1
	doc.lastResult, doc.screenshotFiles = &result, []string{path}
	archive := &liveArchive{
		Source: source, NextID: 2, InlineScreenshots: true,
		Tracks: []recorder.Track{{ID: "microphone", Snapshot: recorder.Snapshot{
			Path: source, SampleRate: 16000, WrittenSamples: 16000, Duration: 1,
		}}},
		Entries: []live.Entry{
			{ID: 1, Track: "microphone", FirstSample: 0, LastSample: 16000, Start: 0, End: 1, Text: "Primeira transcrição."},
			{ID: 2, Start: 0.5, End: 0.5, Screenshot: true, Path: path},
		},
		PreviousBlocks: []live.Block{{ID: 1, Text: "Primeira transcrição."}, {ID: 2, Text: path}},
	}
	archive.PreviousEntries = slices.Clone(archive.Entries)
	if !h.d.validLiveArchive(archive) {
		t.Fatal("fixture de timeline inválida")
	}
	h.d.restoreLiveArchive(doc, archive)
	doc.undo = &clearUndo{Text: doc.editor.Text, Result: &result, Source: source, Live: archive}
	doc.processHistory = []processing.Turn{{Prompt: "Pedido enviado.\n" + path, Response: "Resposta preservada."}}
	doc.view.timer = clockText(result.DurationSeconds)
	h.d.renderDocument(doc)
	h.d.restorePromptHistory(doc, nil)
	doc.audioHistory = []audioSnapshot{h.d.snapshotAudio(doc)}
	return doc, &screenshotAssociation{document: doc, source: source, live: doc.live, marker: 2}, path
}

func TestJoinDetachesOldTimelineBeforeDeletingScreenshot(t *testing.T) {
	h := newHarness(t, nil, nil)
	first, _, path := seedScreenshotTimeline(t, h)
	h.d.addDocument().editor.SetText("Segunda transcrição.")
	test.Tap(h.d.joinButton)
	if len(h.d.documents) != 1 || first.lastAudio != "" || first.lastResult != nil {
		t.Fatal("Juntar não concluiu o estado combinado esperado")
	}
	h.d.removeDocumentScreenshotReference(first, path)
	h.settle()
	if first.lastResult != nil || first.live != nil {
		t.Fatal("a timeline antiga sobreviveu ao Juntar e a exclusão republicou o áudio substituído")
	}
}

func TestUndoDoesNotRestoreExplicitlyDeletedScreenshot(t *testing.T) {
	for _, operation := range []string{"Fechar", "Juntar", "Mesclar"} {
		t.Run(operation, func(t *testing.T) {
			now := time.Now()
			h := newHarness(t, nil, func(deps *dependencies) {
				deps.now = func() time.Time { return now }
				deps.process = func(_ context.Context, _ config.Settings, _ processing.Request) (processing.Result, error) {
					return processing.Result{Text: "Transcrições mescladas."}, nil
				}
			})
			first, owner, path := seedScreenshotTimeline(t, h)
			sent := first.processHistory[0]
			second := h.d.addDocument()
			second.editor.SetText("Segunda transcrição.")
			switch operation {
			case "Fechar":
				h.d.closeDocument(first.item)
			case "Juntar":
				test.Tap(h.d.joinButton)
			case "Mesclar":
				test.Tap(h.d.mergeButton)
				h.await(func() bool { return !h.d.mergeBusy })
			}
			if len(h.d.documents) != 1 {
				t.Fatal("a operação não concluiu")
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			h.d.removeScreenshotReference(owner, path)
			test.Tap(h.d.undoCloseButton)
			if len(h.d.documents) != 2 || first.closed {
				t.Fatal("a operação inteira não foi restaurada")
			}
			timelineLink := first.live != nil && slices.ContainsFunc(first.live.Entries, func(entry live.Entry) bool {
				return entry.Path == path
			})
			if strings.Contains(first.editor.Text, path) || slices.Contains(first.screenshotFiles, path) || timelineLink ||
				first.lastResult != nil && strings.Contains(first.lastResult.Text, path) {
				t.Fatal("Desfazer republicou uma screenshot apagada")
			}
			if first.processHistory[0] != sent || !strings.Contains(first.editor.Text, "Primeira transcrição.") {
				t.Fatal("a exclusão alterou falas ou um turno enviado")
			}
			for _, saved := range first.audioHistory {
				if strings.Contains(saved.Text, path) || slices.Contains(saved.ScreenshotFiles, path) {
					t.Fatal("o histórico de áudio recuperou o caminho apagado")
				}
			}
			h.d.undoClear()
			if strings.Contains(first.editor.Text, path) {
				t.Fatal("Desfazer limpeza recuperou o caminho apagado")
			}
			restored := newHarness(t, nil, func(deps *dependencies) { deps.now = func() time.Time { return now } })
			restored.d.directory = h.d.directory
			if !restored.d.loadWorkspace() || strings.Contains(restored.d.document.editor.Text, path) {
				t.Fatal("reabrir recuperou uma referência excluída")
			}
		})
	}
}
