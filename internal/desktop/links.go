package desktop

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"ai-talker/internal/platform"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

var webAddressPattern = regexp.MustCompile("(?i)https?://[^\\s<>\"'`]+")

func shortLinkName(target *url.URL) string {
	return strings.TrimPrefix(target.Hostname(), "www.")
}

func trimLinkPunctuation(address string) string {
	address = strings.TrimRight(address, ".,;:!?")
	for _, pair := range []struct{ open, close string }{{"(", ")"}, {"[", "]"}, {"{", "}"}} {
		for strings.HasSuffix(address, pair.close) && strings.Count(address, pair.close) > strings.Count(address, pair.open) {
			address = strings.TrimSuffix(address, pair.close)
		}
	}
	return address
}

// linkedSegments retains Markdown labels and formatting, and also recognizes
// plain URLs inside text. Remote images become links and are never fetched
// simply by displaying a model response.
func linkedSegments(text string, open func(string)) ([]widget.RichTextSegment, int) {
	return linkedSegmentsWithFiles(text, nil, open)
}

func linkedSegmentsWithFiles(text string, files map[string]string, open func(string)) ([]widget.RichTextSegment, int) {
	// File references stay as full paths in the actual entry. Only their
	// presentation becomes a short Markdown link, with one item per line.
	if len(files) > 0 {
		lines := strings.Split(text, "\n")
		fileNumber := 0
		for i, line := range lines {
			for address, path := range files {
				if strings.TrimSpace(line) == path {
					fileNumber++
					lines[i] = "- [Screenshot " + strconv.Itoa(fileNumber) + "](" + address + ")"
					break
				}
			}
		}
		text = strings.Join(lines, "\n")
	}
	parsed := widget.NewRichTextFromMarkdown(text)
	count := 0
	var convert func([]widget.RichTextSegment) []widget.RichTextSegment
	makeLink := func(label, address string) widget.RichTextSegment {
		if _, known := files[address]; known {
			target, err := url.Parse(address)
			if err == nil {
				count++
				return &widget.HyperlinkSegment{Text: label, URL: target, OnTapped: func() { open(address) }}
			}
		}
		target, err := platform.WebURL(address)
		if err != nil {
			return &widget.TextSegment{Text: label, Style: widget.RichTextStyleInline}
		}
		if strings.TrimSpace(label) == "" || webAddressPattern.MatchString(label) {
			label = shortLinkName(target)
		}
		count++
		return &widget.HyperlinkSegment{
			Text: label, URL: target, OnTapped: func() { open(target.String()) },
		}
	}
	convert = func(segments []widget.RichTextSegment) []widget.RichTextSegment {
		// Markdown may split plain text at punctuation such as an underscore
		// inside a URL. Join adjacent runs before looking for web addresses.
		joined := make([]widget.RichTextSegment, 0, len(segments))
		for _, segment := range segments {
			current, isText := segment.(*widget.TextSegment)
			if isText && current.Style.Inline && len(joined) > 0 {
				if previous, ok := joined[len(joined)-1].(*widget.TextSegment); ok && previous.Style == current.Style {
					previous.Text += current.Text
					continue
				}
			}
			joined = append(joined, segment)
		}
		result := make([]widget.RichTextSegment, 0, len(segments))
		for _, segment := range joined {
			switch value := segment.(type) {
			case *widget.HyperlinkSegment:
				if value.URL == nil {
					result = append(result, &widget.TextSegment{Text: value.Text, Style: widget.RichTextStyleInline})
				} else {
					result = append(result, makeLink(value.Text, value.URL.String()))
				}
			case *widget.ImageSegment:
				if value.Source != nil {
					result = append(result, makeLink(value.Title, value.Source.String()))
				}
			case *widget.ParagraphSegment:
				value.Texts = convert(value.Texts)
				result = append(result, value)
			case *widget.ListSegment:
				value.Items = convert(value.Items)
				result = append(result, value)
			case *widget.TableSegment:
				for i := range value.Headers {
					value.Headers[i] = convert(value.Headers[i])
				}
				for i := range value.Rows {
					for j := range value.Rows[i] {
						value.Rows[i][j] = convert(value.Rows[i][j])
					}
				}
				result = append(result, value)
			case *widget.TextSegment:
				if value.Text == "" || value.Style.TextStyle.Monospace {
					result = append(result, value)
					continue
				}
				last := 0
				for _, match := range webAddressPattern.FindAllStringIndex(value.Text, -1) {
					address := trimLinkPunctuation(value.Text[match[0]:match[1]])
					if _, err := platform.WebURL(address); err != nil {
						continue
					}
					if match[0] > last {
						result = append(result, &widget.TextSegment{Text: value.Text[last:match[0]], Style: value.Style})
					}
					result = append(result, makeLink("", address))
					last = match[0] + len(address)
				}
				if last < len(value.Text) {
					result = append(result, &widget.TextSegment{Text: value.Text[last:], Style: value.Style})
				}
			default:
				result = append(result, segment)
			}
		}
		return result
	}
	segments := convert(parsed.Segments)
	return segments, count
}

func (d *desktop) openWebLink(doc *document, address string) {
	if doc.closed || d.deps.openURL == nil {
		return
	}
	d.operation(func() error { return d.deps.openURL(address) }, func(err error) {
		if err != nil && !doc.closed {
			dialog.ShowError(err, d.window)
		}
	})
}

// The entry stays the source of truth for persistence, drafts, copy and save.
// Only presentation changes when the user reads links or returns to editing.
func (d *desktop) updateResultLinks(doc *document, show bool) {
	if doc.processPreview == nil {
		return
	}
	files := d.screenshotFileLinks(doc)
	segments, count := linkedSegmentsWithFiles(doc.processEditor.Text, files, func(address string) {
		if path, known := files[address]; known {
			d.openImageLink(doc, path)
		} else {
			d.openWebLink(doc, address)
		}
	})
	doc.processPreview.Segments = segments
	doc.processPreview.Refresh()
	doc.processLinkCount = count
	if count == 0 {
		show = false
	}
	doc.processReading = show
	if show {
		doc.processEditor.Hide()
		doc.processPreview.Show()
		doc.processReadScroll.Show()
		doc.processViewButton.SetIcon(theme.DocumentCreateIcon())
		doc.processViewButton.setHint(uiText("Editar resultado · mostrar texto e endereços completos"))
	} else {
		doc.processPreview.Hide()
		doc.processReadScroll.Hide()
		doc.processEditor.Show()
		doc.processViewButton.SetIcon(theme.VisibilityIcon())
		doc.processViewButton.setHint(uiText("Ler resultado com links clicáveis"))
	}
	setEnabled(doc.processViewButton, count > 0 || show)
	doc.processBody.Refresh()
}

func (d *desktop) updateTranscriptLinks(doc *document, show bool) {
	if doc.transcriptPreview == nil {
		return
	}
	files := d.screenshotFileLinks(doc)
	segments, count := linkedSegmentsWithFiles(doc.editor.Text, files, func(address string) {
		if path, known := files[address]; known {
			d.openImageLink(doc, path)
		} else {
			d.openWebLink(doc, address)
		}
	})
	doc.transcriptPreview.Segments = segments
	doc.transcriptPreview.Refresh()
	doc.transcriptReading = show && count > 0
	if doc.transcriptReading {
		doc.editor.Hide()
		doc.transcriptPreview.Show()
		doc.transcriptReadScroll.Show()
		doc.transcriptViewButton.SetIcon(theme.DocumentCreateIcon())
		doc.transcriptViewButton.setHint(uiText("Editar transcrição · mostrar texto e caminhos completos"))
	} else {
		doc.transcriptPreview.Hide()
		doc.transcriptReadScroll.Hide()
		doc.editor.Show()
		doc.transcriptViewButton.SetIcon(theme.VisibilityIcon())
		doc.transcriptViewButton.setHint(uiText("Ler transcrição com links clicáveis"))
	}
	setEnabled(doc.transcriptViewButton, count > 0)
	doc.transcriptBody.Refresh()
}

func (d *desktop) transcriptPresentation(doc *document) fyne.CanvasObject {
	doc.transcriptPreview = widget.NewRichText()
	doc.transcriptPreview.Wrapping = fyne.TextWrapWord
	// Fyne 2.8.1's internal RichText scroll retains the initial rendered
	// objects when Segments changes, including their hyperlink callbacks.
	// An external scroll keeps renderer updates and hit targets consistent.
	doc.transcriptPreview.Scroll = container.ScrollNone
	doc.transcriptPreview.Hide()
	doc.transcriptReadScroll = container.NewVScroll(doc.transcriptPreview)
	doc.transcriptReadScroll.Hide()
	doc.transcriptBody = container.NewStack(doc.editor, doc.transcriptReadScroll)
	return container.NewThemeOverride(doc.transcriptBody, editorTheme{
		Theme: d.palette, desktop: d, document: doc, transcript: true,
	})
}

func (d *desktop) resultPresentation(doc *document) fyne.CanvasObject {
	doc.processPreview = widget.NewRichText()
	doc.processPreview.Wrapping = fyne.TextWrapWord
	doc.processPreview.Scroll = container.ScrollNone
	doc.processPreview.Hide()
	doc.processReadScroll = container.NewVScroll(doc.processPreview)
	doc.processReadScroll.Hide()
	doc.processBody = container.NewStack(doc.processEditor, doc.processReadScroll)
	return container.NewThemeOverride(doc.processBody, editorTheme{Theme: d.palette, desktop: d, document: doc})
}
