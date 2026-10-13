package desktop

import (
	"math"
	"testing"
	"time"

	"ai-talker/internal/config"

	"fyne.io/fyne/v2/test"
)

func TestPanelOrderCollapseAndSizesRemainIndependentPerTab(t *testing.T) {
	for _, layout := range []string{config.LayoutColumn, config.LayoutInline, config.LayoutStacked} {
		for _, inverted := range []bool{false, true} {
			t.Run(layout+map[bool]string{false: "/normal", true: "/inverted"}[inverted], func(t *testing.T) {
				h := newHarness(t, nil, nil)
				previous := h.d.settings.Appearance
				h.d.settings.Appearance.Layout, h.d.settings.Appearance.InvertPanels = layout, inverted
				h.d.applyAppearance(previous, h.d.settings.Appearance)
				first := h.d.document
				first.editor.SetText("Texto preservado.")
				first.processEditor.SetText("Resposta preservada.")
				first.editors.SetOffset(0.64)
				h.d.columnHeight = 1300
				if h.d.rowSplit != nil {
					h.d.rowSplit.SetOffset(0.35)
				}
				second := h.d.addDocument()
				if math.Abs(second.editors.Offset-0.64) > 0.001 || second.panelSizes.ColumnHeight != 1300 {
					t.Fatal("a nova aba não herdou os tamanhos da aba ativa", second.panelSizes)
				}
				second.editors.SetOffset(0.42)
				h.d.columnHeight = 1600
				h.d.tabs.Select(first.item)
				if math.Abs(first.editors.Offset-0.64) > 0.001 || h.d.columnHeight != 1300 {
					t.Fatal("trocar de aba perdeu o tamanho independente")
				}
				for _, button := range []*actionButton{first.transcriptCollapse, first.processCollapse} {
					test.Tap(button)
				}
				if !first.transcriptCollapsed || !first.processCollapsed || second.transcriptCollapsed || second.processCollapsed {
					t.Fatal("o recolhimento não pertence somente à aba")
				}
				if !h.d.loadWorkspace() {
					t.Fatal("a sessão não reabriu")
				}
				first = h.d.document
				if !first.transcriptCollapsed || !first.processCollapsed {
					t.Fatal("reabrir perdeu o recolhimento independente")
				}
				test.Tap(first.transcriptCollapse)
				test.Tap(first.processCollapse)
				if math.Abs(first.editors.Offset-0.64) > 0.001 || first.editor.Text != "Texto preservado." ||
					first.processEditor.Text != "Resposta preservada." {
					t.Fatal("abrir não restaurou os editores e a divisória")
				}
				leading := first.transcriptPanel
				if inverted {
					leading = first.processPanel
				}
				if first.editors.Leading != leading {
					t.Fatal("a ordem escolhida não foi aplicada")
				}
				h.d.tabs.Select(h.d.documents[1].item)
				if math.Abs(h.d.document.editors.Offset-0.42) > 0.001 || h.d.columnHeight != 1600 {
					t.Fatal("o tamanho da segunda aba foi sobrescrito")
				}
			})
		}
	}
}

func TestNewGroupInheritsActivePanelSizesAndUndoKeepsReplacementAdjustment(t *testing.T) {
	now := time.Now()
	h := newHarness(t, nil, func(deps *dependencies) { deps.now = func() time.Time { return now } })
	h.d.document.editors.SetOffset(0.68)
	h.d.columnHeight = 1700
	h.d.addGroup()
	if h.d.document.editors.Offset != 0.68 || h.d.columnHeight != 1700 {
		t.Fatal("o novo grupo perdeu os tamanhos da aba ativa")
	}
	h.d.closeGroup(h.d.document.group)
	doc := h.d.document
	h.d.closeDocument(doc.item)
	replacement := h.d.document
	replacement.editors.SetOffset(0.31)
	h.d.undoCloseDocument()
	if replacement.closed || len(h.d.documents) != 2 {
		t.Fatal("restaurar apagou o ajuste realizado na aba substituta")
	}
}
