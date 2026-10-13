package desktop

import (
	"testing"

	"fyne.io/fyne/v2"
	fynedesktop "fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
)

func TestIconControlsKeepHintsAndActions(t *testing.T) {
	h := newHarness(t, nil, nil)
	controls := []*actionButton{
		h.d.clearButton, h.d.undoButton, h.d.copyButton, h.d.saveButton,
		h.d.processButton, h.d.processCopyButton, h.d.processSaveButton,
		h.d.pauseButton, h.d.newButton, h.d.importButton, h.d.folderButton,
		h.d.settingsButton, h.d.themeButton, h.d.pinButton, h.d.sizeButton,
		h.d.minimizeButton, h.d.windowCloseButton,
		h.d.joinButton, h.d.mergeButton, h.d.undoCloseButton,
		h.d.screenshotButton, h.d.document.transcriptViewButton, h.d.document.processViewButton,
		h.d.document.processFollowButton,
		h.d.deviceButton,
	}
	for _, control := range controls {
		if control.Text != "" || control.Icon == nil || control.hint.text == "" {
			t.Fatal("um controle compacto ficou sem ícone ou descrição")
		}
	}
	h.d.editor.SetText("Texto que pode ser limpo.")
	h.d.clearButton.MouseIn(&fynedesktop.MouseEvent{})
	if h.d.clearButton.hint.box == nil {
		t.Fatal("passar o mouse não mostrou a descrição")
	}
	test.Tap(h.d.clearButton)
	if h.d.clearButton.hint.box != nil || h.d.editor.Text != "" || h.d.undoButton.Disabled() {
		t.Fatal("o tooltip bloqueou a ação ou a limpeza perdeu o desfazer")
	}
	h.d.undoButton.MouseIn(&fynedesktop.MouseEvent{})
	h.d.undoButton.MouseOut()
	if h.d.undoButton.hint.box != nil {
		t.Fatal("sair do controle não removeu o tooltip")
	}
	test.Tap(h.d.undoButton)
	if h.d.editor.Text != "Texto que pode ser limpo." {
		t.Fatal("o ícone de desfazer não restaurou a limpeza")
	}
}

func TestAWSIndicatorShowsStateProfileAndRegion(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.d.settings.Profile = "perfil-selecionado"
	h.d.settings.Region = "us-west-2"
	h.d.setAWSStatus("AWS conectada", accentColor)
	if h.d.awsIndicator.color != theme.ColorNameSuccess ||
		h.d.awsIndicator.hint.text != "AWS conectada · perfil-selecionado · us-west-2" {
		t.Fatal("a conexão perdeu a indicação verde ou o perfil no tooltip")
	}
	h.d.setAWSStatus("AWS: verificar login", recordingColor)
	if h.d.awsIndicator.color != theme.ColorNameError {
		t.Fatal("a falha de conexão não foi indicada em vermelho")
	}
	h.d.awsIndicator.MouseIn(&fynedesktop.MouseEvent{PointEvent: fyne.PointEvent{}})
	if h.d.awsIndicator.hint.box == nil {
		t.Fatal("o indicador não revelou a informação de login")
	}
	h.d.awsIndicator.MouseOut()
}

func TestTooltipKeepsHoverAndClickWhilePointerMoves(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.d.editor.SetText("Texto para verificar o clique com tooltip.")
	button := h.d.clearButton
	position := h.app.Driver().AbsolutePositionForObject(button).Add(fyne.NewPos(12, 12))
	var shown fyne.CanvasObject
	for step := range 6 {
		test.MoveMouse(h.d.window.Canvas(), position.Add(fyne.NewPos(float32(step), 0)))
		if button.hint.box == nil {
			t.Fatalf("o tooltip desapareceu no movimento %d dentro do botão", step+1)
		}
		if shown != nil && shown != button.hint.box {
			t.Fatal("o tooltip foi recriado enquanto o ponteiro permaneceu no botão")
		}
		shown = button.hint.box
	}
	if len(h.d.window.Canvas().Overlays().List()) != 0 {
		t.Fatal("o tooltip tomou a camada que recebe eventos de mouse")
	}
	test.TapCanvas(h.d.window.Canvas(), position)
	if h.d.editor.Text != "" || button.hint.box != nil || h.d.undoButton.Disabled() {
		t.Fatal("o tooltip impediu o clique no controle ou perdeu o desfazer")
	}
}

func TestTooltipStaysInsideCanvasAndDoesNotChangeMinimumWindowSize(t *testing.T) {
	h := newHarness(t, nil, nil)
	minimum := h.d.window.Content().MinSize()
	for _, target := range []*actionButton{h.d.settingsButton, h.d.folderButton} {
		position := h.app.Driver().AbsolutePositionForObject(target).Add(fyne.NewPos(12, 12))
		test.MoveMouse(h.d.window.Canvas(), position)
		h.d.window.Resize(h.d.window.Content().MinSize())
		box := target.hint.box
		if box == nil || box.Position().X < 0 || box.Position().Y < 0 ||
			box.Position().X+box.Size().Width > h.d.window.Canvas().Size().Width ||
			box.Position().Y+box.Size().Height > h.d.window.Canvas().Size().Height ||
			h.d.window.Content().MinSize() != minimum {
			t.Fatal("o tooltip saiu da janela ou aumentou o tamanho mínimo do aplicativo")
		}
		test.MoveMouse(h.d.window.Canvas(), fyne.NewPos(0, 0))
		if target.hint.box != nil {
			t.Fatal("sair do controle manteve o tooltip na tela")
		}
	}
}
