package desktop

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"
)

func visibleEditorCaret(object fyne.CanvasObject, depth int) *canvas.Rectangle {
	if depth > 20 {
		return nil
	}
	if rectangle, ok := object.(*canvas.Rectangle); ok {
		size := rectangle.Size()
		if rectangle.Visible() && size.Width > 0 && size.Width <= 2 && size.Height > 10 {
			return rectangle
		}
	}
	var children []fyne.CanvasObject
	if box, ok := object.(*fyne.Container); ok {
		children = box.Objects
	} else if widget, ok := object.(fyne.Widget); ok {
		children = test.WidgetRenderer(widget).Objects()
	}
	for _, child := range children {
		if caret := visibleEditorCaret(child, depth+1); caret != nil {
			return caret
		}
	}
	return nil
}

func TestTextEditorsShowCaretOnlyWhileFocused(t *testing.T) {
	h := newHarness(t, nil, nil)
	for _, editor := range []fyne.Focusable{h.d.editor, h.d.processEditor} {
		object := editor.(fyne.CanvasObject)
		h.d.window.Canvas().Focus(editor)
		object.Refresh()
		if visibleEditorCaret(object, 0) == nil {
			t.Fatal("the focused editor has no visible insertion bar")
		}
		h.d.window.Canvas().Unfocus()
		object.Refresh()
		if visibleEditorCaret(object, 0) != nil {
			t.Fatal("the insertion bar remained visible without editor focus")
		}
	}
}
