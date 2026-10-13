package desktop

import (
	"context"
	"sync"
	"testing"

	"ai-talker/internal/config"
	"ai-talker/internal/processing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

func TestProcessingCollapsesAlongEachLayoutAndRestoresEditorsOptionsAndSize(t *testing.T) {
	for _, mode := range []string{config.LayoutColumn, config.LayoutInline, config.LayoutStacked} {
		t.Run(mode, func(t *testing.T) {
			h := newHarness(t, nil, nil)
			old := h.d.settings.Appearance
			h.d.settings.Appearance.Layout = mode
			h.d.applyAppearance(old, h.d.settings.Appearance)
			doc := h.d.document
			doc.editor.SetText("Transcrição conservada.")
			doc.processEditor.SetText("Resposta conservada.")
			doc.editors.SetOffset(0.62)
			options := doc.processOptions
			before := doc.processPanel.MinSize()
			test.Tap(doc.processCollapse)
			after := doc.processPanel.MinSize()
			if !doc.processCollapsed || doc.editor.Text != "Transcrição conservada." ||
				doc.processEditor.Text != "Resposta conservada." || doc.processOptions != options {
				t.Fatal("recolher perdeu conteúdo ou opções")
			}
			if mode == config.LayoutColumn && after.Height >= before.Height ||
				mode != config.LayoutColumn && after.Width >= before.Width {
				t.Fatal("o recolhimento não liberou a dimensão correspondente", before, after)
			}
			h.d.loadWorkspace()
			doc = h.d.document
			if !doc.processCollapsed {
				t.Fatal("a reabertura perdeu o recolhimento")
			}
			h.d.toggleProcessingPanel(doc)
			if doc.processCollapsed || doc.editors.Offset != 0.62 || doc.processEditor.Text != "Resposta conservada." {
				t.Fatal("abrir não restaurou o tamanho e o texto anteriores")
			}
			h.d.window.Resize(fyne.NewSize(540, 850))
		})
	}
}

func TestCollapsedProcessingStillCompletesInItsOriginTab(t *testing.T) {
	release := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	started := make(chan struct{}, 1)
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.process = func(context.Context, config.Settings, processing.Request) (processing.Result, error) {
			started <- struct{}{}
			<-release
			return processing.Result{Text: "Resposta concluída recolhida."}, nil
		}
	})
	t.Cleanup(unblock)
	doc := h.d.document
	doc.editor.SetText("Pedido.")
	test.Tap(doc.processButton)
	waitValue(t, started)
	test.Tap(doc.processCollapse)
	h.d.addDocument()
	unblock()
	h.await(func() bool { return !doc.processBusy })
	if !doc.processCollapsed || doc.processEditor.Text != "Resposta concluída recolhida." ||
		h.d.document.processEditor.Text != "" {
		t.Fatal("recolher ou trocar de aba perdeu/redirecionou o processamento")
	}
}
