package desktop

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ai-talker/internal/platform"
	"ai-talker/internal/screenshot"

	"fyne.io/fyne/v2"
	fynedesktop "fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func screenshotFixture() *image.RGBA {
	pixels := image.NewRGBA(image.Rect(0, 0, 100, 80))
	for i := 0; i < len(pixels.Pix); i += 4 {
		copy(pixels.Pix[i:i+4], []byte{40, 70, 100, 255})
	}
	return pixels
}

func onlyScreenshotEditor(t *testing.T, d *desktop) *screenshotEditor {
	t.Helper()
	if len(d.screenshotEditors) != 1 {
		t.Fatalf("esperado um editor de screenshot, encontrados %d", len(d.screenshotEditors))
	}
	for editor := range d.screenshotEditors {
		return editor
	}
	return nil
}

func TestScreenshotWhileRecordingPreservesAudioAndText(t *testing.T) {
	selected := make(chan struct{})
	release := make(chan struct{})
	h := newHarness(t, nil, func(deps *dependencies) {
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
	h.d.editor.SetText("Transcrição preservada.")
	h.d.processEditor.SetText("Resultado preservado.")
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	recorder, owner := h.d.recorder, h.d.captureDocument
	test.Tap(h.d.screenshotButton)
	waitValue(t, selected)
	if !h.d.screenshotButton.Disabled() || h.d.pauseButton.Disabled() || h.d.recordButton.Disabled() {
		t.Fatal("a seleção não isolou o botão de captura dos controles da gravação")
	}
	h.d.captureScreenshot() // A second request must not start another selection.
	close(release)
	h.await(func() bool { return len(h.d.screenshotEditors) == 1 })
	e := onlyScreenshotEditor(t, h.d)
	if h.d.capture != captureRunning || h.d.recorder != recorder || h.d.captureDocument != owner {
		t.Fatal("a screenshot alterou a captura de áudio")
	}
	if h.d.editor.Text != "Transcrição preservada." || h.d.processEditor.Text != "Resultado preservado." {
		t.Fatal("a screenshot alterou os editores de texto")
	}
	if filepath.Dir(e.path) != filepath.Join(h.d.directory, "screenshots", h.d.deps.now().Format("2006-01-02")) {
		t.Fatal("a captura não foi salva na pasta automática")
	}
	saved, err := os.ReadFile(e.path)
	if err != nil {
		t.Fatal("o editor abriu antes de salvar o original:", err)
	}
	e.document.BeginStroke(screenshot.Point{X: 20, Y: 20}, 6, color.NRGBA{R: 255, A: 255}, false)
	e.document.EndStroke()
	e.window.Close()
	after, err := os.ReadFile(e.path)
	if err != nil || !bytes.Equal(saved, after) {
		t.Fatal("fechar o editor alterou ou apagou a imagem salva")
	}
	if h.d.capture != captureRunning {
		t.Fatal("fechar o editor interrompeu a gravação")
	}
}

func TestScreenshotEditorSaveAndDeleteHaveOwnPath(t *testing.T) {
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.selectScreenshot = func(context.Context) (image.Image, error) { return screenshotFixture(), nil }
	})
	test.Tap(h.d.screenshotButton)
	h.await(func() bool { return len(h.d.screenshotEditors) == 1 })
	first := onlyScreenshotEditor(t, h.d)
	test.Tap(h.d.screenshotButton)
	h.await(func() bool { return len(h.d.screenshotEditors) == 2 })
	var second *screenshotEditor
	for editor := range h.d.screenshotEditors {
		if editor != first {
			second = editor
		}
	}
	original, err := os.ReadFile(second.path)
	if err != nil {
		t.Fatal(err)
	}
	first.document.BeginStroke(screenshot.Point{X: 20, Y: 20}, 6, color.NRGBA{R: 255, A: 255}, false)
	first.document.EndStroke()
	first.refresh()
	h.d.createDocument()
	test.Tap(first.save)
	if !first.trash.Disabled() {
		t.Fatal("excluir durante um salvamento pode recriar o arquivo")
	}
	h.await(func() bool { return !first.busy })
	changed, err := os.ReadFile(first.path)
	if err != nil || bytes.Equal(changed, original) {
		t.Fatal("salvar não atualizou o arquivo da screenshot de origem")
	}
	pixels, err := png.Decode(bytes.NewReader(changed))
	if err != nil || color.NRGBAModel.Convert(pixels.At(20, 20)) != (color.NRGBA{R: 255, A: 255}) {
		t.Fatal("a marcação não apareceu no PNG salvo")
	}
	test.Tap(first.trash)
	h.await(func() bool { return first.closed })
	requireAbsent(t, first.path)
	after, err := os.ReadFile(second.path)
	if err != nil || !bytes.Equal(after, original) || second.closed {
		t.Fatal("excluir a primeira screenshot modificou ou fechou a segunda")
	}
}

func TestScreenshotCopyUsesEachWindowsAbsolutePNGPathAndKeepsUnsavedMarks(t *testing.T) {
	copied := make(chan string, 2)
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.selectScreenshot = func(context.Context) (image.Image, error) { return screenshotFixture(), nil }
		deps.copy = func(text string) error { copied <- text; return nil }
	})
	test.Tap(h.d.screenshotButton)
	h.await(func() bool { return len(h.d.screenshotEditors) == 1 })
	first := onlyScreenshotEditor(t, h.d)
	test.Tap(h.d.screenshotButton)
	h.await(func() bool { return len(h.d.screenshotEditors) == 2 })
	var second *screenshotEditor
	for editor := range h.d.screenshotEditors {
		if editor != first {
			second = editor
		}
	}
	for _, editor := range []*screenshotEditor{first, second} {
		saved, err := os.ReadFile(editor.path)
		if err != nil {
			t.Fatal(err)
		}
		editor.document.BeginStroke(screenshot.Point{X: 20, Y: 20}, 6, color.NRGBA{R: 255, A: 255}, false)
		editor.document.EndStroke()
		before := editor.document.Revision()
		test.Tap(editor.copyPath)
		actual := waitValue(t, copied)
		h.settle()
		after, err := os.ReadFile(editor.path)
		if err != nil || !filepath.IsAbs(actual) || actual != editor.path || before != editor.document.Revision() || !bytes.Equal(saved, after) {
			t.Fatal("copiar mudou o PNG/marcações ou usou o caminho de outra janela", actual, err)
		}
	}
}

func TestScreenshotCancelAndSaveFailureAllowRecovery(t *testing.T) {
	calls := 0
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.selectScreenshot = func(context.Context) (image.Image, error) {
			calls++
			if calls == 1 {
				return nil, platform.ErrScreenshotCanceled
			}
			return screenshotFixture(), nil
		}
	})
	test.Tap(h.d.screenshotButton)
	h.settle()
	if h.d.screenshotButton.Disabled() || len(h.d.screenshotEditors) != 0 {
		t.Fatal("cancelar a seleção deixou controles bloqueados ou abriu um editor")
	}
	if _, err := os.Stat(filepath.Join(h.d.directory, "screenshots")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cancelar a seleção deixou dados no disco")
	}
	test.Tap(h.d.screenshotButton)
	h.await(func() bool { return len(h.d.screenshotEditors) == 1 })
	e := onlyScreenshotEditor(t, h.d)
	realPath := e.path
	e.path = filepath.Join(realPath, "impossivel.png")
	e.saveImage()
	h.await(func() bool { return !e.busy })
	if e.save.Disabled() || e.trash.Disabled() || e.closed {
		t.Fatal("falhar ao salvar impediu recuperação no editor")
	}
	if _, err := os.Stat(realPath); err != nil {
		t.Fatal("o erro de salvamento removeu a imagem original")
	}
}

func TestScreenshotEscapeCancelsBeforeSelectionFinishesAndKeepsRecording(t *testing.T) {
	started := make(chan context.Context, 1)
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.selectScreenshot = func(ctx context.Context) (image.Image, error) {
			started <- ctx
			<-ctx.Done()
			// A selector's late pixels must not bypass cancellation.
			return screenshotFixture(), nil
		}
	})
	h.d.editor.SetText("Meu texto.")
	h.d.processEditor.SetText("Meu resultado.")
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	test.Tap(h.d.screenshotButton)
	ctx := waitValue(t, started)
	h.d.cancelScreenshot()
	h.await(func() bool { return !h.d.screenshotBusy })
	h.settle()
	if !errors.Is(ctx.Err(), context.Canceled) || h.d.screenshotButton.Disabled() ||
		h.d.screenshotCancel != nil || len(h.d.screenshotEditors) != 0 ||
		h.d.capture != captureRunning || h.d.editor.Text != "Meu texto." ||
		h.d.processEditor.Text != "Meu resultado." {
		t.Fatal("Escape saved late pixels, altered recording/text, or left capture controls blocked")
	}
	requireAbsent(t, filepath.Join(h.d.directory, "screenshots"))
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureIdle && !h.d.busy })
}

func TestScreenshotEscapeBeforeShowingEditorRemovesOnlyTheNewCapture(t *testing.T) {
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.selectScreenshot = func(context.Context) (image.Image, error) { return screenshotFixture(), nil }
	})
	directory := filepath.Join(h.d.directory, "screenshots")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	previous := filepath.Join(directory, "previous.png")
	if err := os.WriteFile(previous, []byte("preserved image"), 0o600); err != nil {
		t.Fatal(err)
	}
	test.Tap(h.d.screenshotButton)
	// Do not drain the UI queue: cancel after the worker has created the PNG
	// but before its completion callback can open the editor.
	deadline := time.Now().Add(2 * time.Second)
	for {
		files, err := filepath.Glob(filepath.Join(directory, "*", "*.png"))
		if err != nil {
			t.Fatal(err)
		}
		if len(files) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the screenshot worker did not create its synthetic capture")
		}
		time.Sleep(5 * time.Millisecond)
	}
	h.d.cancelScreenshot()
	h.settle()
	h.await(func() bool {
		files, err := filepath.Glob(filepath.Join(directory, "*", "*.png"))
		return err == nil && len(files) == 0 && !h.d.screenshotBusy
	})
	files, err := filepath.Glob(filepath.Join(directory, "*", "*.png"))
	if err != nil || len(files) != 0 ||
		h.d.screenshotBusy || len(h.d.screenshotEditors) != 0 {
		t.Fatal("canceling a pending editor left its new image or removed another capture")
	}
	requireFile(t, previous, "preserved image")
	test.Tap(h.d.screenshotButton)
	h.await(func() bool { return len(h.d.screenshotEditors) == 1 })
}

func TestScreenshotSurfaceMapsResizedImageAndKeepsLetterboxEmpty(t *testing.T) {
	h := newHarness(t, nil, nil)
	document, err := screenshot.New(screenshotFixture())
	if err != nil {
		t.Fatal(err)
	}
	e := newScreenshotEditor(h.d, filepath.Join(h.d.directory, "synthetic.png"), document)
	e.surface.Resize(fyne.NewSize(400, 400))
	point, inside := e.surface.imagePoint(fyne.NewPos(200, 200), false)
	if !inside || point.X != 50 || point.Y != 40 {
		t.Fatalf("a imagem redimensionada mudou as coordenadas da marcação: %v, %v", point, inside)
	}
	e.surface.MouseDown(&fynedesktop.MouseEvent{
		PointEvent: fyne.PointEvent{Position: fyne.NewPos(100, 10)}, Button: fynedesktop.MouseButtonPrimary,
	})
	if e.surface.drawing || document.CanUndo() {
		t.Fatal("clicar na margem vazia começou um traço")
	}
	e.surface.MouseDown(&fynedesktop.MouseEvent{
		PointEvent: fyne.PointEvent{Position: fyne.NewPos(200, 200)}, Button: fynedesktop.MouseButtonPrimary,
	})
	e.surface.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: fyne.NewPos(300, 200)}})
	e.surface.DragEnd()
	if got := color.NRGBAModel.Convert(document.Image().At(60, 40)); got != e.ink {
		t.Fatalf("o pincel não seguiu o arraste na imagem redimensionada: %v", got)
	}
	document.Undo()
	if got := color.NRGBAModel.Convert(document.Image().At(60, 40)); got != (color.NRGBA{R: 40, G: 70, B: 100, A: 255}) {
		t.Fatal("desfazer não recuperou a imagem após desenhar na visualização")
	}
}

func TestScreenshotZoomChangesViewWithoutChangingScreenshotPixels(t *testing.T) {
	h := newHarness(t, nil, nil)
	document, err := screenshot.New(screenshotFixture())
	if err != nil {
		t.Fatal(err)
	}
	e := newScreenshotEditor(h.d, filepath.Join(h.d.directory, "synthetic.png"), document)
	e.surface.Resize(fyne.NewSize(400, 400))
	before := document.Snapshot()
	revision := document.Revision()

	e.zoom.SetSelected("200%")
	position, imageSize := e.surface.image.Position(), e.surface.image.Size()
	point, inside := e.surface.imagePoint(position.Add(fyne.NewPos(imageSize.Width/2, imageSize.Height/2)), false)
	if !inside || point.X != 50 || point.Y != 40 ||
		imageSize != fyne.NewSize(200, 160) ||
		document.Revision() != revision || !bytes.Equal(before.Pix, document.Snapshot().Pix) {
		t.Fatalf("o zoom não ampliou a visualização ou alterou os pixels: dentro=%v ponto=%v imagem=%v rev=%d/%d pixels=%v",
			inside, point, imageSize, document.Revision(), revision,
			bytes.Equal(before.Pix, document.Snapshot().Pix))
	}

	e.zoom.SetSelected("75%")
	position, imageSize = e.surface.image.Position(), e.surface.image.Size()
	point, inside = e.surface.imagePoint(position.Add(fyne.NewPos(imageSize.Width/4, imageSize.Height/4)), false)
	if !inside || point.X != 25 || point.Y != 20 || imageSize != fyne.NewSize(75, 60) {
		t.Fatalf("trocar o zoom sem redimensionar a janela desalinhou a imagem: dentro=%v ponto=%v imagem=%v",
			inside, point, imageSize)
	}

	e.zoom.SetSelected("Ajustar à janela")
	fitScale := min(float64(e.surface.Size().Width)/100, float64(e.surface.Size().Height)/80)
	expectedFitSize := fyne.NewSize(float32(100*fitScale), float32(80*fitScale))
	if e.surface.image.Size() != expectedFitSize ||
		document.Revision() != revision || !bytes.Equal(before.Pix, document.Snapshot().Pix) {
		t.Fatal("ajustar à janela não restaurou o zoom sem alterar o PNG")
	}
}

func TestScreenshotZoomControlRemainsVisibleInANarrowEditor(t *testing.T) {
	h := newHarness(t, nil, nil)
	document, err := screenshot.New(screenshotFixture())
	if err != nil {
		t.Fatal(err)
	}
	editor := newScreenshotEditor(h.d, filepath.Join(h.d.directory, "synthetic.png"), document)
	t.Cleanup(editor.window.Close)
	editor.window.Resize(fyne.NewSize(560, 420))
	editor.window.Show()
	h.settle()
	position := h.app.Driver().AbsolutePositionForObject(editor.zoom)
	canvasSize := editor.window.Canvas().Size()
	if position.X < 0 || position.X+editor.zoom.Size().Width > canvasSize.Width ||
		position.Y+editor.zoom.Size().Height > canvasSize.Height {
		t.Fatal("o controle de zoom ficou fora da área visível do editor", position, editor.zoom.Size(), canvasSize)
	}
	test.Tap(editor.zoom)
	if !editor.zoomFit {
		t.Fatal("abrir a lista de zoom modificou a captura")
	}
}

func TestScreenshotTextToolAcceptsKeyboardTextBeforeSaving(t *testing.T) {
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.selectScreenshot = func(context.Context) (image.Image, error) { return screenshotFixture(), nil }
	})
	editor := takeScreenshot(t, h)
	original, err := os.ReadFile(editor.path)
	if err != nil {
		t.Fatal(err)
	}
	test.Tap(editor.text)
	editor.surface.MouseDown(&fynedesktop.MouseEvent{
		PointEvent: fyne.PointEvent{Position: fyne.NewPos(editor.surface.Size().Width/2, editor.surface.Size().Height/2)},
		Button:     fynedesktop.MouseButtonPrimary,
	})
	overlays := editor.window.Canvas().Overlays().List()
	if len(overlays) != 1 || !editor.textDialog || !editor.save.Disabled() {
		t.Fatal("a ferramenta de texto não abriu o diálogo de teclado")
	}
	var entry *widget.Entry
	var insert *widget.Button
	for _, object := range test.LaidOutObjects(overlays[0]) {
		switch control := object.(type) {
		case *widget.Entry:
			entry = control
		case *widget.Button:
			if control.Text == "Inserir" {
				insert = control
			}
		}
	}
	if entry == nil || insert == nil {
		t.Fatal("o diálogo não oferece digitação e inserção")
	}
	test.Type(entry, "Olá!\nMarcação")
	test.Tap(insert)
	if editor.textDialog || !editor.document.CanUndo() || editor.document.Revision() != 1 {
		t.Fatal("confirmar não inseriu o texto no histórico da imagem")
	}
	beforeSave, err := os.ReadFile(editor.path)
	if err != nil || !bytes.Equal(beforeSave, original) {
		t.Fatal("digitar modificou o arquivo antes de clicar em Salvar")
	}
	test.Tap(editor.save)
	h.await(func() bool { return !editor.busy })
	afterSave, err := os.ReadFile(editor.path)
	if err != nil || bytes.Equal(afterSave, original) {
		t.Fatal("o texto digitado não apareceu no arquivo salvo")
	}
}
