package desktop

import (
	"context"
	"errors"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"ai-talker/internal/config"
	"ai-talker/internal/processing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
)

func TestAppearanceKeepsEditorsAndUsefulSizes(t *testing.T) {
	h := newHarness(t, nil, nil)
	first := h.d.document
	first.editor.SetText("Primeira transcrição.")
	first.processEditor.SetText("Primeiro resultado.")
	second := h.d.addDocument()
	second.editor.SetText("Segunda transcrição.")
	second.processEditor.SetText("Segundo resultado.")
	for _, mode := range []string{config.ThemeGreen, config.ThemeLight, config.ThemeDark} {
		for _, disposition := range []string{config.LayoutStacked, config.LayoutInline, config.LayoutColumn} {
			for _, size := range []string{config.RecordingLarge, config.RecordingMedium} {
				t.Run(mode+"/"+disposition+"/"+size, func(t *testing.T) {
					defer h.d.tabs.Select(second.item)
					old := h.d.settings.Appearance
					next := config.Appearance{Theme: mode, Layout: disposition, RecordingSize: size}
					h.d.settings.Appearance = next
					h.d.applyAppearance(old, next)
					h.d.window.Resize(h.d.window.Content().MinSize())
					if h.d.document != second || first.editor.Text != "Primeira transcrição." ||
						second.processEditor.Text != "Segundo resultado." || len(h.d.documents) != 2 {
						t.Fatal("mudar a aparência alterou os editores ou seleção")
					}
					for _, doc := range []*document{first, second} {
						h.d.tabs.Select(doc.item)
						if doc.editor.Size().Height < doc.editor.MinSize().Height ||
							doc.processEditor.Size().Height < doc.processEditor.MinSize().Height {
							t.Fatalf("os editores ficaram pequenos: %v, %v", doc.editor.Size(), doc.processEditor.Size())
						}
					}
					h.d.tabs.Select(second.item)
					if disposition == config.LayoutInline && h.d.rowSplit == nil {
						t.Fatal("a opção lado a lado não foi aplicada")
					}
					if second.editors.Horizontal == (disposition == config.LayoutColumn) {
						t.Fatal("os editores não acompanham a disposição em coluna")
					}
					if h.d.themeButton.Text != "" || h.d.themeButton.hint.text != "Mudar tema · "+themeName(mode) {
						t.Fatal("o botão de tema não acompanha a preferência")
					}
				})
			}
		}
	}
}

func TestMediumRecordingPanelIsShorterInAllLayouts(t *testing.T) {
	h := newHarness(t, nil, nil)
	for _, disposition := range []string{config.LayoutStacked, config.LayoutInline, config.LayoutColumn} {
		old := h.d.settings.Appearance
		large := old
		large.Layout, large.RecordingSize = disposition, config.RecordingLarge
		h.d.settings.Appearance = large
		h.d.applyAppearance(old, large)
		largeHeight := h.d.recordPanel.Size().Height
		medium := large
		medium.RecordingSize = config.RecordingMedium
		h.d.settings.Appearance = medium
		h.d.applyAppearance(large, medium)
		mediumHeight := h.d.recordPanel.Size().Height
		if largeHeight-mediumHeight < 20 {
			t.Fatalf("%s: o modo Médio não reduziu a altura: grande=%v, médio=%v", disposition, largeHeight, mediumHeight)
		}
	}
}

func TestColumnLayoutOrdersBoxesAndPersistsWithoutReplacingEditors(t *testing.T) {
	h := newHarness(t, nil, nil)
	doc := h.d.document
	doc.editor.SetText("Transcrição preservada na coluna.")
	doc.processEditor.SetText("Resultado preservado na coluna.")
	sourceEditor, resultEditor := doc.editor, doc.processEditor
	for _, disposition := range []string{inlineChoice, stackedChoice, columnChoice} {
		h.d.openSettings()
		h.await(func() bool { return !h.d.settingsForm.loading })
		h.d.settingsForm.layout.SetSelected(disposition)
		test.Tap(h.d.settingsForm.save)
		h.await(func() bool { return h.d.settingsForm == nil })
	}
	saved, err := config.Load(h.d.directory)
	if err != nil || saved.Appearance.Layout != config.LayoutColumn || doc.editors.Horizontal ||
		doc.editor != sourceEditor || doc.processEditor != resultEditor ||
		doc.editor.Text != "Transcrição preservada na coluna." || doc.processEditor.Text != "Resultado preservado na coluna." {
		t.Fatal("selecionar a coluna não persistiu ou substituiu os editores")
	}
	h.d.window.Resize(h.d.initialWindowSize())
	recorderPosition := h.app.Driver().AbsolutePositionForObject(h.d.recordPanel)
	transcriptionPosition := h.app.Driver().AbsolutePositionForObject(doc.editors.Leading)
	processingPosition := h.app.Driver().AbsolutePositionForObject(doc.editors.Trailing)
	if transcriptionPosition.Y < recorderPosition.Y+h.d.recordPanel.Size().Height ||
		processingPosition.Y < transcriptionPosition.Y+doc.editors.Leading.Size().Height ||
		transcriptionPosition.X != processingPosition.X ||
		doc.editor.Size().Height < doc.editor.MinSize().Height ||
		doc.processEditor.Size().Height < doc.processEditor.MinSize().Height {
		t.Fatalf("a coluna não ordenou os três boxes ou encolheu os editores: gravador %v/%v, transcrição %v/%v, processar %v/%v, editores %v/%v",
			recorderPosition, h.d.recordPanel.Size(), transcriptionPosition, doc.editors.Leading.Size(),
			processingPosition, doc.editors.Trailing.Size(), doc.editor.Size(), doc.processEditor.Size())
	}
	if err := h.d.saveWorkspace(); err != nil {
		t.Fatal(err)
	}
	restored := newHarness(t, nil, nil)
	restored.d.directory = h.d.directory
	if !restored.d.loadWorkspace() || restored.d.document.editors.Horizontal ||
		restored.d.editor.Text != doc.editor.Text || restored.d.processEditor.Text != doc.processEditor.Text {
		t.Fatal("reabrir não restaurou os dois editores em coluna")
	}
}

func TestLightThemeRefreshesExistingEditorsAndSemanticWidgets(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.d.editor.SetText("Texto preservado ao mudar a paleta.")
	entryTheme := h.d.editor.Theme()
	oldSurface := entryTheme.Color(theme.ColorNameInputBackground, theme.VariantDark)
	h.d.setTheme(config.ThemeLight)
	h.settle()
	newSurface := h.d.editor.Theme().Color(theme.ColorNameInputBackground, theme.VariantDark)
	if oldSurface == newSurface || newSurface != color.Color(rgb(0xffffff)) ||
		h.d.editor.Text != "Texto preservado ao mudar a paleta." {
		t.Fatal("o editor existente não acompanhou o tema claro")
	}
	rendered := test.WidgetRenderer(h.d.timer).Objects()[0]
	if rendered.MinSize().Height == 0 {
		t.Fatal("o contador perdeu seu texto ao atualizar a paleta")
	}
	loaded, err := config.Load(h.d.directory)
	if err != nil || loaded.Appearance.Theme != config.ThemeLight {
		t.Fatalf("o tema não foi persistido: %+v, %v", loaded.Appearance, err)
	}
}

func TestAppearanceWriteFailureRestoresThemeAndLayout(t *testing.T) {
	h := newHarness(t, nil, nil)
	path := filepath.Join(h.d.directory, "settings.json")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	h.d.setTheme(config.ThemeLight)
	h.await(func() bool { return h.d.errorLabel.Visible() })
	if h.d.settings.Appearance != config.DefaultAppearance() || h.d.themeButton.hint.text != "Mudar tema · Escuro" ||
		h.d.palette.Color(theme.ColorNameBackground, theme.VariantDark) != color.Color(rgb(0x13171e)) {
		t.Fatal("falhar ao salvar deixou a aparência diferente da preferência")
	}
}

func TestSettingsAndThemeRemainAvailableDuringRecordingAndProcessing(t *testing.T) {
	capturedRecorder := &fakeRecording{}
	processingStarted := make(chan struct{}, 1)
	release := make(chan struct{})
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.newRecorder = func(int, string) recording { return capturedRecorder }
		deps.process = func(ctx context.Context, _ config.Settings, _ processing.Request) (processing.Result, error) {
			processingStarted <- struct{}{}
			select {
			case <-release:
				return processing.Result{Text: "Resultado válido."}, nil
			case <-ctx.Done():
				return processing.Result{}, ctx.Err()
			}
		}
	})
	h.d.editor.SetText("Texto para processar.")
	processingDoc := h.d.document
	test.Tap(h.d.processButton)
	waitValue(t, processingStarted)
	h.d.addDocument()
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	test.Tap(h.d.settingsButton)
	form := h.d.settingsForm
	if form == nil {
		t.Fatal("as configurações não abriram durante a gravação")
	}
	h.await(func() bool { return !form.loading })
	form.layout.SetSelected(inlineChoice)
	form.recordingSize.SetSelected(mediumChoice)
	test.Tap(form.save)
	h.await(func() bool { return h.d.settingsForm == nil })
	h.d.setTheme(config.ThemeDark)
	h.await(func() bool { return h.d.settings.Appearance.Theme == config.ThemeDark })
	if h.d.capture != captureRunning || capturedRecorder.Snapshot().Paused || !processingDoc.processBusy {
		t.Fatal("as preferências interromperam a gravação ou o processamento")
	}
	close(release)
	h.await(func() bool { return !processingDoc.processBusy })
	if processingDoc.processEditor.Text != "Resultado válido." {
		t.Fatal("a resposta em andamento foi perdida ao mudar o visual")
	}
}

func TestMainRecordingSizeTogglePersistsWithoutInterruptingCapture(t *testing.T) {
	r := &fakeRecording{}
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.newRecorder = func(int, string) recording { return r }
	})
	first := h.d.document
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	h.d.editor.SetText("Texto preservado.")
	h.d.processEditor.SetText("Resultado preservado.")
	for _, expected := range []string{config.RecordingMedium, config.RecordingSmall, config.RecordingLarge} {
		test.Tap(h.d.sizeButton)
		h.settle()
		saved, err := config.Load(h.d.directory)
		if err != nil || saved.Appearance.RecordingSize != expected ||
			h.d.settings.Appearance.RecordingSize != expected || !r.Snapshot().Active ||
			h.d.capture != captureRunning || h.d.captureDocument != first ||
			h.d.editor.Text != "Texto preservado." || h.d.processEditor.Text != "Resultado preservado." {
			t.Fatal("mudar o tamanho no box não persistiu ou interrompeu a captura e os textos")
		}
	}
}

func TestMainRecordingSizeToggleRollsBackOnSaveFailure(t *testing.T) {
	h := newHarness(t, nil, nil)
	originalHeight := h.d.recordPanel.MinSize().Height
	if err := os.Mkdir(filepath.Join(h.d.directory, "settings.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	test.Tap(h.d.sizeButton)
	h.await(func() bool { return h.d.errorLabel.Visible() })
	if h.d.settings.Appearance.RecordingSize != config.RecordingLarge ||
		h.d.sizeButton.hint.text != "Reduzir gravação para tamanho Médio" ||
		h.d.recordPanel.MinSize().Height != originalHeight {
		t.Fatal("o tamanho ou seu ícone não voltaram à preferência depois da falha ao salvar")
	}
}

// This optional capture uses only synthetic text and the headless renderer.
// Normal tests do not create images or read desktop pixels.
func TestAppearancePreview(t *testing.T) {
	output := os.Getenv("AI_TALKER_UI_SNAPSHOTS")
	if output == "" {
		t.Skip("defina AI_TALKER_UI_SNAPSHOTS para renderizar as variantes de aparência")
	}
	if err := os.MkdirAll(output, 0o700); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, nil, nil)
	h.d.editor.SetText("Quero mandar uma mensagem para o Mendes num tom cordial dizendo que a revisão da proposta terminou e consigo enviar hoje até as 15h.")
	h.d.processEditor.SetText("Olá, Mendes!\n\nConcluí a revisão da proposta e consigo enviá-la hoje até as 15h.")
	h.d.setAWSStatus("AWS conectada", accentColor)
	capturePreview := func(name string) {
		h.settle()
		h.d.window.Content().Refresh()
		file, err := os.Create(filepath.Join(output, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		if err := errors.Join(png.Encode(file, h.d.window.Canvas().Capture()), file.Close()); err != nil {
			t.Fatal(err)
		}
	}
	for _, mode := range []string{config.ThemeGreen, config.ThemeLight, config.ThemeDark} {
		for _, disposition := range []string{config.LayoutStacked, config.LayoutInline, config.LayoutColumn} {
			for _, size := range []string{config.RecordingLarge, config.RecordingMedium} {
				old := h.d.settings.Appearance
				next := config.Appearance{Theme: mode, Layout: disposition, RecordingSize: size}
				h.d.settings.Appearance = next
				h.d.applyAppearance(old, next)
				h.d.window.Resize(h.d.initialWindowSize())
				h.d.window.Content().Refresh()
				path := filepath.Join(output, mode+"-"+disposition+"-"+size+".png")
				file, err := os.Create(path)
				if err != nil {
					t.Fatal(err)
				}
				err = png.Encode(file, h.d.window.Canvas().Capture())
				if err = errors.Join(err, file.Close()); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	old := h.d.settings.Appearance
	h.d.settings.Appearance = config.DefaultAppearance()
	h.d.applyAppearance(old, h.d.settings.Appearance)
	h.d.window.Resize(h.d.initialWindowSize())
	for _, disposition := range []string{config.LayoutColumn, config.LayoutInline, config.LayoutStacked} {
		old = h.d.settings.Appearance
		h.d.settings.Appearance.Layout = disposition
		h.d.applyAppearance(old, h.d.settings.Appearance)
		h.d.window.Resize(h.d.initialWindowSize())
		h.d.toggleProcessingPanel(h.d.document)
		capturePreview("dark-" + disposition + "-processing-collapsed")
		h.d.toggleProcessingPanel(h.d.document)
		h.d.toggleTranscriptionPanel(h.d.document)
		capturePreview("dark-" + disposition + "-transcription-collapsed")
		h.d.toggleProcessingPanel(h.d.document)
		capturePreview("dark-" + disposition + "-both-collapsed")
		h.d.toggleTranscriptionPanel(h.d.document)
		h.d.toggleProcessingPanel(h.d.document)
		old = h.d.settings.Appearance
		h.d.settings.Appearance.InvertPanels = true
		h.d.applyAppearance(old, h.d.settings.Appearance)
		capturePreview("dark-" + disposition + "-inverted")
		old = h.d.settings.Appearance
		h.d.settings.Appearance.InvertPanels = false
		h.d.applyAppearance(old, h.d.settings.Appearance)
	}
	old = h.d.settings.Appearance
	h.d.settings.Appearance = config.DefaultAppearance()
	h.d.applyAppearance(old, h.d.settings.Appearance)
	h.d.window.Resize(h.d.initialWindowSize())
	h.d.chooseTranscriptionProvider()
	capturePreview("dark-column-engine-menu")
	h.d.window.Canvas().Overlays().Top().Hide()
	h.d.chooseTranscriptionModel()
	capturePreview("dark-column-cloud-model-menu")
	h.d.window.Canvas().Overlays().Top().Hide()
	h.d.setTranscriptionProvider(config.ProviderWhisper)
	h.settle()
	h.d.chooseTranscriptionModel()
	capturePreview("dark-column-whisper-model-menu")
	h.d.window.Canvas().Overlays().Top().Hide()
	h.d.setTranscriptionProvider(config.ProviderBedrock)
	h.settle()
	closed := h.d.addDocument()
	closed.editor.SetText("Rascunho de uma aba fechada para testar o desfazer.")
	h.d.closeDocument(closed.item)
	position := h.app.Driver().AbsolutePositionForObject(h.d.clearButton).Add(fyne.NewPos(12, 12))
	for step := range 6 {
		test.MoveMouse(h.d.window.Canvas(), position.Add(fyne.NewPos(float32(step), 0)))
	}
	file, err := os.Create(filepath.Join(output, h.d.settings.Appearance.Theme+"-"+h.d.settings.Appearance.Layout+"-tooltip-undo.png"))
	if err != nil {
		t.Fatal(err)
	}
	err = png.Encode(file, h.d.window.Canvas().Capture())
	if err = errors.Join(err, file.Close()); err != nil {
		t.Fatal(err)
	}
	test.MoveMouse(h.d.window.Canvas(), fyne.NewPos(0, 0))
	h.d.settings.Capture.Mode = config.CaptureTeam
	for _, volumes := range []struct {
		name       string
		microphone int
		playback   int
	}{
		{"dark-column-team", 100, 100},
		{"dark-column-team-volumes", 45, 130},
	} {
		h.d.settings.Capture.MicrophoneVolume = &volumes.microphone
		h.d.settings.Capture.PlaybackVolume = &volumes.playback
		h.d.syncCaptureOptions()
		h.d.window.Content().Refresh()
		file, err := os.Create(filepath.Join(output, volumes.name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(file, h.d.window.Canvas().Capture())
		if err := errors.Join(err, file.Close()); err != nil {
			t.Fatal(err)
		}
	}
	h.d.settings.Capture.Mode = config.CaptureMicrophone
	h.d.settings.Capture.MicrophoneVolume, h.d.settings.Capture.PlaybackVolume = nil, nil
	h.d.syncCaptureOptions()
	h.d.window.Resize(fyne.NewSize(1000, 740))
	h.d.openSettings()
	h.await(func() bool { return !h.d.settingsForm.loading })
	h.d.settingsForm.dialog.Resize(fyne.NewSize(760, 660))
	for index, name := range []string{"settings", "settings-audio", "settings-visual", "settings-llm", "settings-instructions", "settings-whisper", "settings-cloud", "settings-api"} {
		h.d.settingsForm.sections.SelectIndex(index)
		file, err := os.Create(filepath.Join(output, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(file, h.d.window.Canvas().Capture())
		if err := errors.Join(err, file.Close()); err != nil {
			t.Fatal(err)
		}
	}
	h.d.settingsForm.dialog.Hide()
	h.d.window.Resize(h.d.window.Content().MinSize())
	file, err = os.Create(filepath.Join(output, "dark-column-minimum.png"))
	if err != nil {
		t.Fatal(err)
	}
	err = png.Encode(file, h.d.window.Canvas().Capture())
	if err := errors.Join(err, file.Close()); err != nil {
		t.Fatal(err)
	}
	h.d.window.Resize(h.d.initialWindowSize())
	h.d.setTranscriptionProvider(config.ProviderWhisper)
	h.settle()
	file, err = os.Create(filepath.Join(output, "dark-column-whisper.png"))
	if err != nil {
		t.Fatal(err)
	}
	err = png.Encode(file, h.d.window.Canvas().Capture())
	if err := errors.Join(err, file.Close()); err != nil {
		t.Fatal(err)
	}
	first := h.d.document
	h.d.addDocument().editor.SetText("Outra transcrição no Grupo 1.")
	h.d.addGroup().editor.SetText("Uma transcrição independente no Grupo 2.")
	h.d.addDocument().editor.SetText("Outra transcrição no Grupo 2.")
	h.d.tabs.Select(first.item)
	for _, capture := range []struct {
		name string
		size fyne.Size
	}{
		{"dark-column-groups", h.d.initialWindowSize()},
		{"dark-column-groups-minimum", h.d.window.Content().MinSize()},
	} {
		h.d.window.Resize(capture.size)
		file, err := os.Create(filepath.Join(output, capture.name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(file, h.d.window.Canvas().Capture())
		if err := errors.Join(err, file.Close()); err != nil {
			t.Fatal(err)
		}
	}
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	h.d.requestDiscardRecording()
	file, err = os.Create(filepath.Join(output, "dark-column-discard-confirmation.png"))
	if err != nil {
		t.Fatal(err)
	}
	err = png.Encode(file, h.d.window.Canvas().Capture())
	if err := errors.Join(err, file.Close()); err != nil {
		t.Fatal(err)
	}
	h.d.window.Canvas().Overlays().Top().Hide()
	source := historyAudio(t, h.d.directory, "20261009-124850.wav")
	h.d.captureOrigin.recordingSource = source
	h.d.caption.Text = recordingLabel(source)
	h.d.copyAudioPath.Show()
	h.d.window.Resize(fyne.NewSize(1600, 900))
	for _, size := range []string{config.RecordingMedium, config.RecordingLarge} {
		h.d.resizeRecordingPanel(size)
		capturePreview("dark-column-wide-recording-" + size)
	}
}
