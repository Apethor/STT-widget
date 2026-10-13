package desktop

import (
	"testing"

	"ai-talker/internal/config"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func assertPopupInCanvas(t *testing.T, h *testHarness, popup *widget.PopUp) {
	t.Helper()
	if !popup.Visible() {
		t.Fatal("o atalho fechou durante a interação")
	}
	h.d.window.Content().Refresh()
	position := h.app.Driver().AbsolutePositionForObject(popup.Content)
	size := popup.Content.Size()
	canvas := h.d.window.Canvas().Size()
	if position.X < 0 || position.Y < 0 || position.X+size.Width > canvas.Width+1 ||
		position.Y+size.Height > canvas.Height+1 {
		t.Fatalf("o atalho foi cortado: posição %v tamanho %v canvas %v", position, size, canvas)
	}
}

func TestDeviceShortcutOpensToLeftAndStaysInsideAllRecorderLayouts(t *testing.T) {
	h := newHarness(t, nil, nil)
	for _, disposition := range []string{config.LayoutColumn, config.LayoutInline, config.LayoutStacked} {
		for _, size := range []string{config.RecordingLarge, config.RecordingMedium} {
			t.Run(disposition+"/"+size, func(t *testing.T) {
				previous := h.d.settings.Appearance
				next := previous
				next.Layout, next.RecordingSize = disposition, size
				h.d.settings.Appearance = next
				h.d.applyAppearance(previous, next)
				h.d.window.Resize(h.d.initialWindowSize())
				before := h.d.window.Canvas().Size()
				test.Tap(h.d.deviceButton)
				form := h.d.deviceForm
				defer form.popup.Hide()
				h.await(func() bool { return !form.loading })
				for _, mode := range []string{teamChoice, systemChoice, microphoneChoice} {
					h.d.captureSelect.SetSelected(mode)
					h.settle()
					assertPopupInCanvas(t, h, form.popup)
					position := form.popup.Position()
					anchor := h.app.Driver().AbsolutePositionForObject(h.d.deviceButton)
					anchorRight := anchor.X + h.d.deviceButton.Size().Width
					if anchorRight >= form.popup.Size().Width+8 &&
						(position.X >= anchor.X || position.X+form.popup.Size().Width > anchorRight+1) {
						t.Fatalf("o menu não abriu para a esquerda do botão: %v / %v", position, anchor)
					}
					if h.d.window.Canvas().Size() != before {
						t.Fatal("escolher uma fonte aumentou a janela")
					}
				}
				form.popup.Hide()
			})
		}
	}
}

func TestShortcutUsesSpaceAboveButtonNearBottomAndClampsLeftEdge(t *testing.T) {
	h := newHarness(t, nil, nil)
	canvas := h.d.window.Canvas().Size()
	popup := widget.NewPopUp(widget.NewLabel("Atalho"), h.d.window.Canvas())
	popup.Resize(fyne.NewSize(220, 110))
	anchor := widget.NewButton("", nil)
	anchor.Resize(fyne.NewSize(32, 32))
	anchor.Move(fyne.NewPos(0, canvas.Height-32))
	h.d.positionShortcut(popup, anchor)
	if popup.Position().X != 8 || popup.Position().Y+popup.Size().Height >
		anchor.Position().Y || popup.Position().Y < 8 {
		t.Fatal("o atalho não aproveitou o espaço disponível acima/esquerda", popup.Position())
	}
	popup.Hide()
}

func TestProviderMenuContainsOnlyEnginesAndFitsNarrowWindows(t *testing.T) {
	h := newHarness(t, nil, nil)
	for _, width := range []float32{320, 480} {
		h.d.window.Resize(fyne.NewSize(width, 900))
		before := h.d.window.Canvas().Size()
		test.Tap(h.d.providerButton)
		menu := h.d.window.Canvas().Focused().(*widget.PopUpMenu)
		if len(menu.Items) != 3 {
			t.Fatal("o menu repetiu catálogos de modelo")
		}
		for _, item := range menu.Items {
			if item.(interface{ Child() *widget.Menu }).Child() != nil {
				t.Fatal("um motor ainda abre submenu de modelo")
			}
		}
		position := h.app.Driver().AbsolutePositionForObject(menu)
		if position.X < 0 || position.Y < 0 || position.X+menu.Size().Width > before.Width+1 ||
			position.Y+menu.Size().Height > before.Height+1 || h.d.window.Canvas().Size() != before {
			t.Fatal("o menu de motores saiu da janela ou alterou suas dimensões")
		}
		menu.Dismiss()
	}
}
