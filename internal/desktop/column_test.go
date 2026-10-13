package desktop

import (
	"image/color"
	"testing"

	"ai-talker/internal/config"
	"ai-talker/internal/platform"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
)

func TestColumnIntegratedHeaderHasNoReservedTitlebarGap(t *testing.T) {
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.windowFrame = func(bool, color.NRGBA, platform.WindowHeader) error { return nil }
	})
	position := h.app.Driver().AbsolutePositionForObject(h.d.header)
	if position.Y > 8 {
		t.Fatalf("o cabeçalho ficou afastado do topo: %v", position)
	}
}

func TestColumnOnlyScrollsWhenItsContentsOverflow(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.d.window.Resize(fyne.NewSize(480, 900))
	h.d.columnScroll.ScrollToBottom()
	if h.d.columnScroll.Offset.Y > 0 {
		t.Fatalf("a coluna força rolagem mesmo sem aumentar as caixas: %v", h.d.columnScroll.Offset)
	}
}

func TestColumnFitsCompactWindow(t *testing.T) {
	h := newHarness(t, nil, nil)
	if initial := h.d.window.Canvas().Size(); initial.Width > 520 {
		t.Fatalf("a coluna abre larga demais: %v", initial)
	}
	if minimum := h.d.window.Content().MinSize(); minimum.Width > 340 {
		t.Fatalf("a coluna não permite aproximadamente metade da largura anterior: %v", minimum)
	}
	h.d.window.Resize(fyne.NewSize(320, 700))
	h.d.editor.SetText("Transcrição preservada numa janela estreita.")
	h.d.processEditor.SetText("Mensagem pronta para copiar.")
	h.d.showError("Não foi possível localizar o perfil AWS. Confira as configurações.", "")
	h.d.window.Content().Refresh()
	if size := h.d.window.Canvas().Size(); size.Width > 340 {
		t.Fatalf("um texto ou controle alargou a coluna: %v", size)
	}
	for _, control := range []fyne.CanvasObject{
		h.d.themeButton, h.d.pinButton, h.d.settingsButton, h.d.minimizeButton, h.d.windowCloseButton, h.d.recordButton,
		h.d.clearButton, h.d.saveButton, h.d.processButton, h.d.processModel,
	} {
		position := h.app.Driver().AbsolutePositionForObject(control)
		if position.X < 0 || position.X+control.Size().Width > h.d.window.Canvas().Size().Width {
			t.Fatalf("um controle saiu pelas laterais da coluna: %T em %v, tamanho %v", control, position, control.Size())
		}
	}
}

func TestColumnEditorDividerRedistributesHeight(t *testing.T) {
	h := newHarness(t, nil, nil)
	doc := h.d.document
	doc.editor.SetText("Texto que deve sobreviver ao ajuste.")
	doc.processEditor.SetText("Resultado que deve sobreviver ao ajuste.")
	dragColumnHeight(h, 220)
	beforeSource, beforeResult := doc.editors.Leading.Size().Height, doc.editors.Trailing.Size().Height
	dragDivider(h, doc.editors, 0, 60)
	if doc.editors.Leading.Size().Height-beforeSource < 40 ||
		beforeResult-doc.editors.Trailing.Size().Height < 40 {
		t.Fatalf("arrastar não redistribuiu a altura: antes %v/%v, depois %v/%v",
			beforeSource, beforeResult, doc.editors.Leading.Size().Height, doc.editors.Trailing.Size().Height)
	}
	h.d.window.Resize(fyne.NewSize(320, 700))
	if doc.editor.Text != "Texto que deve sobreviver ao ajuste." ||
		doc.processEditor.Text != "Resultado que deve sobreviver ao ajuste." {
		t.Fatal("arrastar ou estreitar a janela alterou os textos")
	}
}

func TestColumnRecorderDividerAndScrollingKeepTheirAdjustments(t *testing.T) {
	h := newHarness(t, nil, nil)
	dragColumnHeight(h, 220)
	split := h.d.rowSplit
	beforeRecorder, beforeTabs := split.Leading.Size().Height, split.Trailing.Size().Height
	dragDivider(h, split, 0, 50)
	if split.Leading.Size().Height-beforeRecorder < 35 || beforeTabs-split.Trailing.Size().Height < 35 {
		t.Fatalf("a divisória da gravação não redistribuiu altura: antes %v/%v, depois %v/%v",
			beforeRecorder, beforeTabs, split.Leading.Size().Height, split.Trailing.Size().Height)
	}
	h.d.columnScroll.ScrollToBottom()
	if h.d.columnScroll.Offset.Y <= 0 {
		t.Fatal("a coluna não permite rolar até o processamento")
	}
	scroll, totalHeight := h.d.columnScroll.Offset, h.d.columnScroll.Content.Size().Height
	expandedHeight := h.d.recordPanel.Size().Height
	previous := h.d.settings.Appearance
	h.d.settings.Appearance.RecordingSize = config.RecordingMedium
	h.d.applyAppearance(previous, h.d.settings.Appearance)
	maximumScroll := max(0, h.d.columnScroll.Content.Size().Height-h.d.columnScroll.Size().Height)
	if h.d.recordPanel.Size().Height >= expandedHeight ||
		h.d.columnScroll.Content.Size().Height != totalHeight ||
		h.d.columnScroll.Offset.Y != min(scroll.Y, maximumScroll) {
		t.Fatalf("o preset não reduziu o gravador ou perdeu a altura total/rolagem: gravador %v/%v, total %v/%v, scroll %v/%v",
			expandedHeight, h.d.recordPanel.Size().Height, totalHeight, h.d.columnScroll.Content.Size().Height,
			scroll.Y, h.d.columnScroll.Offset.Y)
	}
}

func TestCompactColumnKeepsAWSAfterFolderAndLongRecordingNameWithinWidth(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.d.window.Resize(fyne.NewSize(320, 700))
	h.d.document.name = "Uma aba com um nome de gravação bem longo"
	h.d.renumberDocuments()
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	if width := h.d.window.Canvas().Size().Width; width > 340 {
		t.Fatalf("o nome da aba alargou a janela durante a gravação: %v", width)
	}
	dot := h.app.Driver().AbsolutePositionForObject(h.d.awsIndicator)
	folder := h.app.Driver().AbsolutePositionForObject(h.d.folderButton)
	if dot.X <= folder.X || dot.Y < h.d.header.Size().Height ||
		dot.Y+h.d.awsIndicator.Size().Height > h.d.window.Canvas().Size().Height {
		t.Fatal("o estado AWS não ficou no rodapé, depois de Abrir pasta")
	}
	rendered := test.WidgetRenderer(h.d.caption).Objects()[0]
	if rendered.MinSize().Width > h.d.caption.Size().Width+1 {
		t.Fatal("a legenda da gravação não respeitou a largura disponível")
	}
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureIdle && !h.d.busy })
}

func TestColumnBottomHandleExpandsAndThenRemovesOverflow(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.d.editor.SetText("Texto preservado ao ajustar a altura total.")
	h.d.processEditor.SetText("Resultado preservado ao ajustar a altura total.")
	initial := h.d.rowSplit.Size().Height
	dragColumnHeight(h, 240)
	h.d.columnScroll.ScrollToBottom()
	if h.d.columnScroll.Offset.Y <= 0 || h.d.rowSplit.Size().Height < initial+220 {
		t.Fatal("a alça inferior não aumentou as caixas nem habilitou a rolagem")
	}
	dragColumnHeight(h, -280)
	h.d.columnScroll.ScrollToBottom()
	if h.d.columnScroll.Offset.Y != 0 ||
		h.d.columnScroll.Content.Size().Height > h.d.columnScroll.Size().Height ||
		h.d.editor.Text != "Texto preservado ao ajustar a altura total." ||
		h.d.processEditor.Text != "Resultado preservado ao ajustar a altura total." {
		t.Fatal("reduzir as caixas manteve a rolagem ou alterou os textos")
	}
}

func dragColumnHeight(h *testHarness, delta float32) {
	h.t.Helper()
	position := h.app.Driver().AbsolutePositionForObject(h.d.columnHandle)
	position = position.Add(fyne.NewPos(h.d.columnHandle.Size().Width/2, h.d.columnHandle.Size().Height/2))
	test.Drag(h.d.window.Canvas(), position, 0, delta)
}

func dragDivider(h *testHarness, split *container.Split, dx, dy float32) {
	h.t.Helper()
	divider := test.WidgetRenderer(split).Objects()[1]
	position := h.app.Driver().AbsolutePositionForObject(divider)
	position = position.Add(fyne.NewPos(divider.Size().Width/2, divider.Size().Height/2))
	test.Drag(h.d.window.Canvas(), position, dx, dy)
}
