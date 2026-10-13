package desktop

import (
	"strings"
	"testing"

	"ai-talker/internal/version"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func TestAboutShowsAndCopiesTheRunningApplicationIdentification(t *testing.T) {
	copied := make(chan string, 1)
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.copy = func(text string) error {
			copied <- text
			return nil
		}
	})
	h.d.openSettingsSection("Sobre")
	form := h.d.settingsForm
	h.await(func() bool { return !form.loading })
	if form.sections.selectedID != "about" {
		t.Fatal("Sobre não abriu sua própria página")
	}
	var copyButton *widget.Button
	var identification string
	for _, object := range test.LaidOutObjects(form.sections.pages["about"]) {
		switch control := object.(type) {
		case *widget.Label:
			if strings.Contains(control.Text, "Executável:") {
				identification = control.Text
			}
		case *widget.Button:
			if control.Text == "Copiar identificação" {
				copyButton = control
			}
		}
	}
	if copyButton == nil || !strings.Contains(identification, version.String()) ||
		!strings.Contains(identification, "Revisão: "+version.Revision) {
		t.Fatal("a identificação não corresponde ao processo aberto", identification)
	}
	test.Tap(copyButton)
	if text := waitValue(t, copied); text != identification {
		t.Fatal("copiar identificação não usou os dados exibidos")
	}
}
