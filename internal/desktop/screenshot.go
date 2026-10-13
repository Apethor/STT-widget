package desktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"ai-talker/internal/platform"
	"ai-talker/internal/screenshot"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	fynedesktop "fyne.io/fyne/v2/driver/desktop"
)

func (d *desktop) cancelScreenshot() {
	if d.screenshotCancel != nil {
		d.screenshotCancel()
	}
}

func (d *desktop) captureScreenshot() {
	if d.screenshotBusy || d.deps.selectScreenshot == nil {
		return
	}
	d.hints.hide()
	d.screenshotBusy = true
	d.screenshotButton.Disable()
	ctx, cancel := context.WithCancel(d.ctx)
	d.screenshotCancel = cancel
	escape := &fynedesktop.CustomShortcut{KeyName: fyne.KeyEscape}
	d.window.Canvas().AddShortcut(escape, func(fyne.Shortcut) { d.guard(d.cancelScreenshot)() })
	var association *screenshotAssociation
	if d.capture == captureRunning && d.captureDocument != nil && d.captureOrigin != nil && d.captureOrigin.recordingSource != "" {
		association = &screenshotAssociation{
			document: d.captureDocument, source: d.captureOrigin.recordingSource, capture: d.captureOrigin,
		}
		session := d.captureOrigin.live
		if session == nil {
			session = d.startCaptureTimeline(d.captureDocument, d.captureOrigin, d.recorder)
		}
		association.live = session
		association.marker = d.reserveLiveScreenshot(session, d.recorder.Snapshot().Duration)
	}
	d.workers.Go(func() {
		var path string
		var document *screenshot.Document
		err := callOperation(func() error {
			pixels, err := d.deps.selectScreenshot(ctx)
			if err != nil {
				return err
			}
			document, err = screenshot.New(pixels)
			if err != nil {
				return err
			}
			path, err = screenshot.SaveNew(ctx, filepath.Join(d.directory, "screenshots"), document.Image(), d.deps.now())
			return err
		})
		d.post(func() {
			defer cancel()
			d.window.Canvas().RemoveShortcut(escape)
			d.screenshotCancel = nil
			d.screenshotBusy = false
			d.screenshotButton.Enable()
			if err == nil {
				err = ctx.Err()
			}
			if errors.Is(err, context.Canceled) || errors.Is(err, platform.ErrScreenshotCanceled) {
				if path != "" {
					d.operation(func() error { return os.Remove(path) }, func(removeErr error) {
						if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
							dialog.ShowError(fmt.Errorf("remover captura cancelada: %w", removeErr), d.window)
						}
					})
				}
				if association != nil && association.live != nil {
					d.removeLiveScreenshot(association.live, association.marker)
				}
				return
			}
			if err != nil {
				if association != nil && association.live != nil {
					d.removeLiveScreenshot(association.live, association.marker)
				}
				dialog.ShowError(fmt.Errorf("não foi possível capturar a tela: %w", err), d.window)
				return
			}
			editor := newScreenshotEditor(d, path, document)
			editor.association = association
			d.attachScreenshot(association, path)
			d.screenshotEditors[editor] = struct{}{}
			editor.window.Show()
			editor.window.RequestFocus()
			d.ownScreenshotEditor(editor)
		})
	})
}

func (d *desktop) ownScreenshotEditor(editor *screenshotEditor) {
	if d.deps.ownWindow == nil {
		return
	}
	title := editor.window.Title()
	d.operation(func() error {
		for range 40 {
			if err := d.ctx.Err(); err != nil {
				return err
			}
			err := d.deps.ownWindow(title)
			if !errors.Is(err, platform.ErrWindowNotFound) {
				return err
			}
			if err := waitContext(d.ctx, 25*time.Millisecond); err != nil {
				return err
			}
		}
		return platform.ErrWindowNotFound
	}, func(err error) {
		if err != nil && !editor.closed && !errors.Is(err, context.Canceled) {
			dialog.ShowError(fmt.Errorf("posicionar editor de screenshot: %w", err), editor.window)
		}
	})
}
