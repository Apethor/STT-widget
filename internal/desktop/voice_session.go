package desktop

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"ai-talker/internal/config"
	"ai-talker/internal/realtime"

	"fyne.io/fyne/v2/widget"
)

type voiceConnection interface {
	SendAudio(context.Context, []byte) error
	Close(context.Context) error
	Cancel()
	Interrupt()
	Done() <-chan struct{}
	Err() error
}

type voiceSession struct {
	doc               *document
	ctx               context.Context
	cancel            context.CancelFunc
	connection        voiceConnection
	recorder          recording
	files             responseFiles
	stopping          bool
	relayDone         chan error
	startupDone       chan struct{}
	startupConnection voiceConnection
	startupRecorder   recording
	startupPath       string
	quitPending       bool
	quitReady         bool
	uiAudioBytes      map[string]int
}

func (d *desktop) startVoiceSession() {
	doc := d.document
	if d.capture != captureIdle || doc.processBusy || doc.busy || d.voiceSession != nil {
		return
	}
	if d.deps.newConversation == nil || !realtime.SupportsRegion(d.settings.Region) {
		d.showDocumentError(doc, uiText("Conversa ao vivo indisponível nesta configuração AWS."), "")
		return
	}
	if d.unavailableMicrophone {
		d.showDocumentError(doc, uiText("Selecione um microfone disponível antes de iniciar a conversa."), "")
		return
	}
	if !d.preserveAudioHistory(doc) {
		return
	}
	d.stopPlayback()
	d.stopResponsePlayback()
	d.ensureResponsePanel(doc)
	settings := cloneSettings(d.settings)
	settings.Capture.Mode, settings.Capture.Stream = config.CaptureMicrophone, false
	// Conversation captures microphone independently of the conventional source.
	options := realtime.Options{ModelID: settings.Conversation.Model(), VoiceID: settings.Conversation.Voice()}
	for _, turn := range doc.voice.History {
		if turn.UserText != "" {
			options.History = append(options.History, realtime.Message{Role: "USER", Text: turn.UserText})
		}
		if turn.Complete && turn.Text != "" {
			options.History = append(options.History, realtime.Message{Role: "ASSISTANT", Text: turn.Text})
		}
	}
	if len(options.History) > 38 {
		options.History = slices.Clone(options.History[len(options.History)-38:])
	}
	ctx, cancel := context.WithCancel(d.ctx)
	session := &voiceSession{
		doc: doc, ctx: ctx, cancel: cancel, relayDone: make(chan error, 1),
		startupDone: make(chan struct{}),
		files:       responseFiles{folder: responseFolder(d.directory, d.deps.now())},
	}
	d.voiceSession, d.captureDocument = session, doc
	d.capture = captureStarting
	d.recordState.SetText(uiText("Conectando a conversa ao vivo…"))
	d.status.SetText(uiText("Preparando voz e microfone."))
	d.syncControls()
	var connection voiceConnection
	var r recording
	var path string
	d.operation(func() error {
		defer func() {
			session.startupConnection, session.startupRecorder, session.startupPath = connection, r, path
			close(session.startupDone)
		}()
		var err error
		connection, err = d.deps.newConversation(ctx, settings, options, func(event realtime.Event) {
			var audioPath string
			var audioEnd int
			if event.Kind == realtime.AssistantAudio {
				var fileErr error
				audioPath, audioEnd, fileErr = session.files.appendBlock(event.CompletionID, event.PCM, event.SampleRate)
				if fileErr != nil {
					event.Err = fileErr
					event.Kind = realtime.SessionEnd
				}
			}
			d.post(func() { d.receiveVoiceEvent(session, event, audioPath, audioEnd) })
		})
		if err != nil {
			return err
		}
		if d.deps.newConfiguredRecorder != nil {
			r = d.deps.newConfiguredRecorder(settings, filepath.Join(recordingFolder(d.directory), d.deps.now().Format("2006-01-02")))
		} else {
			r = d.deps.newRecorder(settings.Device(), recordingFolder(d.directory))
		}
		d.applyCaptureVolumes(r, settings)
		path, err = r.Start(ctx)
		if err != nil {
			connection.Cancel()
			<-connection.Done()
			_, _ = r.Stop()
			return errors.Join(err, session.files.close())
		}
		if ctx.Err() != nil || d.stopped.Load() {
			_, _ = r.Stop()
			connection.Cancel()
			<-connection.Done()
			_ = session.files.close()
			return ctx.Err()
		}
		return err
	}, func(err error) {
		if d.voiceSession != session {
			cancel()
			if connection != nil {
				connection.Cancel()
			}
			d.workers.Go(func() {
				if connection != nil {
					<-connection.Done()
				}
				if r != nil {
					_, _ = r.Stop()
				}
				_ = session.files.close()
			})
			return
		}
		session.connection, session.recorder = connection, r
		if err != nil {
			cancel()
			if path != "" {
				doc.lastAudio = path
				doc.audioMissing = false
			}
			if session.quitPending {
				// The Quit worker owns Done, the final snapshot and saving.
				// Retain the session so another close cannot bypass it.
				return
			}
			d.mergeVoiceSnapshot(session)
			d.voiceSession, d.captureDocument, d.recorder = nil, nil, nil
			d.capture = captureIdle
			d.workers.Go(func() {
				if connection != nil {
					connection.Cancel()
					<-connection.Done()
				}
				if r != nil {
					_, _ = r.Stop()
				}
				_ = session.files.close()
			})
			_ = d.saveWorkspace()
			d.showDocumentError(doc, uiText("Não foi possível iniciar a conversa: ")+err.Error(), "")
			d.syncControls()
			return
		}
		d.recorder = r
		d.capture = captureRunning
		doc.lastAudio = path
		doc.audioMissing = false
		doc.view.recordText = uiText("Pode falar. A conversa está ao vivo.")
		doc.view.importance = widget.SuccessImportance
		d.recordState.SetText(doc.view.recordText)
		d.recordState.Importance = doc.view.importance
		d.status.SetText(uiText("O microfone é enviado ao serviço. As respostas chegam automaticamente nas pausas."))
		d.syncControls()
		d.workers.Go(func() {
			err := relayVoiceCapture(ctx, r, connection.SendAudio)
			session.relayDone <- err
			if err != nil && !errors.Is(err, context.Canceled) {
				d.post(func() {
					if d.voiceSession == session && !session.stopping {
						d.status.SetText(shortMessage(err.Error()))
						d.stopVoiceSession()
					}
				})
			}
		})
		d.workers.Go(func() {
			select {
			case <-ctx.Done():
			case <-connection.Done():
				d.post(func() {
					if d.voiceSession == session && !session.stopping {
						d.stopVoiceSession()
					}
				})
			}
		})
	})
}

func (d *desktop) stopVoiceSession() {
	session := d.voiceSession
	if session == nil || session.stopping {
		return
	}
	if session.recorder == nil {
		session.cancel()
		return
	}
	session.stopping = true
	d.capture = captureStopping
	d.recordState.SetText(uiText("Encerrando a conversa…"))
	d.syncControls()
	var path string
	d.operation(func() error {
		var err error
		path, err = session.recorder.Stop()
		select {
		case relayErr := <-session.relayDone:
			if !errors.Is(relayErr, context.Canceled) {
				err = errors.Join(err, relayErr)
			}
		case <-time.After(3 * time.Second):
			err = errors.Join(err, errors.New("o envio final da conversa não terminou no prazo"))
		}
		if session.connection != nil {
			closeCtx, closeCancel := context.WithTimeout(d.ctx, 5*time.Second)
			closeErr := session.connection.Close(closeCtx)
			closeCancel()
			if !errors.Is(closeErr, context.Canceled) {
				err = errors.Join(err, closeErr)
			}
		}
		session.cancel()
		err = errors.Join(err, session.files.close())
		return err
	}, func(err error) {
		if d.voiceSession != session {
			return
		}
		if session.quitPending {
			// The Quit worker saves the final provider snapshot after Done.
			return
		}
		d.voiceSession, d.recorder, d.captureDocument = nil, nil, nil
		d.capture = captureIdle
		doc := session.doc
		if path != "" {
			doc.lastAudio = path
		}
		for i := range doc.voice.History {
			turn := &doc.voice.History[i]
			if !turn.Complete {
				turn.Interrupted = true
			}
		}
		if playback := d.responsePlayback; playback != nil && playback.doc == doc {
			d.finishResponsePlayer(playback)
		}
		doc.view.recordText = uiText("Conversa encerrada.")
		doc.view.status = uiText("Texto e áudio da conversa foram preservados.")
		doc.view.importance = widget.SuccessImportance
		if err != nil {
			doc.view.status = uiText("Conversa incompleta. O conteúdo recebido foi preservado: ") + shortMessage(err.Error())
			doc.view.importance = widget.WarningImportance
		}
		if saveErr := d.saveWorkspace(); saveErr != nil {
			doc.view.status = fmt.Sprintf("%s · %s", doc.view.status, saveErr.Error())
		}
		d.renderVoiceHistory(doc)
		d.renderDocument(doc)
		d.syncControls()
	})
}

func (d *desktop) cancelVoiceSession() {
	session := d.voiceSession
	if session == nil {
		return
	}
	connection := session.connection
	if connection != nil {
		connection.Cancel()
	}
	session.cancel()
	d.workers.Go(func() {
		if connection != nil {
			<-connection.Done()
		}
		_ = session.files.close()
	})
}

func (d *desktop) receiveVoiceEvent(session *voiceSession, event realtime.Event, audioPath string, audioEnds ...int) {
	if d.voiceSession != session || session.doc.closed {
		return
	}
	doc := session.doc
	v := doc.voice
	if event.Kind == realtime.Interrupted {
		if d.responsePlayback != nil && d.responsePlayback.doc == doc && d.responsePlayback.id == event.CompletionID {
			d.stopResponsePlayback()
		}
		for i := range v.History {
			if v.History[i].ID == event.CompletionID {
				v.History[i].Interrupted = true
				v.History[i].Complete = false
				break
			}
		}
		d.renderVoiceHistory(doc)
		return
	}
	if event.Kind == realtime.SessionEnd {
		if event.Err != nil {
			d.status.SetText(shortMessage(event.Err.Error()))
			d.stopVoiceSession()
		}
		return
	}
	var turn *voiceTurn
	for i := range v.History {
		if v.History[i].ID == event.CompletionID {
			turn = &v.History[i]
			break
		}
	}
	if turn == nil {
		if event.Kind == realtime.UserText && !event.Final {
			return
		}
		v.History = append(v.History, voiceTurn{ID: event.CompletionID, Created: d.deps.now()})
		v.index = len(v.History) - 1
		turn = &v.History[v.index]
	}
	switch event.Kind {
	case realtime.UserText:
		if event.Final && turn.UserText != event.Text {
			d.updateVoiceTranscript(doc, turn.UserText, event.Text)
			turn.UserText = event.Text
		}
	case realtime.AssistantText:
		turn.Text = event.Text
	case realtime.AssistantAudio:
		turn.AudioPath, turn.SampleRate = audioPath, event.SampleRate
		if session.uiAudioBytes == nil {
			session.uiAudioBytes = make(map[string]int)
		}
		start := session.uiAudioBytes[turn.ID]
		if len(audioEnds) != 0 {
			start = audioEnds[0] - len(event.PCM)
		}
		session.uiAudioBytes[turn.ID] = max(session.uiAudioBytes[turn.ID], start+len(event.PCM))
		playing := d.responsePlayback != nil && d.responsePlayback.doc == doc && d.responsePlayback.id == turn.ID
		if d.settings.Conversation.AutoPlay() || playing {
			d.appendResponsePlayback(doc, turn, event.PCM, start)
		}
	case realtime.ResponseEnd:
		turn.Complete, turn.Interrupted = event.Final, event.Interrupted
		if d.responsePlayback != nil && d.responsePlayback.doc == doc && d.responsePlayback.id == turn.ID {
			d.finishResponsePlayer(d.responsePlayback)
		}
		if err := d.saveWorkspace(); err != nil {
			d.status.SetText(uiText("Não foi possível salvar o histórico da conversa: ") + err.Error())
		}
	}
	d.renderVoiceHistory(doc)
}

func (d *desktop) updateVoiceTranscript(doc *document, previous, current string) {
	if current == previous || current == "" {
		return
	}
	switch {
	case previous != "" && strings.HasSuffix(doc.editor.Text, previous):
		doc.editor.SetText(strings.TrimSuffix(doc.editor.Text, previous) + current)
	case previous != "" && strings.HasPrefix(current, previous):
		if suffix := strings.TrimSpace(strings.TrimPrefix(current, previous)); suffix != "" {
			doc.editor.SetText(appendTranscriptText(doc.editor.Text, suffix))
		}
	default:
		doc.editor.SetText(appendTranscriptText(doc.editor.Text, current))
	}
}

// preserveVoiceSnapshot runs on the UI thread before shutdown saves. Provider
// state includes events already received but still waiting in the UI queue.
func (d *desktop) preserveVoiceSnapshot() {
	if session := d.voiceSession; session != nil {
		d.mergeVoiceSnapshot(session)
	}
}

// finishVoiceBeforeQuit keeps the UI responsive while the canceled provider
// completes its final callback and capture/file owners release their resources.
// Saving is deferred until this final snapshot includes every received block.
func (d *desktop) finishVoiceBeforeQuit() bool {
	session := d.voiceSession
	if session == nil || session.quitReady {
		return false
	}
	if session.quitPending {
		return true
	}
	session.quitPending, session.stopping = true, true
	connection, capture := session.connection, session.recorder
	session.cancel()
	if connection != nil {
		connection.Cancel()
	}
	d.stopResponsePlayback()
	d.capture = captureStopping
	d.recordState.SetText(uiText("Encerrando a conversa…"))
	d.syncControls()
	var path string
	d.operation(func() error {
		if session.startupDone != nil {
			<-session.startupDone
			connection, capture, path = session.startupConnection, session.startupRecorder, session.startupPath
		}
		if connection != nil {
			connection.Cancel()
		}
		var err error
		if capture != nil {
			var capturePath string
			capturePath, err = capture.Stop()
			if capturePath != "" {
				path = capturePath
			}
		}
		if connection != nil {
			<-connection.Done()
		}
		return errors.Join(err, session.files.close())
	}, func(err error) {
		// Startup completion may already have removed the active session.
		if path != "" {
			session.doc.lastAudio, session.doc.audioMissing = path, false
		}
		d.mergeVoiceConnectionSnapshot(session, connection)
		session.quitReady = true
		if d.voiceSession == session {
			d.voiceSession, d.captureDocument, d.recorder = nil, nil, nil
			d.capture = captureIdle
		}
		if err != nil {
			slog.Error("falha ao finalizar a conversa antes de sair", "error", err)
		}
		d.finish()
	})
	return true
}

func (d *desktop) mergeVoiceSnapshot(session *voiceSession) {
	d.mergeVoiceConnectionSnapshot(session, session.connection)
}

func (d *desktop) mergeVoiceConnectionSnapshot(session *voiceSession, connection voiceConnection) {
	snapshotter, ok := connection.(interface{ Snapshot() []realtime.Response })
	if !ok || session.doc == nil || session.doc.voice == nil {
		return
	}
	doc := session.doc
	for _, response := range snapshotter.Snapshot() {
		if response.UserText == "" && response.Text == "" && len(response.PCM) == 0 {
			continue
		}
		var turn *voiceTurn
		for i := range doc.voice.History {
			if doc.voice.History[i].ID == response.CompletionID {
				turn = &doc.voice.History[i]
				break
			}
		}
		if turn == nil {
			doc.voice.History = append(doc.voice.History, voiceTurn{ID: response.CompletionID, Created: d.deps.now()})
			turn = &doc.voice.History[len(doc.voice.History)-1]
		}
		d.updateVoiceTranscript(doc, turn.UserText, response.UserText)
		turn.UserText, turn.Text = response.UserText, response.Text
		turn.Complete = response.Complete
		turn.Interrupted = response.Interrupted || !response.Complete
		turn.SampleRate = response.SampleRate
		if path := session.files.path(response.CompletionID); path != "" {
			turn.AudioPath = path
		}
	}
}

func (d *desktop) syncVoiceControls() {
	live := d.document != nil && d.document.voice != nil && d.document.voice.Mode == voiceModeLive
	if !live && d.voiceSession == nil {
		return
	}
	if d.capture == captureIdle && !d.busy {
		d.recordButton.SetText(uiText("●  Iniciar conversa"))
	} else if d.capture == captureRunning {
		d.recordButton.SetText(uiText("■  Encerrar conversa"))
	}
	d.recordButton.Refresh()
	d.discardButton.Hide()
	d.videoButton.Disable()
	d.newButton.Disable()
	d.importButton.Disable()
	d.retryButton.Disable()
	d.streamButton.setHint(uiText("Conversa ao vivo · modos de gravação"))
	if d.document.voice != nil && d.document.voice.panel != nil {
		setEnabled(d.document.voice.interrupt, d.voiceSession != nil && d.capture == captureRunning)
	}
}
