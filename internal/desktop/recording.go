package desktop

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"ai-talker/internal/config"
	"ai-talker/internal/platform"
	"ai-talker/internal/recorder"

	"fyne.io/fyne/v2/widget"
)

func (d *desktop) recordAction() {
	if d.settingsForm != nil || (d.captureDevicesOpen() && d.capture == captureIdle) || d.closeDialog || d.tabsDialog || d.discardDialog || d.videoSelecting {
		return
	}
	if d.voiceSession != nil {
		d.stopVoiceSession()
		return
	}
	if d.document.voice != nil && d.document.voice.Mode == voiceModeLive && !d.busy {
		d.startVoiceSession()
		return
	}
	switch {
	case d.capture == captureRunning:
		d.stopCapture(transcribeCapture)
	case d.capture != captureIdle:
		return
	case d.busy:
		d.cancelJob()
	case d.capture == captureIdle:
		d.startCapture()
	}
}

func (d *desktop) startCapture() {
	if err := d.captureDeviceSelectionError(); err != nil {
		d.showDocumentError(d.document, err.Error(), "")
		return
	}
	d.returnToCurrentPrompt(d.document)
	if !d.preserveAudioHistory(d.document) {
		return
	}
	d.stopPlayback()
	d.invalidateJob()
	doc := d.document
	origin := &captureOrigin{
		document: doc, generation: doc.generation, source: doc.lastAudio,
		result: doc.lastResult, append: doc.recordedAppend, sourceLabel: doc.sourceLabel.Text,
		settings:         cloneSettings(d.settings),
		previousLive:     doc.live,
		transcriptionRun: doc.transcriptionRun,
	}
	if origin.settings.TranscriptionProvider() != config.ProviderBedrock {
		origin.settings.Capture.Stream = false
	}
	if doc.voice != nil {
		origin.settings.Capture.Stream = doc.voice.Mode == voiceModeStream && origin.settings.TranscriptionProvider() == config.ProviderBedrock
	}
	if !d.prepareDocumentTranscript(doc, "Gravação atual", true) {
		return
	}
	videoRequested := d.videoArmed && d.videoRegion != nil
	var videoRegion platform.ScreenRegion
	if videoRequested {
		videoRegion = *d.videoRegion
	}
	d.captureEpoch++
	epoch := d.captureEpoch
	d.captureOrigin = origin
	d.captureDocument = doc
	captureContext, cancel := context.WithCancel(d.ctx)
	d.captureCancel = cancel
	folder := filepath.Join(recordingFolder(d.directory), d.deps.now().Format("2006-01-02"))
	videoFolder := filepath.Join(d.directory, "videos", d.deps.now().Format("2006-01-02"))
	var r recording
	if d.deps.newConfiguredRecorder != nil {
		r = d.deps.newConfiguredRecorder(origin.settings, folder)
	} else {
		r = d.deps.newRecorder(origin.settings.Device(), folder)
	}
	d.recorder = r
	d.applyCaptureVolumes(r, origin.settings)
	d.capture = captureStarting
	d.afterCapture = keepCapture
	d.captureError = ""
	d.wave.reset()
	setLabel(d.timer, "00:00", textColor)
	d.recordState.Importance = widget.MediumImportance
	d.recordState.SetText("Abrindo os dispositivos de áudio…")
	d.status.SetText("Preparando a gravação.")
	d.syncControls()

	var path string
	var videoSession screenVideoCapture
	var videoStartErr error
	d.operation(func() error {
		return d.withPlaybackSlot(captureContext, func() error {
			var err error
			path, err = r.Start(captureContext)
			if err != nil || !videoRequested {
				return err
			}
			if d.deps.newVideoRecorder == nil {
				videoStartErr = errors.New("a captura de vídeo não está disponível; o áudio continuará sendo gravado")
				return nil
			}
			videoSession, err = d.deps.newVideoRecorder(videoFolder, path, videoRegion)
			if err != nil {
				videoStartErr = fmt.Errorf("não foi possível preparar o vídeo; o áudio continuará sendo gravado: %w", err)
				videoSession = nil
				return nil
			}
			configureVideoAudio(videoSession, r.Snapshot())
			err = configureVideoPreset(videoSession, origin.settings.VideoQuality())
			if err == nil {
				err = videoSession.Start(captureContext)
			}
			if err != nil {
				videoStartErr = errors.Join(
					fmt.Errorf("não foi possível iniciar o vídeo; o áudio continuará sendo gravado: %w", err),
					videoSession.Abort(),
				)
				videoSession = nil
				return nil
			}
			return nil
		})
	}, func(err error) {
		if epoch != d.captureEpoch {
			return
		}
		if path != "" {
			origin.recordingSource = path
			doc.lastAudio = path
			doc.audioMissing = false
			doc.transcriptionRun = ""
			doc.recordedAppend = &recordedAppend{Source: path, Prefix: doc.editor.Text, Combined: doc.editor.Text}
		}
		if err != nil {
			d.videoSession = nil
			d.captureError = err.Error()
			d.stopCapture(failedCapture)
			return
		}
		d.videoSession = videoSession
		if videoRequested {
			d.videoArmed, d.videoRegion = false, nil
		}
		d.capture = captureRunning
		d.recordState.Importance = widget.DangerImportance
		d.recordState.SetText("Pode falar. Estou gravando.")
		switch origin.settings.Capture.SourceMode() {
		case config.CaptureTeam:
			d.recordState.SetText("Gravando sua fala e o áudio do computador.")
		case config.CaptureSystem:
			d.recordState.SetText("Gravando o áudio do computador.")
		}
		if videoSession != nil {
			d.recordState.SetText("Gravando áudio e vídeo da área selecionada.")
		} else if videoStartErr != nil {
			d.recordState.Importance = widget.WarningImportance
			d.recordState.SetText(shortMessage(videoStartErr.Error()))
		}
		if videoStartErr != nil {
			d.status.SetText("Somente o áudio está sendo gravado. A área de vídeo não iniciou.")
		} else {
			d.status.SetText("O áudio está sendo salvo no seu computador.")
		}
		d.beginLiveCapture(doc, origin, r)
		d.syncControls()
	})
}

func (d *desktop) pauseAction() {
	if d.capture == captureIdle {
		d.playbackAction()
		return
	}
	if d.capture != captureRunning || d.discardDialog {
		return
	}
	paused := d.recorder.Snapshot().Paused
	if paused {
		d.recorder.Resume()
		d.syncVideoSegment(d.videoSession, false)
		d.recordState.Importance = widget.DangerImportance
		d.recordState.SetText("Pode continuar.")
	} else {
		d.recorder.Pause()
		d.syncVideoSegment(d.videoSession, true)
		d.recordState.Importance = widget.MediumImportance
		d.recordState.SetText("Pausado. Esse intervalo não entra no áudio nem no vídeo.")
	}
	d.syncControls()
}

func (d *desktop) stopCapture(action captureAction) {
	if d.voiceSession != nil {
		d.stopVoiceSession()
		return
	}
	d.afterCapture = action
	if d.capture == captureStopping {
		return
	}
	if d.recorder == nil {
		d.capture = captureIdle
		d.syncControls()
		return
	}
	wasStarting := d.capture == captureStarting
	d.captureEpoch++
	epoch := d.captureEpoch
	d.capture = captureStopping
	r := d.recorder
	doc := d.captureDocument
	videoSession := d.videoSession
	videoCommandsDone := d.videoCommandsDone(videoSession)
	videoMode := config.CaptureMicrophone
	if d.captureOrigin != nil {
		videoMode = d.captureOrigin.settings.Capture.SourceMode()
	}
	cancel := d.captureCancel
	if d.captureOrigin != nil && d.captureOrigin.live != nil && action != transcribeCapture && action != failedCapture {
		d.cancelLiveRecording(d.captureOrigin.live)
	}
	if wasStarting && cancel != nil {
		// Opening a device can be cancelled. During normal capture Stop must
		// finish before cancelling its context, so cancellation is not saved
		// as a recording failure by the native recorder.
		cancel()
	}
	d.recordState.SetText("Salvando o áudio gravado…")
	d.syncControls()
	var path, videoPath string
	var videoErr error
	d.operation(func() error {
		var err error
		path, err = r.Stop()
		if videoSession != nil {
			if videoCommandsDone != nil {
				<-videoCommandsDone
			}
			if path != "" {
				ctx, finishCancel := context.WithTimeout(d.ctx, 30*time.Minute)
				videoPath, videoErr = videoSession.Finish(ctx, recordingTracks(r, videoMode))
				finishCancel()
			} else {
				videoErr = errors.New("o áudio não foi salvo; o vídeo não pode ser finalizado")
				videoErr = errors.Join(videoErr, videoSession.Abort())
			}
		}
		if cancel != nil {
			cancel()
		}
		return err
	}, func(err error) {
		if epoch != d.captureEpoch {
			return
		}
		d.recorder = nil
		d.captureCancel = nil
		d.videoSession = nil
		if d.videoCommandSession == videoSession {
			d.videoCommandSession, d.videoCommandTail = nil, nil
		}
		d.capture = captureIdle
		if path != "" {
			doc.lastAudio = path
			doc.view.timer = clockText(r.Snapshot().Duration)
			if doc.recordedAppend == nil || doc.recordedAppend.Source != path {
				doc.recordedAppend = &recordedAppend{Source: path, Prefix: doc.editor.Text, Combined: doc.editor.Text}
			}
			if d.captureOrigin != nil && d.captureOrigin.recordingSource == path {
				doc.recordedAppend.Screenshots = slices.Clone(d.captureOrigin.screenshots)
			}
			if videoPath != "" {
				doc.recordedAppend.VideoPath = videoPath
				if saveErr := d.saveWorkspace(); saveErr != nil {
					videoErr = errors.Join(videoErr, fmt.Errorf("o vídeo foi salvo, mas a referência não pôde ser salva nas abas: %w", saveErr))
				}
			}
		}
		if d.captureOrigin != nil && d.captureOrigin.live != nil {
			d.captureOrigin.live.Tracks = recordingTracks(r, d.captureOrigin.live.settings.Capture.SourceMode())
		}
		d.syncControls()
		completedAction := d.afterCapture
		d.captureStopped(path, err)
		if completedAction != keepCapture && completedAction != transcribeCapture {
			return
		}
		if err != nil {
			if videoErr != nil {
				d.showDocumentError(doc, errors.Join(err, fmt.Errorf("finalizar vídeo: %w", videoErr)).Error(), "")
			}
			return
		}
		if path == "" {
			return
		}
		if videoErr != nil {
			doc.view.status = "O áudio foi preservado. Não foi possível finalizar o vídeo: " + shortMessage(videoErr.Error())
		} else if videoPath != "" {
			doc.view.status = "Áudio e vídeo salvos. Use o botão de vídeo ao lado do nome da gravação para abrir o arquivo."
		}
		if doc == d.document {
			d.status.SetText(doc.view.status)
		}
	})
}

func (d *desktop) captureStopped(path string, err error) {
	action := d.afterCapture
	d.afterCapture = keepCapture
	doc := d.captureDocument
	if doc == nil {
		doc = d.document
	}
	d.captureDocument = nil
	origin := d.captureOrigin
	d.captureOrigin = nil
	if origin != nil && (action == discardCapture || action == newCapture) {
		origin.ignoreScreenshots = true
	}
	if origin != nil && origin.live != nil {
		if action != transcribeCapture && action != failedCapture {
			d.cancelLiveRecording(origin.live)
		}
		if err != nil && action != discardCapture && action != newCapture && action != closeCapture {
			origin.live.CaptureError = shortMessage(err.Error())
		}
	}
	switch action {
	case discardCapture:
		d.restoreDiscardedCapture(doc, origin)
		if err != nil && !errors.Is(err, recorder.ErrNoAudio) && !errors.Is(err, recorder.ErrNotRecording) {
			d.showDocumentError(doc, "A gravação foi encerrada sem transcrever. "+err.Error(), "")
		}
		d.renderDocument(d.document)
		d.syncControls()
	case failedCapture:
		message := d.captureError
		d.captureError = ""
		if message == "" && err != nil {
			message = err.Error()
		}
		d.showDocumentError(doc, message, "")
		if origin != nil && origin.live != nil {
			d.finishLiveCapture(origin.live)
		}
		if path != "" {
			doc.retryButton.Show()
		}
		d.syncControls()
	case newCapture:
		// Cancelling device startup or discarding an empty new recording is a
		// successful reset. The native recorder still preserves its file.
		cancelledStart := errors.Is(err, context.Canceled)
		emptyCapture := errors.Is(err, recorder.ErrNoAudio) || errors.Is(err, recorder.ErrNotRecording)
		expected := cancelledStart || emptyCapture
		if err != nil && !expected {
			d.showDocumentError(doc, err.Error(), "")
			return
		}
		if doc == d.document {
			d.resetAudio()
		} else {
			doc.lastAudio = ""
			doc.audioMissing = false
			doc.transcriptionRun = ""
			doc.lastResult = nil
			doc.live = nil
			doc.view = defaultDocumentView()
		}
		d.renderDocument(d.document)
	case closeCapture:
		cancelledStart := errors.Is(err, context.Canceled) || errors.Is(err, recorder.ErrNotRecording)
		emptyCapture := errors.Is(err, recorder.ErrNoAudio)
		if err != nil && !cancelledStart && !emptyCapture {
			d.showDocumentError(doc, err.Error(), "")
			return
		}
		d.requestClose()
	case transcribeCapture:
		if origin != nil && origin.live != nil {
			if err != nil {
				d.showDocumentError(doc, err.Error(), "")
			}
			d.finishLiveCapture(origin.live)
			d.renderDocument(d.document)
			d.syncControls()
			return
		}
		if err != nil {
			d.showDocumentError(doc, err.Error(), "")
			if path != "" {
				doc.retryButton.Show()
			}
			d.syncControls()
			return
		}
		if path == "" {
			d.showDocumentError(doc, "A gravação não retornou um arquivo de áudio.", "")
			return
		}
		d.startRecordedJob(doc, path)
		d.renderDocument(d.document)
	default:
		if err != nil {
			d.showDocumentError(doc, err.Error(), "")
			return
		}
		doc.view.importance = widget.MediumImportance
		doc.view.recordText = "Áudio salvo."
		doc.view.status = "O áudio foi preservado."
		d.renderDocument(d.document)
	}
}

func (d *desktop) tick() {
	d.tickVoicePlayback()
	d.expireClosedDocument()
	if d.instance != nil {
		activate, err := d.instance.consumeActivation()
		if err != nil {
			// A launcher may still have the marker open on Windows; retry on
			// the next tick without stopping recording or the event loop.
			d.instance.logActivationError(err)
		}
		if activate {
			d.activateWindow()
		}
	}
	if d.capture == captureRunning {
		snapshot := d.recorder.Snapshot()
		setLabel(d.timer, clockText(snapshot.Duration), textColor)
		d.wave.push(snapshot.Level)
		d.tickLiveCapture()
		switch {
		case snapshot.Error != "":
			d.captureError = snapshot.Error
			d.stopCapture(failedCapture)
		case !snapshot.Active:
			d.captureError = "A gravação foi interrompida. O áudio já salvo foi preservado."
			d.stopCapture(failedCapture)
		case snapshot.Warning != "":
			d.status.SetText(snapshot.Warning)
		}
	} else {
		d.wave.push(0)
	}
	if d.capture == captureIdle && d.busy && d.deps.now().Sub(d.lastProgressAt).Seconds() >= 10 {
		elapsed := d.deps.now().Sub(d.jobStarted).Seconds()
		message := "Aguardando a AWS"
		if d.jobProvider == config.ProviderWhisper {
			message = "Whisper transcrevendo neste computador"
		}
		if d.jobProgress.Stage == "wait" {
			message = "Aguardando a tentativa anterior"
		}
		if d.jobProgress.Total > 1 {
			message += fmt.Sprintf(" · %d/%d trechos", d.jobProgress.Completed, d.jobProgress.Total)
		} else if d.live != nil && d.live.stopped {
			message += " · " + d.view.recordText
		}
		d.recordState.SetText(fmt.Sprintf("%s · %s", message, clockText(elapsed)))
	}
	if d.playback != nil {
		d.syncPlaybackControls()
		d.renderPlayback()
	}
}
