package desktop

import (
	"errors"
	"fmt"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ai-talker/internal/config"
	"ai-talker/internal/recorder"
	"ai-talker/internal/store"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

func setRecorderMode(h *testHarness, mode string) {
	h.d.resizeRecordingPanel(mode)
	h.d.window.Content().Refresh()
	h.settle()
}

func TestRecorderModesPreserveHeightAndArrangementWhenWidthChanges(t *testing.T) {
	for _, mode := range []string{config.RecordingLarge, config.RecordingMedium, config.RecordingSmall} {
		t.Run(mode, func(t *testing.T) {
			h := newHarness(t, nil, nil)
			h.d.window.Resize(fyne.NewSize(1700, 1000))
			setRecorderMode(h, mode)
			height := h.d.recordPanel.Size().Height
			for _, width := range []float32{2300, 480, 2300} {
				h.d.window.Resize(fyne.NewSize(width, 1000))
				h.d.window.Content().Refresh()
				h.settle()
				if h.d.recorderLayout.mode != mode {
					t.Fatalf("a largura %.0f mudou %s para %s", width, mode, h.d.recorderLayout.mode)
				}
				position := h.app.Driver().AbsolutePositionForObject
				clock, wave, button := position(h.d.timer), position(h.d.wave), position(h.d.recordButton)
				if mode != config.RecordingSmall {
					if button.Y <= clock.Y || wave.Y >= button.Y || position(h.d.newButton).Y >= clock.Y {
						t.Fatalf("%s comprimido em uma linha ao mudar a largura: relógio=%v gráfico=%v botão=%v",
							mode, clock, wave, button)
					}
				} else if width > 1000 && (wave.X < clock.X+h.d.timer.Size().Width || button.X < wave.X+h.d.wave.Size().Width) {
					t.Fatal("Pequeno largo não usou a linha compacta")
				}
				if width > 1000 && math.Abs(float64(h.d.recordPanel.Size().Height-height)) > 1 {
					t.Fatalf("alargar alterou a altura escolhida: %.1f para %.1f", height, h.d.recordPanel.Size().Height)
				}
				if h.d.window.Content().MinSize().Width > width {
					t.Fatal("os controles impediram a largura solicitada", mode, width, h.d.window.Content().MinSize())
				}
			}
		})
	}
}

func TestRecorderSmallShowsFileBeforeClockAndCentersMessage(t *testing.T) {
	r := &fakeRecording{}
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.newRecorder = func(int, string) recording { return r }
	})
	r.update(func(state *recorder.Snapshot) {
		state.Path = historyAudio(t, h.d.directory, "20261012-124850.wav")
	})
	h.d.window.Resize(fyne.NewSize(1700, 900))
	setRecorderMode(h, config.RecordingSmall)
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	h.d.window.Content().Refresh()
	position := h.app.Driver().AbsolutePositionForObject
	copyPath, caption, clock := position(h.d.copyAudioPath), position(h.d.caption), position(h.d.timer)
	if copyPath.X >= caption.X || caption.X+h.d.caption.Size().Width > clock.X {
		t.Fatal("Copiar caminho e arquivo não vieram antes do relógio", copyPath, caption, clock)
	}
	inner := position(h.d.recorderLayout.surface)
	message := position(h.d.recordState)
	center := message.Y + h.d.recordState.Size().Height/2
	want := inner.Y + h.d.recorderLayout.surface.Size().Height/2
	if math.Abs(float64(center-want)) > 1 {
		t.Fatalf("a mensagem não ficou centralizada verticalmente: %.1f, esperado %.1f", center, want)
	}
	firstWidth, height := h.d.recordButton.Size().Width, h.d.recordPanel.Size().Height
	h.d.window.Resize(fyne.NewSize(2300, 900))
	h.d.window.Content().Refresh()
	if h.d.recordButton.Size().Width <= firstWidth+20 || math.Abs(float64(h.d.recordPanel.Size().Height-height)) > 1 {
		t.Fatal("alargar não aumentou o botão preservando o Pequeno")
	}
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureIdle && !h.d.busy })
}

func TestRecorderMediumMovesMessageIntoHeaderOnlyWhenItFits(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.d.recordState.SetText("Pode falar. Estou gravando.")
	h.d.lastAudio = historyAudio(t, h.d.directory, "20261012-124850.wav")
	h.d.syncRecordingName()
	h.d.window.Resize(fyne.NewSize(480, 900))
	setRecorderMode(h, config.RecordingMedium)
	position := h.app.Driver().AbsolutePositionForObject
	if position(h.d.recordState).Y < position(h.d.wave).Y+h.d.wave.Size().Height-1 {
		t.Fatal("Médio estreito não deixou a mensagem abaixo da waveform")
	}
	h.d.window.Resize(fyne.NewSize(1500, 900))
	h.d.window.Content().Refresh()
	message, caption, tools := position(h.d.recordState), position(h.d.caption), position(h.d.newButton)
	if math.Abs(float64(message.Y-caption.Y)) > 5 ||
		message.X < caption.X+h.d.caption.Size().Width || message.X+h.d.recordState.Size().Width > tools.X {
		t.Fatal("Médio largo não colocou a mensagem entre o arquivo e os atalhos", message, caption, tools)
	}
	if position(h.d.recordButton).Y <= position(h.d.timer).Y {
		t.Fatal("Médio virou uma linha ao mover a mensagem")
	}
	if h.d.caption.Size().Width+0.5 < fyne.MeasureText(h.d.caption.Text, h.d.caption.TextSize, h.d.caption.TextStyle).Width {
		t.Fatal("Médio largo abreviou o arquivo mesmo com espaço para exibir seu rótulo")
	}
}

func TestRecorderDividerSelectsThreeModesAndPreservesActiveCapture(t *testing.T) {
	r := &fakeRecording{}
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.newRecorder = func(int, string) recording { return r }
	})
	h.d.window.Resize(fyne.NewSize(1700, 1000))
	doc := h.d.document
	doc.editor.SetText("Texto preservado.")
	doc.processEditor.SetText("Resposta preservada.")
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	dragColumnHeight(h, 400)
	dragDivider(h, h.d.rowSplit, 0, -1000)
	h.settle()
	if h.d.recorderLayout.mode != config.RecordingSmall {
		t.Fatal("a altura mínima não selecionou Pequeno")
	}
	mediumHeight := h.d.recorderLayout.stackedNaturalSize(config.RecordingMedium).Height + 32
	dragDivider(h, h.d.rowSplit, 0, mediumHeight-h.d.recordPanel.Size().Height)
	h.settle()
	if h.d.recorderLayout.mode != config.RecordingMedium {
		t.Fatal("a altura intermediária não selecionou Médio", h.d.recordPanel.Size().Height)
	}
	largeHeight := h.d.recorderLayout.stackedNaturalSize(config.RecordingLarge).Height + 40
	dragDivider(h, h.d.rowSplit, 0, largeHeight-h.d.recordPanel.Size().Height)
	h.settle()
	saved, err := config.Load(h.d.directory)
	if err != nil || saved.Appearance.RecordingSize != config.RecordingLarge || h.d.recorderLayout.mode != config.RecordingLarge {
		t.Fatal("a altura grande não persistiu o modo", saved.Appearance, err)
	}
	if doc.panelSizes.RecorderHeight < largeHeight-1 || doc.panelSizes.RecorderMode != config.RecordingLarge ||
		h.d.capture != captureRunning || !r.Snapshot().Active || h.d.captureDocument != doc ||
		doc.editor.Text != "Texto preservado." || doc.processEditor.Text != "Resposta preservada." {
		t.Fatal("o ajuste perdeu a altura, captura ou textos")
	}
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureIdle && !h.d.busy })
}

func TestRecorderDividerModeSaveFailureRestoresThePreferenceAndIcon(t *testing.T) {
	h := newHarness(t, nil, nil)
	if err := os.Mkdir(filepath.Join(h.d.directory, "settings.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	dragDivider(h, h.d.rowSplit, 0, -400)
	h.await(func() bool { return h.d.errorLabel.Visible() })
	if h.d.settings.Appearance.RecordingSize != config.RecordingLarge || h.d.recorderLayout.mode != config.RecordingLarge {
		t.Fatal("falhar ao salvar deixou o modo diferente da preferência")
	}
	if !h.d.loadWorkspace() || h.d.document.panelSizes.RecorderMode != config.RecordingLarge ||
		h.d.recorderLayout.mode != config.RecordingLarge {
		t.Fatal("reabrir a sessão recuperou o tamanho rejeitado em vez do modo restaurado")
	}
}

func TestRecorderDividerReportsPreferencesAndWorkspaceRollbackFailures(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.d.settings.Region = ""
	writes := 0
	h.d.deps.writeWorkspace = func(path string, snapshot workspaceSnapshot) error {
		writes++
		if writes == 1 {
			return store.WriteJSON(path, snapshot)
		}
		return errors.New("sessão recusada ao restaurar a divisória")
	}
	dragDivider(h, h.d.rowSplit, 0, -400)
	h.settle()
	if writes != 2 || !strings.Contains(h.d.errorLabel.Text, "não foi possível salvar as configurações") ||
		!strings.Contains(h.d.errorLabel.Text, "informe uma região AWS") ||
		!strings.Contains(h.d.errorLabel.Text, "sessão recusada ao restaurar a divisória") ||
		h.d.recorderLayout.mode != config.RecordingLarge {
		t.Fatal("a falha de rollback apagou o erro das preferências ou deixou de restaurar a interface", h.d.errorLabel.Text)
	}
}

func TestRecorder011Preview(t *testing.T) {
	output := os.Getenv("AI_TALKER_UI_SNAPSHOTS")
	if output == "" {
		t.Skip("defina AI_TALKER_UI_SNAPSHOTS para conferir os três modos")
	}
	if err := os.MkdirAll(output, 0o700); err != nil {
		t.Fatal(err)
	}
	r := &fakeRecording{}
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.newRecorder = func(int, string) recording { return r }
	})
	r.update(func(state *recorder.Snapshot) {
		state.Path = historyAudio(t, h.d.directory, "20261012-124850.wav")
		state.Duration, state.Level = 90, 0.35
	})
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	for range 20 {
		h.d.tick()
	}
	for _, mode := range []string{config.RecordingLarge, config.RecordingMedium, config.RecordingSmall} {
		for _, width := range []float32{480, 1700} {
			h.d.window.Resize(fyne.NewSize(width, 900))
			setRecorderMode(h, mode)
			file, err := os.Create(filepath.Join(output, fmt.Sprintf("recorder-%s-%.0f.png", mode, width)))
			if err != nil {
				t.Fatal(err)
			}
			if err := errors.Join(png.Encode(file, h.d.window.Canvas().Capture()), file.Close()); err != nil {
				t.Fatal(err)
			}
		}
	}
	h.d.toggleRecorderPanel()
	file, err := os.Create(filepath.Join(output, "recorder-collapsed.png"))
	if err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(png.Encode(file, h.d.window.Canvas().Capture()), file.Close()); err != nil {
		t.Fatal(err)
	}
	test.Tap(h.d.recorderLayout.surface.stop)
	h.await(func() bool { return h.d.capture == captureIdle && !h.d.busy })
}
