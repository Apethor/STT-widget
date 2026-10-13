package desktop

import (
	"testing"

	"ai-talker/internal/config"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

func TestQualityBarsFollowSavedModeAcrossTabsAndKeepHover(t *testing.T) {
	h := newHarness(t, nil, nil)
	first := h.d.document
	second := h.d.addDocument()
	first.editor.SetText("Primeira aba preservada.")
	second.editor.SetText("Segunda aba preservada.")
	updated := cloneSettings(h.d.settings)
	updated.Quality = "fast"
	h.d.persistPreferences(updated, func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	})
	h.settle()
	saved, err := config.Load(h.d.directory)
	if err != nil || saved.Quality != "fast" || first.qualityBars.levels != 2 || second.qualityBars.levels != 2 {
		t.Fatal("o modo rápido não foi persistido e indicado em todas as abas")
	}
	position := h.app.Driver().AbsolutePositionForObject(second.qualityBars).Add(fyne.NewPos(12, 12))
	var shown fyne.CanvasObject
	for step := range 6 {
		test.MoveMouse(h.d.window.Canvas(), position.Add(fyne.NewPos(float32(step), 0)))
		box := second.qualityBars.hint.box
		if box == nil || shown != nil && box != shown {
			t.Fatal("as barras perderam ou recriaram o tooltip enquanto o ponteiro estava sobre elas")
		}
		shown = box
	}
	if second.qualityBars.hint.text != "Modo rápido · Voxtral 3B · clicar para escolher o modelo" ||
		len(h.d.window.Canvas().Overlays().List()) != 0 {
		t.Fatal("a qualidade perdeu sua descrição ou voltou a bloquear eventos")
	}
	h.d.tabs.Select(first.item)
	if second.qualityBars.hint.box != nil || first.editor.Text != "Primeira aba preservada." ||
		second.editor.Text != "Segunda aba preservada." {
		t.Fatal("trocar de aba manteve um tooltip antigo ou alterou o texto")
	}
}
