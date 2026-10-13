package desktop

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"ai-talker/internal/config"
	"ai-talker/internal/playback"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type audioPlayer interface {
	Run(context.Context, string) error
	SetPaused(bool)
	SetVolume(int)
	SetSpeed(float64)
	Snapshot() playback.Snapshot
}

type audioPlayback struct {
	document *document
	source   string
	player   audioPlayer
	cancel   context.CancelFunc
}

func (d *desktop) playbackAction() {
	if d.audioMissing {
		d.showDocumentError(d.document, "O áudio foi removido para a Lixeira. A transcrição continua disponível.", "")
		return
	}
	if d.capture != captureIdle || d.closing || d.closeDialog || d.tabsDialog || d.discardDialog || d.lastAudio == "" {
		return
	}
	if current := d.playback; current != nil {
		state := current.player.Snapshot()
		if !state.Ready {
			d.stopPlayback()
			d.renderDocument(d.document)
		} else {
			current.player.SetPaused(!state.Paused)
		}
		d.syncControls()
		return
	}
	if d.deps.newPlayer == nil {
		d.showError("A reprodução de áudio não está disponível.", "")
		return
	}
	d.rememberDocumentView()
	ctx, cancel := context.WithCancel(d.ctx)
	session := &audioPlayback{
		document: d.document, source: d.lastAudio,
		player: d.deps.newPlayer(d.directory), cancel: cancel,
	}
	if session.player == nil {
		cancel()
		d.showError("Não foi possível preparar a reprodução de áudio.", "")
		return
	}
	session.player.SetVolume(config.CaptureVolume(d.settings.OutputVolume))
	session.player.SetSpeed(d.playbackSpeed(session.source))
	d.playback = session
	d.syncControls()
	d.workers.Go(func() {
		err := callOperation(func() error {
			return d.withPlaybackSlot(ctx, func() error { return session.player.Run(ctx, session.source) })
		})
		cancel()
		d.post(func() {
			if d.playback != session {
				return
			}
			d.playback = nil
			d.renderDocument(d.document)
			d.syncControls()
			if err != nil && !errors.Is(err, context.Canceled) {
				d.showDocumentError(session.document, fmt.Sprintf("Não foi possível ouvir o áudio: %v", err), "")
			}
		})
	})
}

func (d *desktop) stopPlaybackAction() {
	if d.capture != captureIdle || d.playback == nil {
		return
	}
	d.stopPlayback()
	d.renderDocument(d.document)
	d.syncControls()
}

// A replacement waits for the previous player's teardown. Recorder startup
// uses the same slot so a new capture cannot pick up the sound being stopped.
func (d *desktop) withPlaybackSlot(ctx context.Context, work func() error) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case d.playbackSlot <- struct{}{}:
	}
	defer func() { <-d.playbackSlot }()
	if err := ctx.Err(); err != nil {
		return err
	}
	return work()
}

func (d *desktop) stopPlayback() {
	if session := d.playback; session != nil {
		d.playback = nil
		session.cancel()
		if d.capture == captureIdle && session.document == d.document {
			setLabel(d.timer, d.view.timer, textColor)
		}
	}
}

func (d *desktop) stopDocumentPlayback(doc *document) {
	if d.playback != nil && d.playback.document == doc {
		d.stopPlayback()
	}
}

func (d *desktop) syncPlaybackControls() {
	if d.capture != captureIdle || d.lastAudio != "" && !d.audioMissing {
		d.pauseButton.Show()
	} else {
		d.pauseButton.Hide()
	}
	if session := d.playback; session != nil && (session.document != d.document || session.document.closed ||
		session.source != session.document.lastAudio || d.capture != captureIdle) {
		d.stopPlayback()
	}
	d.syncPlaybackVolume()
	d.stopPlaybackButton.Hide()
	if d.capture == captureIdle && d.playback != nil {
		d.stopPlaybackButton.Show()
		d.stopPlaybackButton.Enable()
	}
	if d.capture != captureIdle || d.lastAudio == "" || d.audioMissing {
		d.outputVolumeButton.Hide()
		d.playbackSpeedButton.Hide()
	} else {
		d.outputVolumeButton.Show()
		d.playbackSpeedButton.Show()
	}
	speed := d.playbackSpeed(d.lastAudio)
	d.playbackSpeedButton.SetText(playbackSpeedLabel(speed))
	d.playbackSpeedButton.setHint("Velocidade de reprodução · " + playbackSpeedLabel(speed) + " · tom natural preservado")
	setEnabled(d.playbackSpeedButton, d.capture == captureIdle && d.lastAudio != "" && !d.closing)
	d.pauseButton.Importance = widget.LowImportance
	if d.capture != captureIdle {
		setEnabled(d.pauseButton, d.capture == captureRunning && !d.discardDialog)
		d.pauseButton.setHint(uiText("Pausar"))
		d.pauseButton.SetIcon(theme.MediaPauseIcon())
		if d.capture == captureRunning && d.recorder.Snapshot().Paused {
			d.pauseButton.setHint(uiText("Retomar"))
			d.pauseButton.SetIcon(theme.MediaPlayIcon())
		}
		return
	}
	enabled := !d.closing && !d.closeDialog && !d.tabsDialog && d.lastAudio != "" && !d.audioMissing
	d.pauseButton.setHint(uiText("Ouvir áudio carregado · Ctrl+Shift+Espaço"))
	d.pauseButton.SetIcon(theme.MediaPlayIcon())
	if session := d.playback; session != nil {
		state := session.player.Snapshot()
		switch {
		case !state.Ready:
			d.pauseButton.setHint(uiText("Cancelar preparação da reprodução"))
			d.pauseButton.SetIcon(theme.CancelIcon())
		case !state.Active:
			enabled = false
			d.pauseButton.setHint(uiText("Concluindo reprodução"))
		case state.Paused:
			d.pauseButton.setHint(uiText("Retomar reprodução · Ctrl+Shift+Espaço"))
		default:
			d.pauseButton.Importance = widget.HighImportance
			d.pauseButton.setHint(uiText("Pausar reprodução · Play retoma deste ponto · Ctrl+Shift+Espaço"))
			d.pauseButton.SetIcon(theme.MediaPauseIcon())
		}
	}
	d.pauseButton.Refresh()
	setEnabled(d.pauseButton, enabled)
}

func playbackSourceKey(source string) string {
	return strings.ToLower(filepath.Clean(source))
}

func (d *desktop) playbackSpeed(source string) float64 {
	if d.playbackRates == nil || source == "" {
		return 1
	}
	if speed := d.playbackRates[playbackSourceKey(source)]; validPlaybackRate(speed) {
		return speed
	}
	return 1
}

func validPlaybackRate(speed float64) bool {
	switch speed {
	case 0.5, 0.75, 1, 1.25, 1.5, 2, 2.25, 2.5:
		return true
	default:
		return false
	}
}

func playbackSpeedLabel(speed float64) string {
	return strings.ReplaceAll(strconv.FormatFloat(speed, 'f', -1, 64), ".", ",") + "×"
}

func (d *desktop) openPlaybackSpeed() {
	if d.capture != captureIdle || d.lastAudio == "" || d.closing {
		return
	}
	current := d.playbackSpeed(d.lastAudio)
	items := make([]*fyne.MenuItem, 0, 8)
	for _, speed := range []float64{0.5, 0.75, 1, 1.25, 1.5, 2, 2.25, 2.5} {
		rate := speed
		item := fyne.NewMenuItem(playbackSpeedLabel(rate), d.guard(func() { d.setPlaybackSpeed(rate) }))
		item.Checked = rate == current
		items = append(items, item)
	}
	menu := widget.NewPopUpMenu(fyne.NewMenu(uiText("Velocidade"), items...), d.window.Canvas())
	menu.ShowAtRelativePosition(fyne.NewPos(0, d.playbackSpeedButton.Size().Height), d.playbackSpeedButton)
}

func (d *desktop) setPlaybackSpeed(speed float64) {
	if d.capture != captureIdle || d.lastAudio == "" || !validPlaybackRate(speed) {
		return
	}
	if d.playbackRates == nil {
		d.playbackRates = make(map[string]float64)
	}
	key := playbackSourceKey(d.lastAudio)
	d.playbackRates[key] = speed
	if session := d.playback; session != nil && playbackSourceKey(session.source) == key {
		session.player.SetSpeed(speed)
	}
	d.syncPlaybackControls()
}

func (d *desktop) renderPlayback() {
	d.wave.seekable = false
	if session := d.playback; session != nil && session.document == d.document &&
		session.source == d.lastAudio && !session.document.closed && d.capture == captureIdle {
		if state := session.player.Snapshot(); state.Ready {
			setLabel(d.timer, clockText(state.Position), textColor)
			d.wave.seekable = state.Active && state.Duration > 0
			d.wave.position = boundedLevel(state.Position / max(0.001, state.Duration))
		}
	}
	d.wave.Refresh()
}

func (d *desktop) seekPlayback(fraction float64) {
	session := d.playback
	if session == nil || d.capture != captureIdle || session.document != d.document {
		return
	}
	if player, ok := session.player.(interface{ Seek(float64) }); ok {
		state := session.player.Snapshot()
		player.Seek(state.Duration * boundedLevel(fraction))
	}
}
