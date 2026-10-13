package desktop

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"ai-talker/internal/config"
	"ai-talker/internal/recorder"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

func TestRecorderCollapseKeepsActiveCaptureAndStopInAllLayouts(t *testing.T) {
	for _, layout := range []string{config.LayoutColumn, config.LayoutInline, config.LayoutStacked} {
		t.Run(layout, func(t *testing.T) {
			r := &fakeRecording{}
			h := newHarness(t, nil, func(deps *dependencies) {
				deps.newRecorder = func(int, string) recording { return r }
			})
			r.update(func(state *recorder.Snapshot) {
				state.Path = historyAudio(t, h.d.directory, "20261012-124850.wav")
			})
			previous := h.d.settings.Appearance
			h.d.settings.Appearance.Layout = layout
			h.d.applyAppearance(previous, h.d.settings.Appearance)
			doc := h.d.document
			doc.editor.SetText("Texto preservado.")
			doc.processEditor.SetText("Resposta preservada.")
			test.Tap(h.d.recordButton)
			h.await(func() bool { return h.d.capture == captureRunning })
			height := h.d.recordPanel.Size().Height
			h.d.toggleRecorderPanel()
			surface := h.d.recorderLayout.surface
			if !doc.panelSizes.RecorderCollapsed || h.d.capture != captureRunning || !r.Snapshot().Active ||
				!surface.stop.Visible() || surface.stop.Disabled() || surface.activity.Text != "Gravação ativa" ||
				h.d.recordPanel.Size().Height >= height {
				t.Fatalf("recolher: collapsed=%v capture=%v active=%v stop visible=%v disabled=%v activity=%q height=%.1f/%.1f",
					doc.panelSizes.RecorderCollapsed, h.d.capture, r.Snapshot().Active, surface.stop.Visible(),
					surface.stop.Disabled(), surface.activity.Text, h.d.recordPanel.Size().Height, height)
			}
			if surface.activity.Size().Width+0.5 < fyne.MeasureText(surface.activity.Text, 12, surface.activity.TextStyle).Width {
				t.Fatal("a indicação de captura ativa ficou abreviada apesar do espaço disponível")
			}
			test.Tap(surface.restore)
			if doc.panelSizes.RecorderCollapsed || math.Abs(float64(h.d.recordPanel.Size().Height-height)) > 1 {
				t.Fatal("expandir não restaurou a altura anterior", height, h.d.recordPanel.Size().Height)
			}
			h.d.toggleRecorderPanel()
			test.Tap(h.d.recorderLayout.surface.stop)
			h.await(func() bool { return h.d.capture == captureIdle && !h.d.busy })
			if h.d.lastAudio == "" || !doc.panelSizes.RecorderCollapsed || h.d.recorderLayout.surface.stop.Visible() ||
				doc.processEditor.Text != "Resposta preservada." {
				t.Fatalf("Parar recolhido: audio=%q collapsed=%v stop visible=%v result=%q",
					h.d.lastAudio, doc.panelSizes.RecorderCollapsed, h.d.recorderLayout.surface.stop.Visible(),
					doc.processEditor.Text)
			}
		})
	}
}

func TestRecorderCollapseBelongsToTabIsInheritedAndPersists(t *testing.T) {
	h := newHarness(t, nil, nil)
	first := h.d.document
	first.editor.SetText("Texto da primeira aba.")
	h.d.toggleRecorderPanel()
	second := h.d.addDocument()
	if !second.panelSizes.RecorderCollapsed {
		t.Fatal("a aba nova não herdou o gravador recolhido")
	}
	h.d.toggleRecorderPanel()
	h.d.tabs.Select(first.item)
	if !first.panelSizes.RecorderCollapsed || second.panelSizes.RecorderCollapsed ||
		!h.d.recorderLayout.surface.isCollapsed() {
		t.Fatal("o estado de uma aba alterou a outra")
	}
	h.d.addGroup()
	if !h.d.document.panelSizes.RecorderCollapsed {
		t.Fatal("a aba do grupo novo não herdou o recolhimento")
	}
	h.d.tabs.Select(first.item)
	if err := h.d.saveWorkspace(); err != nil {
		t.Fatal(err)
	}
	if !h.d.loadWorkspace() || !h.d.document.panelSizes.RecorderCollapsed ||
		h.d.documents[1].panelSizes.RecorderCollapsed || h.d.document.editor.Text != "Texto da primeira aba." {
		t.Fatal("reabrir não restaurou o estado independente e o texto")
	}
}

func TestRecorderCollapseSaveFailureRestoresThePanel(t *testing.T) {
	h := newHarness(t, nil, nil)
	if err := os.Mkdir(filepath.Join(h.d.directory, "workspace-go-v1.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	height := h.d.recordPanel.Size().Height
	h.d.toggleRecorderPanel()
	if h.d.document.panelSizes.RecorderCollapsed || !h.d.errorLabel.Visible() ||
		math.Abs(float64(h.d.recordPanel.Size().Height-height)) > 1 {
		t.Fatal("falhar ao salvar não restaurou o painel aberto")
	}
}
