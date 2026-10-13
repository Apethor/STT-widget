package desktop

import (
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func renderedHyperlinks(object fyne.CanvasObject, depth int) []*widget.Hyperlink {
	if depth > 20 {
		return nil
	}
	if link, ok := object.(*widget.Hyperlink); ok {
		return []*widget.Hyperlink{link}
	}
	var children []fyne.CanvasObject
	if box, ok := object.(*fyne.Container); ok {
		children = box.Objects
	} else if item, ok := object.(fyne.Widget); ok {
		children = test.WidgetRenderer(item).Objects()
	}
	var links []*widget.Hyperlink
	for _, child := range children {
		links = append(links, renderedHyperlinks(child, depth+1)...)
	}
	return links
}

func TestReadingViewRenderedClicksTrackUpdatedScreenshotAndWebTargets(t *testing.T) {
	opened := make(chan string, 4)
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.openImage = func(path string) error { opened <- path; return nil }
		deps.openURL = func(address string) error { opened <- address; return nil }
	})
	doc := h.d.document
	for _, filename := range []string{"first.png", "second.png"} {
		path := filepath.Join(h.d.directory, "screenshots", filename)
		doc.screenshotFiles = []string{path}
		doc.editor.SetText("Imagem atual\n" + path)
		h.d.updateTranscriptLinks(doc, true)
		links := renderedHyperlinks(doc.transcriptPreview, 0)
		if len(links) != 1 {
			t.Fatalf("expected one rendered image link, got %d", len(links))
		}
		size := links[0].MinSize()
		test.TapAt(links[0], fyne.NewPos(size.Width/2, size.Height/2))
		if waitValue(t, opened) != path {
			t.Fatal("rendered image link retained an earlier target")
		}
		h.settle()
	}
	for _, address := range []string{"https://example.com/first", "https://example.com/second"} {
		doc.processEditor.SetText(address)
		h.d.updateResultLinks(doc, true)
		links := renderedHyperlinks(doc.processPreview, 0)
		if len(links) != 1 {
			t.Fatalf("expected one rendered web link, got %d", len(links))
		}
		size := links[0].MinSize()
		test.TapAt(links[0], fyne.NewPos(size.Width/2, size.Height/2))
		if waitValue(t, opened) != address {
			t.Fatal("rendered web link retained an earlier response's target")
		}
		h.settle()
	}
}

func TestTextNotificationsPreserveReadingViewsAndEmptyTextReturnsToEditing(t *testing.T) {
	h := newHarness(t, nil, nil)
	doc := h.d.document
	for _, processed := range []bool{false, true} {
		editor := doc.editor
		refresh := func() { h.d.updateTranscriptLinks(doc, true) }
		reading := func() bool { return doc.transcriptReading }
		if processed {
			editor = doc.processEditor
			refresh = func() { h.d.updateResultLinks(doc, true) }
			reading = func() bool { return doc.processReading }
		}
		editor.SetText("Leia [Documentação](https://example.com/docs).")
		refresh()
		// Native layout can deliver a text notification after the reading
		// view opens. The notification must respect the current presentation.
		editor.OnChanged(editor.Text)
		if !reading() || editor.Visible() {
			t.Fatal("a text notification hid the clickable reading view")
		}
		editor.SetText("")
		if reading() || !editor.Visible() {
			t.Fatal("removing the last link left an empty reading view")
		}
	}
}
