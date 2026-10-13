package desktop

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ai-talker/internal/platform"
	"ai-talker/internal/playback"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

func (d *desktop) audioFileAction() {
	if d.capture != captureIdle {
		return
	}
	if d.lastAudio == "" {
		d.importAudio()
		return
	}
	menu := widget.NewPopUpMenu(fyne.NewMenu(uiText("Arquivo de áudio"),
		fyne.NewMenuItem(uiText("Importar áudio…"), d.guard(d.importAudio)),
		fyne.NewMenuItem(uiText("Exportar MP3 · 192 kb/s…"), d.guard(d.exportAudio)),
	), d.window.Canvas())
	menu.ShowAtRelativePosition(fyne.NewPos(0, d.importButton.Size().Height), d.importButton)
}

func (d *desktop) exportAudio() {
	doc, source := d.document, d.lastAudio
	if source == "" || d.capture != captureIdle {
		return
	}
	name := strings.TrimSuffix(filepath.Base(source), filepath.Ext(source)) + ".mp3"
	export := func(path string, err error) {
		if err != nil {
			d.showDocumentError(doc, "Não foi possível escolher o destino: "+err.Error(), "")
			return
		}
		if path == "" {
			return
		}
		ctx, cancel := context.WithTimeout(d.ctx, 15*time.Minute)
		doc.view.status = "Exportando MP3 a 192 kb/s…"
		d.renderDocument(doc)
		d.operation(func() error {
			defer cancel()
			return playback.ExportMP3(ctx, source, path, d.directory)
		}, func(err error) {
			if doc.closed {
				return
			}
			if err != nil {
				d.showDocumentError(doc, "Não foi possível exportar o áudio: "+err.Error(), "")
			} else {
				doc.view.status = "MP3 exportado."
				d.renderDocument(doc)
			}
		})
	}
	if d.deps.chooseFile != nil {
		d.selectFile(platform.FileDialogOptions{
			Title: "Exportar áudio", Save: true, FileName: name, DefaultExtension: "mp3",
			Filters: []platform.FileFilter{{Name: "MP3 a 192 kb/s", Pattern: "*.mp3"}},
		}, export)
		return
	}
	selection := dialog.NewEntryDialog(uiText("Exportar MP3"), uiText("Caminho completo do arquivo MP3"), func(path string) {
		d.guard(func() {
			if _, err := os.Stat(path); err == nil {
				dialog.ShowConfirm(uiText("Substituir MP3"), uiText("Substituir o arquivo escolhido após concluir a exportação?"), func(confirmed bool) {
					if confirmed {
						d.guard(func() { export(path, nil) })()
					}
				}, d.window)
			} else {
				export(path, nil)
			}
		})()
	}, d.window)
	selection.SetText(filepath.Join(recordingFolder(d.directory), name))
	selection.Show()
	selection.Resize(dialogSize(d.window))
}
