package desktop

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"ai-talker/internal/platform"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"
)

func (d *desktop) importAudio() {
	if d.capture != captureIdle {
		return
	}
	doc := d.document
	if d.deps.chooseFile != nil {
		generation := doc.generation
		d.selectFile(platform.FileDialogOptions{
			Title: "Importar áudio", Folder: recordingFolder(d.directory), NewestFirst: true,
			Filters: []platform.FileFilter{
				{Name: "Áudio e vídeo", Pattern: "*.m4a;*.mp3;*.wav;*.ogg;*.opus;*.flac;*.aac;*.webm;*.mp4;*.aiff;*.wma"},
				{Name: "Todos os arquivos", Pattern: "*.*"},
			},
		}, func(path string, err error) {
			if doc.closed || generation != doc.generation {
				return
			}
			if err != nil {
				d.showDocumentError(doc, fmt.Sprintf("Não foi possível abrir o áudio: %v", err), "")
			} else if path != "" {
				d.startImportedJob(doc, path)
			}
		})
		return
	}
	selection := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
		d.guard(func() {
			if err != nil {
				d.showDocumentError(doc, fmt.Sprintf("Não foi possível abrir o áudio: %v", err), "")
				return
			}
			if reader == nil {
				return
			}
			path, pathErr := localPath(reader.URI())
			generation := doc.generation
			d.operation(reader.Close, func(closeErr error) {
				if doc.closed || generation != doc.generation {
					return
				}
				if err := errors.Join(pathErr, closeErr); err != nil {
					d.showDocumentError(doc, err.Error(), "")
					return
				}
				d.startImportedJob(doc, path)
			})
		})()
	}, d.window)
	selection.SetFilter(storage.NewExtensionFileFilter([]string{
		".m4a", ".mp3", ".wav", ".ogg", ".opus", ".flac",
		".aac", ".webm", ".mp4", ".aiff", ".wma",
	}))
	selection.SetConfirmText(uiText("Abrir áudio"))
	selection.SetDismissText(uiText("Cancelar"))
	selection.Show()
	selection.Resize(dialogSize(d.window))
}

func (d *desktop) copyText() {
	d.copyDocumentText(d.document, false)
}

func (d *desktop) copyDocumentText(doc *document, processed bool) {
	d.syncDocumentActions(doc)
	text := doc.editor.Text
	if processed {
		text = doc.processEditor.Text
	}
	if strings.TrimSpace(text) == "" {
		doc.view.status = "Ainda não há texto para copiar."
		d.renderDocument(doc)
		return
	}
	doc.copyEpoch++
	epoch := doc.copyEpoch
	generation := doc.generation
	d.operation(func() error {
		for attempt := range 3 {
			if err := d.ctx.Err(); err != nil {
				return err
			}
			err := d.deps.copy(text)
			if err == nil || !errors.Is(err, platform.ErrClipboardBusy) || attempt == 2 {
				return err
			}
			if err := waitContext(d.ctx, 100*time.Millisecond); err != nil {
				return err
			}
		}
		return platform.ErrClipboardBusy
	}, func(err error) {
		if doc.closed || epoch != doc.copyEpoch || generation != doc.generation {
			return
		}
		switch {
		case errors.Is(err, platform.ErrUnsupported):
			// Fyne's clipboard is an optional fallback for non-Windows test
			// or desktop drivers. Access it only on the UI thread.
			d.application.Clipboard().SetContent(text)
			doc.view.status = "Texto copiado."
		case errors.Is(err, platform.ErrClipboardBusy):
			doc.view.status = "A área de transferência está ocupada. Tente copiar novamente."
		case err != nil:
			if processed {
				d.showProcessingError(doc, fmt.Sprintf("Não foi possível copiar o resultado: %v", err))
			} else {
				d.showDocumentError(doc, fmt.Sprintf("Não foi possível copiar o texto: %v", err), "")
			}
		default:
			doc.view.status = "Texto copiado."
		}
		d.renderDocument(doc)
	})
}

func (d *desktop) saveText() {
	d.saveDocumentText(d.document, false)
}

func (d *desktop) saveDocumentText(doc *document, processed bool) {
	d.syncDocumentActions(doc)
	text := doc.editor.Text
	if processed {
		text = doc.processEditor.Text
	}
	if strings.TrimSpace(text) == "" {
		return
	}
	name := "Transcricao"
	if doc.lastAudio != "" {
		base := filepath.Base(doc.lastAudio)
		name = strings.TrimSuffix(base, filepath.Ext(base))
	}
	if processed {
		name += "-processado"
	}
	if d.deps.chooseFile != nil {
		generation := doc.generation
		d.selectFile(platform.FileDialogOptions{
			Title: "Salvar texto", Save: true, FileName: name + ".txt", DefaultExtension: "txt",
			Filters: []platform.FileFilter{
				{Name: "Texto", Pattern: "*.txt"}, {Name: "Markdown", Pattern: "*.md"},
			},
		}, func(path string, err error) {
			if doc.closed || generation != doc.generation {
				return
			}
			if err != nil {
				d.showDocumentError(doc, fmt.Sprintf("Não foi possível salvar o texto: %v", err), "")
				return
			}
			if path != "" {
				d.saveDocumentPath(doc, processed, path)
			}
		})
		return
	}
	selection := dialog.NewFileSave(func(writer fyne.URIWriteCloser, err error) {
		d.guard(func() {
			if err != nil {
				d.showDocumentError(doc, fmt.Sprintf("Não foi possível salvar o texto: %v", err), "")
				return
			}
			if writer == nil {
				return
			}
			d.saveDocumentSelection(doc, processed, writer)
		})()
	}, d.window)
	selection.SetFileName(name + ".txt")
	selection.SetFilter(storage.NewExtensionFileFilter([]string{".txt", ".md"}))
	selection.SetConfirmText(uiText("Salvar"))
	selection.SetDismissText(uiText("Cancelar"))
	selection.Show()
	selection.Resize(dialogSize(d.window))
}

func (d *desktop) selectFile(options platform.FileDialogOptions, complete func(string, error)) {
	options.Title = uiText(options.Title)
	for index := range options.Filters {
		options.Filters[index].Name = uiText(options.Filters[index].Name)
	}
	var path string
	d.operation(func() error {
		var err error
		path, err = d.deps.chooseFile(d.ctx, options)
		return err
	}, func(err error) {
		if !errors.Is(err, context.Canceled) {
			complete(path, err)
		}
	})
}

func (d *desktop) saveDocumentPath(doc *document, processed bool, path string) {
	text := doc.editor.Text
	if processed {
		text = doc.processEditor.Text
	}
	generation := doc.generation
	d.operation(func() (err error) {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, file.Close()) }()
		_, err = io.WriteString(file, text+"\n")
		return err
	}, func(err error) {
		if doc.closed || generation != doc.generation {
			return
		}
		if err != nil {
			d.showDocumentError(doc, fmt.Sprintf("Não foi possível salvar o texto: %v", err), "")
			return
		}
		doc.view.status = "Texto salvo."
		d.renderDocument(doc)
	})
}

func (d *desktop) saveSelection(writer fyne.URIWriteCloser) {
	d.saveDocumentSelection(d.document, false, writer)
}

func (d *desktop) saveDocumentSelection(doc *document, processed bool, writer fyne.URIWriteCloser) {
	// Capture the current editor at confirmation, including changes made while
	// transcription was pending. File I/O and Close both belong to the worker.
	text := doc.editor.Text
	if processed {
		text = doc.processEditor.Text
	}
	generation := doc.generation
	d.operation(func() (err error) {
		defer func() { err = errors.Join(err, writer.Close()) }()
		// The file dialog already opened the selected destination. Finish this
		// short write even during exit; Run joins the worker before returning.
		_, err = io.WriteString(writer, text+"\n")
		return err
	}, func(err error) {
		if doc.closed || generation != doc.generation {
			return
		}
		if err != nil {
			if processed {
				d.showProcessingError(doc, fmt.Sprintf("Não foi possível salvar o resultado: %v", err))
			} else {
				d.showDocumentError(doc, fmt.Sprintf("Não foi possível salvar o texto: %v", err), "")
			}
			return
		}
		doc.view.status = "Texto salvo."
		d.renderDocument(doc)
	})
}

func (d *desktop) openFolder() {
	d.openDataFolder(recordingFolder(d.directory))
}

func (d *desktop) chooseFolder() {
	menu := widget.NewPopUpMenu(fyne.NewMenu(uiText("Abrir pasta"),
		fyne.NewMenuItem(uiText("Gravações"), d.guard(d.openFolder)),
		fyne.NewMenuItem(uiText("Screenshots"), d.guard(func() {
			d.openDataFolder(filepath.Join(d.directory, "screenshots"))
		})),
		fyne.NewMenuItem(uiText("Capturas de vídeo"), d.guard(func() {
			d.openDataFolder(filepath.Join(d.directory, "videos"))
		})),
	), d.window.Canvas())
	menu.ShowAtRelativePosition(fyne.NewPos(0, d.folderButton.Size().Height), d.folderButton)
}

func (d *desktop) openDataFolder(folder string) {
	doc := d.document
	generation := doc.generation
	d.operation(func() error {
		if err := os.MkdirAll(folder, 0o700); err != nil {
			return fmt.Errorf("não foi possível criar a pasta: %w", err)
		}
		return d.deps.openFolder(folder)
	}, func(err error) {
		if err != nil && !doc.closed && generation == doc.generation {
			d.showDocumentError(doc, fmt.Sprintf("Não foi possível abrir a pasta: %v", err), "")
		}
	})
}

func (d *desktop) minimizeWindow() {
	if d.deps.minimize == nil {
		return
	}
	d.operation(d.deps.minimize, func(err error) {
		if err != nil {
			d.showError(fmt.Sprintf("Não foi possível minimizar a janela: %v", err), "")
		}
	})
}

func (d *desktop) requestClose() {
	if d.discardDialog || d.tabsDialog {
		return
	}
	if err := d.saveAllDrafts(); err != nil {
		d.showError(err.Error(), "")
		return
	}
	if err := d.saveWorkspace(); err != nil {
		d.showError("Não foi possível salvar as abas: "+err.Error(), "")
		return
	}
	if d.capture != captureIdle {
		if d.closeDialog {
			return
		}
		d.closeDialog = true
		d.deps.confirmClose(d.window, func(confirmed bool) {
			d.guard(func() {
				d.closeDialog = false
				if !confirmed {
					return
				}
				if d.capture != captureIdle {
					d.stopCapture(closeCapture)
					return
				}
				d.requestClose()
			})()
		})
		return
	}
	d.stopPlayback()
	d.renderDocument(d.document)
	d.syncControls()
	if d.hasJobs() {
		d.closeAfterJob = true
		d.hidden = true
		d.window.Hide()
		return
	}
	d.finish()
}

func (d *desktop) reveal() {
	d.closeAfterJob = false
	d.hidden = false
	d.window.Show()
	d.window.RequestFocus()
}

func (d *desktop) activateWindow() {
	d.reveal()
	d.operation(d.deps.activate, func(err error) {
		if err != nil && !errors.Is(err, platform.ErrUnsupported) {
			if errors.Is(err, platform.ErrForegroundDenied) {
				d.status.SetText("A janela foi reaberta. Selecione-a na barra de tarefas.")
				return
			}
			d.showError(fmt.Sprintf("Não foi possível ativar a janela: %v", err), "")
		}
	})
}

func (d *desktop) finish() {
	if d.closing {
		return
	}
	if d.finishVoiceBeforeQuit() {
		return
	}
	d.preserveVoiceSnapshot()
	if err := d.saveAllDrafts(); err != nil && d.ctx.Err() == nil {
		d.reveal()
		d.showError(err.Error(), "")
		return
	}
	if err := d.saveWorkspace(); err != nil && d.ctx.Err() == nil {
		d.reveal()
		d.showError("Não foi possível salvar as abas: "+err.Error(), "")
		return
	}
	d.closing = true
	d.stopped.Store(true)
	d.captureEpoch++
	d.cancelVoiceSession()
	d.stopResponsePlayback()
	d.stopSoundboardPlayback()
	if d.soundboardFrameCancel != nil {
		d.soundboardFrameCancel()
		d.soundboardFrameCancel = nil
	}
	if d.soundEngine != nil {
		engine := d.soundEngine
		d.soundEngine = nil
		d.workers.Go(engine.Close)
	}
	d.cancelMerge("Mesclagem cancelada.")
	for _, doc := range d.documents {
		if doc.live != nil {
			d.cancelLiveRecording(doc.live)
		}
		d.invalidateDocumentJob(doc)
		d.invalidateProcessing(doc)
	}
	if d.authCancel != nil {
		d.authCancel()
		d.authCancel = nil
	}
	d.cancel()
	if d.videoSession != nil {
		session := d.videoSession
		d.videoSession = nil
		d.workers.Go(func() {
			if err := session.Abort(); err != nil {
				slog.Error("falha ao descartar a captura de vídeo", "error", err)
			}
		})
	}
	if d.recorder != nil {
		r := d.recorder
		d.recorder = nil
		d.workers.Go(func() {
			if err := callOperation(func() error {
				_, err := r.Stop()
				return err
			}); err != nil && !errors.Is(err, context.Canceled) {
				slog.Error("falha ao encerrar a gravação", "error", err)
			}
		})
	}
	d.window.SetCloseIntercept(nil)
	d.application.Quit()
}

func localPath(uri fyne.URI) (string, error) {
	if uri.Scheme() != "file" {
		return "", errors.New("escolha um arquivo de áudio no seu computador")
	}
	path := uri.Path()
	if runtime.GOOS == "windows" && len(path) >= 3 && path[0] == '/' && path[2] == ':' {
		path = path[1:]
	}
	return filepath.FromSlash(path), nil
}

func dialogSize(window fyne.Window) fyne.Size {
	size := window.Canvas().Size()
	return fyne.NewSize(max(320, size.Width-40), max(360, size.Height-60))
}

func waitContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func recordingFolder(directory string) string { return filepath.Join(directory, "gravacoes") }
