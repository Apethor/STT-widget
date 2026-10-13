package desktop

import (
	"context"
	"errors"
	"fmt"
	"time"

	"ai-talker/internal/platform"
	"ai-talker/internal/recorder"
	"ai-talker/internal/video"
)

func (d *desktop) syncVideoButton() {
	if d.videoButton == nil {
		return
	}
	if d.deps.selectVideoRegion == nil {
		d.videoButton.Hide()
		return
	}
	d.videoButton.Show()
	if d.videoArmed {
		d.videoButton.setHint(uiText("Vídeo preparado para a próxima gravação · clicar para cancelar"))
	} else {
		d.videoButton.setHint(uiText("Selecionar área da tela para gravar vídeo junto com o áudio"))
	}
	setEnabled(d.videoButton, d.capture == captureIdle && !d.busy &&
		!d.videoSelecting && !d.closing && !d.closeDialog && !d.discardDialog)
}

func (d *desktop) armVideoCapture() {
	if d.capture != captureIdle || d.busy || d.videoSelecting || d.closing {
		return
	}
	if d.videoArmed {
		d.videoArmed, d.videoRegion = false, nil
		d.status.SetText("A gravação de vídeo foi desativada.")
		d.syncControls()
		return
	}
	if d.deps.selectVideoRegion == nil {
		d.showError("A seleção da área de vídeo não está disponível.", "")
		return
	}
	ctx, cancel := context.WithCancel(d.ctx)
	d.videoSelecting = true
	d.syncControls()
	d.workers.Go(func() {
		region, err := d.deps.selectVideoRegion(ctx)
		cancel()
		d.post(func() {
			d.videoSelecting = false
			if d.closing || errors.Is(err, context.Canceled) {
				d.syncControls()
				return
			}
			if err != nil {
				if !errors.Is(err, platform.ErrScreenshotCanceled) {
					d.showError("Não foi possível selecionar a área de vídeo: "+err.Error(), "")
				}
				d.syncControls()
				return
			}
			region, err = video.NormalizeRegion(region)
			if err != nil {
				d.showError(err.Error(), "")
				d.syncControls()
				return
			}
			d.videoRegion, d.videoArmed = &region, true
			d.status.SetText(fmt.Sprintf("Vídeo preparado · área %d × %d · próxima gravação.", region.Width, region.Height))
			d.syncControls()
		})
	})
}

func (d *desktop) syncVideoSegment(session screenVideoCapture, pause bool) {
	if session == nil || d.videoSession != session {
		return
	}
	if d.videoCommandSession != session {
		d.videoCommandSession, d.videoCommandTail = session, nil
	}
	previous := d.videoCommandTail
	completed := make(chan struct{})
	d.videoCommandTail = completed
	position := d.recorder.Snapshot().Duration
	activeAt := d.deps.now()
	d.workers.Go(func() {
		defer close(completed)
		if previous != nil {
			select {
			case <-previous:
			case <-d.ctx.Done():
				return
			}
		}
		ctx, cancel := context.WithTimeout(d.ctx, 15*time.Second)
		defer cancel()
		var err error
		if aligned, ok := session.(interface {
			PauseAt(context.Context, float64, time.Time) error
			ResumeAt(context.Context, float64, time.Time) error
		}); ok {
			if pause {
				err = aligned.PauseAt(ctx, position, activeAt)
			} else {
				err = aligned.ResumeAt(ctx, position, activeAt)
			}
		} else if pause {
			err = session.Pause(ctx)
		} else {
			err = session.Resume(ctx)
		}
		d.post(func() {
			if d.videoSession != session || d.capture != captureRunning {
				return
			}
			if err != nil {
				d.captureError = "A captura de vídeo foi interrompida: " + err.Error()
				d.stopCapture(failedCapture)
			}
		})
	})
}

func configureVideoAudio(session screenVideoCapture, snapshot recorder.Snapshot) {
	if aligned, ok := session.(interface{ SetAudioOrigin(recorder.Snapshot) }); ok {
		aligned.SetAudioOrigin(snapshot)
	}
}

func configureVideoPreset(session screenVideoCapture, preset string) error {
	if configured, ok := session.(interface{ SetPreset(video.Preset) error }); ok {
		return configured.SetPreset(video.Preset(preset))
	}
	return nil
}

// Capture this tail on the UI thread before starting Stop's worker. Finish
// then waits for every pause/resume already requested by the user.
func (d *desktop) videoCommandsDone(session screenVideoCapture) <-chan struct{} {
	if session == nil || d.videoCommandSession != session {
		return nil
	}
	return d.videoCommandTail
}

func (d *desktop) openRecordedVideo() {
	if d.recordedAppend == nil || d.recordedAppend.VideoPath == "" || d.deps.openFile == nil {
		return
	}
	path := d.recordedAppend.VideoPath
	doc := d.document
	d.operation(func() error { return d.deps.openFile(path) }, func(err error) {
		if err != nil {
			d.showDocumentError(doc, "Não foi possível abrir o vídeo: "+err.Error(), "")
		}
	})
}
