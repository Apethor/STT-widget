package desktop

import (
	"context"
	"image"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"ai-talker/internal/config"
	"ai-talker/internal/recorder"
	"ai-talker/internal/store"
	"ai-talker/internal/transcript"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

func recordedScreenshotHarness(t *testing.T, runner Runner, configure func(*dependencies)) *testHarness {
	t.Helper()
	return newHarness(t, runner, func(deps *dependencies) {
		deps.newRecorder = func(_ int, directory string) recording {
			path := filepath.Join(directory, "gravacao-com-screenshots.wav")
			streamFixtureSeconds(t, path, 4)
			return &fakeRecording{state: recorder.Snapshot{Path: path, Duration: 4, SampleRate: 16000, WrittenSamples: 64000}}
		}
		deps.selectScreenshot = func(context.Context) (image.Image, error) { return screenshotFixture(), nil }
		if configure != nil {
			configure(deps)
		}
	})
}

func takeScreenshot(t *testing.T, h *testHarness) *screenshotEditor {
	t.Helper()
	before := len(h.d.screenshotEditors)
	var previous []*screenshotEditor
	for editor := range h.d.screenshotEditors {
		previous = append(previous, editor)
	}
	test.Tap(h.d.screenshotButton)
	h.await(func() bool { return len(h.d.screenshotEditors) == before+1 })
	for editor := range h.d.screenshotEditors {
		if !slices.Contains(previous, editor) {
			return editor
		}
	}
	t.Fatal("a captura não abriu um editor novo")
	return nil
}

func TestRecordingScreenshotsHaveClickableShortNamesAndCopyFullPaths(t *testing.T) {
	copied := make(chan string, 1)
	opened := make(chan string, 1)
	h := recordedScreenshotHarness(t, nil, func(deps *dependencies) {
		deps.copy = func(text string) error { copied <- text; return nil }
		deps.openImage = func(path string) error { opened <- path; return nil }
	})
	doc := h.d.document
	doc.editor.SetText("Texto anterior.")
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	first := takeScreenshot(t, h)
	second := takeScreenshot(t, h)
	if doc.editor.Text != "Texto anterior." {
		t.Fatal("os links apareceram antes do final da transcrição")
	}
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureIdle && !doc.busy })
	want := "Texto anterior.\n\nTexto desta gravação.\n\n" + first.path + "\n\n" + second.path
	if doc.editor.Text != want {
		t.Fatalf("os links não ficaram na posição da captura: %q", doc.editor.Text)
	}
	if !doc.transcriptReading || doc.editor.Visible() || !doc.transcriptPreview.Visible() {
		t.Fatal("a transcrição não mostrou a leitura com hyperlinks")
	}
	links := collectLinks(doc.transcriptPreview.Segments)
	if len(links) != 2 || links[0].Text != "Screenshot 1" || links[1].Text != "Screenshot 2" {
		t.Fatalf("a leitura não condensou os caminhos: %+v", links)
	}
	links[0].OnTapped()
	if waitValue(t, opened) != first.path {
		t.Fatal("o hyperlink não abriu a screenshot correspondente")
	}
	h.settle()
	test.Tap(doc.copyButton)
	if waitValue(t, copied) != want {
		t.Fatal("copiar substituiu os caminhos pelos nomes reduzidos")
	}
	h.settle()
	if saved := readResult(t, h.d.documentLatestPath(doc)); saved.Text != want {
		t.Fatal("o resultado persistido perdeu os links")
	}
	if err := h.d.saveWorkspace(); err != nil {
		t.Fatal(err)
	}
	if !h.d.loadWorkspace() || !h.d.document.transcriptReading ||
		len(collectLinks(h.d.document.transcriptPreview.Segments)) != 2 || h.d.editor.Text != want {
		t.Fatal("reabrir a sessão perdeu a leitura ou os caminhos das screenshots")
	}
}

func TestScreenshotFinishingAfterTranscriptionStillBelongsToOriginalRecording(t *testing.T) {
	selected := make(chan struct{})
	release := make(chan struct{})
	releaseSelection := sync.OnceFunc(func() { close(release) })
	h := recordedScreenshotHarness(t, nil, func(deps *dependencies) {
		deps.selectScreenshot = func(ctx context.Context) (image.Image, error) {
			close(selected)
			select {
			case <-release:
				return screenshotFixture(), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	})
	t.Cleanup(releaseSelection)
	first := h.d.document
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	test.Tap(h.d.screenshotButton)
	waitValue(t, selected)
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureIdle && !first.busy })
	second := h.d.addDocument()
	second.editor.SetText("Outro texto isolado.")
	releaseSelection()
	h.await(func() bool { return len(h.d.screenshotEditors) == 1 })
	editor := onlyScreenshotEditor(t, h.d)
	want := "Texto desta gravação.\n\n" + editor.path
	if first.editor.Text != want || second.editor.Text != "Outro texto isolado." || !first.transcriptReading {
		t.Fatal("uma seleção tardia perdeu o vínculo ou mudou a aba selecionada")
	}
	if saved := readResult(t, h.d.documentLatestPath(first)); saved.Text != want {
		t.Fatal("a referência tardia não foi persistida")
	}
}

func TestRecordingScreenshotRetranscriptionKeepsExactlyOneLink(t *testing.T) {
	attempts := 0
	h := recordedScreenshotHarness(t, runnerFunc(func(_ context.Context, source string, _ config.Settings, _ transcript.Request) (
		transcript.Result, error,
	) {
		attempts++
		text := "Primeira resposta."
		if attempts > 1 {
			text = "Resposta revisada."
		}
		return resultFor(source, text), nil
	}), nil)
	h.d.editor.SetText("Texto anterior.")
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	editor := takeScreenshot(t, h)
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureIdle && !h.d.busy })
	test.Tap(h.d.retranscribeButton)
	h.await(func() bool { return !h.d.busy })
	want := "Texto anterior.\n\nResposta revisada.\n\n" + editor.path
	if h.d.editor.Text != want || strings.Contains(h.d.editor.Text, screenshotLinkHeading) ||
		strings.Count(h.d.editor.Text, editor.path) != 1 {
		t.Fatalf("retomar duplicou ou perdeu os screenshots: %q", h.d.editor.Text)
	}
}

func TestRecordingScreenshotClearKeepsAudioAndAttachments(t *testing.T) {
	h := recordedScreenshotHarness(t, nil, nil)
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	first := takeScreenshot(t, h)
	h.d.editor.SetText("Texto para limpar.")
	test.Tap(h.d.clearButton)
	second := takeScreenshot(t, h)
	if h.d.capture != captureRunning || h.d.editor.Text != "" {
		t.Fatal("limpar interrompeu a gravação ou trouxe links antes da transcrição")
	}
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureIdle && !h.d.busy })
	want := "Texto desta gravação.\n\n" + first.path + "\n\n" + second.path
	if h.d.editor.Text != want {
		t.Fatalf("limpar durante a captura perdeu as imagens da gravação: %q", h.d.editor.Text)
	}
}

func TestDeletingScreenshotBeforeOrAfterTranscriptionRemovesOnlyItsLink(t *testing.T) {
	for _, when := range []string{"before", "after"} {
		t.Run(when, func(t *testing.T) {
			h := recordedScreenshotHarness(t, nil, nil)
			test.Tap(h.d.recordButton)
			h.await(func() bool { return h.d.capture == captureRunning })
			first := takeScreenshot(t, h)
			second := takeScreenshot(t, h)
			if when == "after" {
				test.Tap(h.d.recordButton)
				h.await(func() bool { return h.d.capture == captureIdle && !h.d.busy })
			}
			test.Tap(first.trash)
			h.await(func() bool { return first.closed })
			if when == "before" {
				test.Tap(h.d.recordButton)
				h.await(func() bool { return h.d.capture == captureIdle && !h.d.busy })
			}
			want := "Texto desta gravação.\n\n" + second.path
			if h.d.editor.Text != want || len(collectLinks(h.d.transcriptPreview.Segments)) != 1 {
				t.Fatal("a exclusão perdeu outro link ou deixou o arquivo excluído na transcrição")
			}
			requireAbsent(t, first.path)
			if _, err := os.Stat(second.path); err != nil {
				t.Fatal("a exclusão removeu outra imagem:", err)
			}
		})
	}
}

func TestScreenshotSaveKeepsLinkAndClosingDuringDeletionRemovesIt(t *testing.T) {
	h := recordedScreenshotHarness(t, nil, nil)
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	editor := takeScreenshot(t, h)
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureIdle && !h.d.busy })
	want := h.d.editor.Text
	test.Tap(editor.save)
	h.await(func() bool { return !editor.busy })
	if h.d.editor.Text != want || len(collectLinks(h.d.transcriptPreview.Segments)) != 1 {
		t.Fatal("salvar a imagem removeu ou alterou seu link")
	}
	test.Tap(editor.trash)
	editor.window.Close()
	h.settle()
	requireAbsent(t, editor.path)
	if h.d.editor.Text != "Texto desta gravação." ||
		len(collectLinks(h.d.transcriptPreview.Segments)) != 0 ||
		readResult(t, h.d.documentLatestPath(h.d.document)).Text != h.d.editor.Text {
		t.Fatal("fechar durante a exclusão deixou uma referência ao arquivo apagado")
	}
}

func TestScreenshotOutsideRecordingAndDiscardDoNotAppendFooters(t *testing.T) {
	h := recordedScreenshotHarness(t, nil, func(deps *dependencies) {
		deps.confirmDiscard = func(_ fyne.Window, callback func(bool)) { callback(true) }
	})
	h.d.editor.SetText("Manter este texto.")
	outside := takeScreenshot(t, h)
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	inside := takeScreenshot(t, h)
	test.Tap(h.d.discardButton)
	h.await(func() bool { return h.d.capture == captureIdle })
	if h.d.editor.Text != "Manter este texto." || strings.Contains(h.d.editor.Text, screenshotLinkHeading) {
		t.Fatal("descartar a gravação ou capturar fora dela acrescentou links")
	}
	for _, path := range []string{outside.path, inside.path} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("descartar a gravação apagou uma screenshot salva:", err)
		}
	}
}

func TestUnregisteredLocalPathsNeverBecomeClickableImages(t *testing.T) {
	h := newHarness(t, nil, nil)
	doc := h.d.document
	outside := filepath.Join(t.TempDir(), "outside.png")
	doc.screenshotFiles = []string{outside}
	doc.editor.SetText(screenshotFooter([]string{outside}) + "\n[file](file:///C:/Windows/cmd.exe)")
	h.d.updateTranscriptLinks(doc, true)
	if doc.transcriptReading || len(collectLinks(doc.transcriptPreview.Segments)) != 0 {
		t.Fatal("um arquivo fora da pasta de screenshots virou link clicável")
	}
}

func TestJoiningTabsKeepsScreenshotLinksAndFullPaths(t *testing.T) {
	h := newHarness(t, nil, nil)
	first := h.d.document
	second := h.d.addDocument()
	path := filepath.Join(h.d.directory, "screenshots", "Screenshot_2026-10-06_12-30-00.png")
	second.screenshotFiles = []string{path}
	first.editor.SetText("Primeiro texto.")
	second.editor.SetText("Segundo texto.\n\n" + screenshotFooter([]string{path}))
	h.d.joinTranscripts()
	if h.d.document != first || len(h.d.documents) != 1 ||
		!first.transcriptReading || len(collectLinks(first.transcriptPreview.Segments)) != 1 ||
		!strings.Contains(first.editor.Text, path) {
		t.Fatal("Juntar perdeu os hyperlinks ou os caminhos das screenshots")
	}
	var workspace workspaceSnapshot
	if err := store.ReadJSON(filepath.Join(h.d.directory, "workspace-go-v1.json"), &workspace); err != nil {
		t.Fatal(err)
	}
	if len(workspace.Tabs[0].ScreenshotFiles) != 1 {
		t.Fatal("Juntar não persistiu os arquivos da aba de origem")
	}
}
