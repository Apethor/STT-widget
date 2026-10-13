package screenshot

import (
	"bytes"
	"image"
	"image/color"
	"testing"
)

func testImage() *image.RGBA {
	pixels := image.NewRGBA(image.Rect(0, 0, 160, 80))
	for y := 0; y < 80; y++ {
		for x := 0; x < 160; x++ {
			pixels.SetRGBA(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 180, A: 255})
		}
	}
	return pixels
}

func TestStrokeEraserUndoAndRedoPreserveOriginalPixels(t *testing.T) {
	original := testImage()
	document, err := New(original)
	if err != nil {
		t.Fatal(err)
	}
	ink := color.NRGBA{R: 255, A: 255}
	document.BeginStroke(Point{20, 20}, 8, ink, false)
	document.ExtendStroke(Point{80, 20})
	document.EndStroke()
	if got := document.Snapshot().RGBAAt(50, 20); got != (color.RGBA{R: 255, A: 255}) {
		t.Fatalf("o pincel deixou um intervalo no traço: %v", got)
	}
	painted := document.Snapshot()
	document.BeginStroke(Point{50, 20}, 12, ink, true)
	document.EndStroke()
	if got := document.Snapshot().RGBAAt(50, 20); got != original.RGBAAt(50, 20) {
		t.Fatalf("a borracha não recuperou a captura original: %v", got)
	}
	if got := document.Snapshot().RGBAAt(30, 20); got != painted.RGBAAt(30, 20) {
		t.Fatal("a borracha removeu marcações fora de sua área")
	}
	document.Undo()
	if !bytes.Equal(document.Snapshot().Pix, painted.Pix) {
		t.Fatal("desfazer a borracha não restaurou o traço")
	}
	document.Redo()
	if document.Snapshot().RGBAAt(50, 20) != original.RGBAAt(50, 20) {
		t.Fatal("refazer não aplicou a borracha")
	}
	document.Undo()
	document.Undo()
	if !bytes.Equal(document.Snapshot().Pix, original.Pix) {
		t.Fatal("desfazer os traços não restaurou a captura exata")
	}
	document.BeginStroke(Point{5, 5}, 4, ink, false)
	document.EndStroke()
	if document.CanRedo() {
		t.Fatal("uma marcação nova deve descartar o histórico de refazer")
	}
}

func TestSnapshotAndInputPixelsAreIndependent(t *testing.T) {
	input := testImage()
	document, err := New(input)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := document.Snapshot()
	input.SetRGBA(20, 20, color.RGBA{})
	document.BeginStroke(Point{20, 20}, 8, color.NRGBA{R: 255, A: 255}, false)
	document.EndStroke()
	if snapshot.RGBAAt(20, 20) != (color.RGBA{R: 20, G: 20, B: 180, A: 255}) {
		t.Fatal("desenhar ou modificar a entrada mudou um snapshot destinado ao salvamento")
	}
	document.Undo()
	if document.Snapshot().RGBAAt(20, 20) != snapshot.RGBAAt(20, 20) {
		t.Fatal("o original mudou junto com os pixels da entrada")
	}
}

func TestTextFontsAccentsAndMultilineHaveUndo(t *testing.T) {
	for _, family := range []string{FontSans, FontBold, FontMono} {
		t.Run(family, func(t *testing.T) {
			original := testImage()
			document, err := New(original)
			if err != nil {
				t.Fatal(err)
			}
			if err := document.AddText(Point{5, 3}, "Olá, ação!\nSegunda linha", family, 18, color.NRGBA{A: 255}); err != nil {
				t.Fatal(err)
			}
			withText := document.Snapshot()
			if bytes.Equal(withText.Pix, original.Pix) {
				t.Fatal("o texto não foi desenhado")
			}
			changedBelow := false
			for y := 28; y < 65; y++ {
				for x := 0; x < 160; x++ {
					if withText.RGBAAt(x, y) != original.RGBAAt(x, y) {
						changedBelow = true
					}
				}
			}
			if !changedBelow {
				t.Fatal("a segunda linha não apareceu na imagem")
			}
			document.Undo()
			if !bytes.Equal(document.Snapshot().Pix, original.Pix) {
				t.Fatal("desfazer texto não recuperou o original")
			}
			document.Redo()
			if !bytes.Equal(document.Snapshot().Pix, withText.Pix) {
				t.Fatal("refazer mudou a fonte, a posição ou o texto")
			}
		})
	}
}

func TestInvalidTextAndEmptyCaptureDoNotModifyDocument(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("captura ausente deve produzir erro")
	}
	document, err := New(testImage())
	if err != nil {
		t.Fatal(err)
	}
	before := document.Snapshot()
	if err := document.AddText(Point{}, "texto", "inexistente", 24, color.NRGBA{A: 255}); err == nil {
		t.Fatal("uma fonte desconhecida não foi rejeitada")
	}
	if err := document.AddText(Point{}, " \n ", FontSans, 24, color.NRGBA{A: 255}); err != nil {
		t.Fatal(err)
	}
	if document.CanUndo() || document.Revision() != 0 || !bytes.Equal(document.Snapshot().Pix, before.Pix) {
		t.Fatal("texto inválido/vazio alterou a captura")
	}
}
