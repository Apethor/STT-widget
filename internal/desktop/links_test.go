package desktop

import (
	"context"
	"errors"
	"strings"
	"testing"

	"ai-talker/internal/config"
	"ai-talker/internal/processing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func collectLinks(segments []widget.RichTextSegment) []*widget.HyperlinkSegment {
	var result []*widget.HyperlinkSegment
	for _, segment := range segments {
		switch value := segment.(type) {
		case *widget.HyperlinkSegment:
			result = append(result, value)
		case *widget.ParagraphSegment:
			result = append(result, collectLinks(value.Texts)...)
		case *widget.ListSegment:
			result = append(result, collectLinks(value.Items)...)
		case *widget.TableSegment:
			for _, cell := range value.Headers {
				result = append(result, collectLinks(cell)...)
			}
			for _, row := range value.Rows {
				for _, cell := range row {
					result = append(result, collectLinks(cell)...)
				}
			}
		}
	}
	return result
}

func TestResultLinksCompactNamesKeepExactDestinations(t *testing.T) {
	for _, tc := range []struct {
		name, text, label, address string
	}{
		{"markdown", "Veja [Documentação oficial](https://example.com/a/very/long/path?q=go&lang=pt#install).",
			"Documentação oficial", "https://example.com/a/very/long/path?q=go&lang=pt#install"},
		{"plain URL", "Página: https://www.example.com/a/very/long/path?q=go&lang=pt#install.",
			"example.com", "https://www.example.com/a/very/long/path?q=go&lang=pt#install"},
		{"parentheses", "Leia (https://example.com/long/path).", "example.com", "https://example.com/long/path"},
		{"brackets", "Leia [https://example.com/long/path].", "example.com", "https://example.com/long/path"},
		{"angle brackets", "Leia <https://example.com/long/path>.", "example.com", "https://example.com/long/path"},
		{"balanced parentheses", "https://example.com/wiki/Example_(topic).", "example.com", "https://example.com/wiki/Example_(topic)"},
		{"list", "- [Fonte confiável](https://example.com/research)\n- Outra informação",
			"Fonte confiável", "https://example.com/research"},
		{"image becomes link", "![Imagem de exemplo](https://example.com/image.png)", "example.com", "https://example.com/image.png"},
		{"table", "| Fonte |\n| --- |\n| [Documentação](https://example.com/docs) |", "Documentação", "https://example.com/docs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var opened string
			segments, count := linkedSegments(tc.text, func(address string) { opened = address })
			links := collectLinks(segments)
			if count != 1 || len(links) != 1 || links[0].Text != tc.label || links[0].URL.String() != tc.address {
				t.Fatalf("link convertido incorretamente: count=%d, links=%+v; segmentos=%+v", count, links, segments)
			}
			if opened != "" {
				t.Fatal("mostrar o resultado abriu o navegador sem clique")
			}
			links[0].OnTapped()
			if opened != tc.address {
				t.Fatalf("o clique não abriu o endereço exato: %q", opened)
			}
		})
	}
}

func TestResultLinksDoNotActivateCodeOrNonWebProtocols(t *testing.T) {
	text := "`https://example.com/code` [arquivo](file:///C:/Windows/cmd.exe) [script](javascript:alert)\n" +
		"```\nhttps://example.com/fenced\n```\n" +
		"[configuração](ms-settings:privacy)"
	_, count := linkedSegments(text, func(string) { t.Fatal("um link inativo foi aberto") })
	if count != 0 {
		t.Fatalf("código ou protocolos locais viraram hyperlinks: %d", count)
	}
}

func TestProcessedLinksShowReadingAndKeepEditableCopy(t *testing.T) {
	output := "Leia [Documentação](https://example.com/long/path?q=go).\n\nDetalhes adicionais."
	opened := make(chan string, 2)
	copied := make(chan string, 1)
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.process = func(context.Context, config.Settings, processing.Request) (processing.Result, error) {
			return processing.Result{Text: output}, nil
		}
		deps.openURL = func(address string) error { opened <- address; return nil }
		deps.copy = func(text string) error { copied <- text; return nil }
	})
	doc := h.d.document
	doc.editor.SetText("Pesquise a documentação.")
	test.Tap(doc.processButton)
	h.await(func() bool { return !doc.processBusy })
	if !doc.processReading || !doc.processPreview.Visible() || doc.processEditor.Visible() ||
		doc.processEditor.Text != output || doc.processViewButton.Disabled() {
		t.Fatal("o resultado não abriu com links ou alterou o texto original")
	}
	links := collectLinks(doc.processPreview.Segments)
	if len(links) != 1 {
		t.Fatal("a documentação não apareceu como link")
	}
	links[0].OnTapped()
	if waitValue(t, opened) != "https://example.com/long/path?q=go" {
		t.Fatal("o clique abriu outro endereço")
	}
	h.settle()
	test.Tap(doc.processCopyButton)
	if waitValue(t, copied) != output {
		t.Fatal("copiar perdeu a URL ou a formatação original")
	}
	h.settle()
	test.Tap(doc.processViewButton)
	if doc.processReading || !doc.processEditor.Visible() {
		t.Fatal("o usuário não conseguiu voltar ao editor")
	}
	doc.processEditor.SetText(strings.ReplaceAll(output, "Detalhes adicionais.", "Texto editado."))
	if doc.processReading || !strings.Contains(doc.processEditor.Text, "Texto editado.") {
		t.Fatal("editar o resultado não preservou a alteração")
	}
	test.Tap(doc.processViewButton)
	if !doc.processReading || !doc.processPreview.Visible() {
		t.Fatal("o texto editado não pode ser relido com links")
	}
}

func TestResultBrowserFailureAndTabIsolation(t *testing.T) {
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.openURL = func(string) error { return errors.New("navegador indisponível") }
	})
	first := h.d.document
	first.processEditor.SetText("https://example.com/first")
	h.d.updateResultLinks(first, true)
	second := h.d.createDocument()
	second.processEditor.SetText("Texto sem link.")
	links := collectLinks(first.processPreview.Segments)
	links[0].OnTapped()
	h.settle()
	if !first.processReading || first.processEditor.Text != "https://example.com/first" ||
		second.processReading || second.processEditor.Text != "Texto sem link." || second.processViewButton.Disabled() == false {
		t.Fatal("abrir um link alterou outra aba ou bloqueou o resultado")
	}
}
