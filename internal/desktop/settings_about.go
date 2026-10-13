package desktop

import (
	"fmt"
	"os"
	"runtime"

	"ai-talker/internal/version"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

func applicationIdentification() string {
	executable, err := os.Executable()
	if err != nil {
		executable = "Caminho indisponível"
	}
	return fmt.Sprintf("%s\nRevisão: %s\nPlataforma: %s/%s\nExecutável: %s",
		version.String(), version.Revision, runtime.GOOS, runtime.GOARCH, executable)
}

func (d *desktop) buildAboutSettings() fyne.CanvasObject {
	identity := widget.NewLabel(applicationIdentification())
	identity.Wrapping = fyne.TextWrapWord
	description := widget.NewLabel(
		"Esta identificação pertence ao aplicativo que está aberto. " +
			"Depois de instalar uma atualização, feche a janela anterior e abra novamente pelo launcher.",
	)
	description.Wrapping = fyne.TextWrapWord
	copyIdentity := widget.NewButton(uiText("Copiar identificação"), d.guard(func() {
		text := applicationIdentification()
		d.operation(func() error { return d.deps.copy(text) }, func(err error) {
			if err != nil {
				d.showError("Não foi possível copiar a identificação: "+err.Error(), "")
			}
		})
	}))
	return container.NewVBox(identity, description, copyIdentity)
}
