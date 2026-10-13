package desktop

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"time"

	"ai-talker/internal/config"
	"ai-talker/internal/pcmoutput"
	"ai-talker/internal/realtime"
	"ai-talker/internal/recorder"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const (
	voiceModeRecording = "recording"
	voiceModeStream    = "stream"
	voiceModeLive      = "conversation"
)

type voiceTurn struct {
	ID          string    `json:"id"`
	UserText    string    `json:"user_text,omitempty"`
	Text        string    `json:"text,omitempty"`
	AudioPath   string    `json:"audio_path,omitempty"`
	SampleRate  int       `json:"sample_rate,omitempty"`
	Complete    bool      `json:"complete,omitempty"`
	Interrupted bool      `json:"interrupted,omitempty"`
	Missing     bool      `json:"audio_missing,omitempty"`
	Created     time.Time `json:"created"`
}

type voiceArchive struct {
	Mode    string      `json:"mode"`
	History []voiceTurn `json:"history,omitempty"`
}

type voiceDocument struct {
	Mode            string
	History         []voiceTurn
	index           int
	panel           *themedPanel
	text            *widget.Entry
	status          *widget.Label
	counter         *widget.Label
	play            *actionButton
	stop            *actionButton
	back            *actionButton
	next            *actionButton
	interrupt       *widget.Button
	position        *widget.Slider
	volume          *widget.Slider
	voice           *widget.Select
	model           *widget.Select
	output          *widget.Select
	autoplay        *widget.Check
	syncing         bool
	playbackFailed  string
	playbackError   string
	playbackStopped string
	modes           []*widget.Select
}

type responsePlayback struct {
	doc           *document
	id            string
	player        responsePlayer
	cancel        context.CancelFunc
	rate          int
	receivedBytes int
	loading       bool
	finishPending bool
	pending       []voicePCMBlock
}

type voicePCMBlock struct {
	start int
	pcm   []byte
}

type responsePlayer interface {
	Run(context.Context) error
	Append([]byte) error
	SetPaused(bool)
	SetVolume(int)
	Seek(float64)
	Snapshot() pcmoutput.Snapshot
}

func archiveVoice(v *voiceDocument) *voiceArchive {
	if v == nil {
		return nil
	}
	return &voiceArchive{Mode: v.Mode, History: slices.Clone(v.History)}
}

func (d *desktop) restoreVoice(doc *document, state *voiceArchive) {
	if state == nil {
		doc.voice = nil
		return
	}
	mode := state.Mode
	if mode != voiceModeRecording && mode != voiceModeStream && mode != voiceModeLive {
		mode = voiceModeRecording
	}
	doc.voice = &voiceDocument{Mode: mode, History: slices.Clone(state.History), index: max(0, len(state.History)-1)}
	if mode == voiceModeLive {
		d.ensureResponsePanel(doc)
	}
	d.arrangeEditorPanels(doc)
}

func audioModeName(mode string) string {
	switch mode {
	case voiceModeLive:
		return "Conversa ao vivo"
	case voiceModeStream:
		return "Transcrição Stream"
	default:
		return "Gravação e texto"
	}
}

func (d *desktop) documentAudioMode(doc *document) string {
	if doc != nil && doc.voice != nil {
		if doc.voice.Mode == voiceModeStream && d.settings.TranscriptionProvider() != config.ProviderBedrock {
			return voiceModeRecording
		}
		return doc.voice.Mode
	}
	if d.settings.Capture.Stream && d.settings.TranscriptionProvider() == config.ProviderBedrock {
		return voiceModeStream
	}
	return voiceModeRecording
}

func (d *desktop) chooseAudioMode() {
	if d.capture != captureIdle {
		return
	}
	items := make([]*fyne.MenuItem, 0, 3)
	for _, mode := range []string{voiceModeRecording, voiceModeStream, voiceModeLive} {
		item := fyne.NewMenuItem(uiText(audioModeName(mode)), d.guard(func() { d.selectAudioMode(d.document, mode) }))
		item.Checked = d.documentAudioMode(d.document) == mode
		item.Disabled = mode == voiceModeStream && d.settings.TranscriptionProvider() != config.ProviderBedrock ||
			mode == voiceModeLive && !realtime.SupportsRegion(d.settings.Region)
		items = append(items, item)
	}
	popup := widget.NewPopUpMenu(fyne.NewMenu(uiText("Modo de áudio"), items...), d.window.Canvas())
	popup.ShowAtRelativePosition(fyne.NewPos(0, d.streamButton.Size().Height), d.streamButton)
}

func (d *desktop) selectAudioMode(doc *document, mode string) {
	if d.capture != captureIdle || doc.processBusy || doc.busy {
		d.status.SetText(uiText("Encerre a sessão ou tarefa atual antes de mudar de modo."))
		return
	}
	if mode != voiceModeRecording && mode != voiceModeStream && mode != voiceModeLive {
		return
	}
	if mode == voiceModeStream && d.settings.TranscriptionProvider() != config.ProviderBedrock {
		d.status.SetText(uiText("Transcrição Stream disponível com AWS Cloud."))
		return
	}
	if mode == voiceModeLive && !realtime.SupportsRegion(d.settings.Region) {
		d.status.SetText(uiText("Conversa ao vivo indisponível nesta região AWS."))
		return
	}
	previous := archiveVoice(doc.voice)
	if doc.voice == nil {
		doc.voice = &voiceDocument{}
	}
	doc.voice.Mode = mode
	if mode == voiceModeLive {
		d.ensureResponsePanel(doc)
	}
	d.arrangeEditorPanels(doc)
	if err := d.saveWorkspace(); err != nil {
		d.restoreVoice(doc, previous)
		d.showDocumentError(doc, err.Error(), "")
		return
	}
	d.stopResponsePlayback()
	d.syncDocumentProvider(doc)
	d.syncControls()
	if d.settingsForm != nil && d.settingsForm.document == doc {
		d.syncConversationSettings()
	}
}

func (d *desktop) ensureResponsePanel(doc *document) {
	if doc.voice == nil {
		doc.voice = &voiceDocument{Mode: voiceModeLive}
	}
	v := doc.voice
	if v.panel != nil {
		return
	}
	v.text = widget.NewMultiLineEntry()
	v.text.Wrapping = fyne.TextWrapWord
	v.text.SetMinRowsVisible(3)
	v.text.SetPlaceHolder(uiText("A resposta da conversa aparece aqui."))
	v.text.Disable()
	v.status = widget.NewLabel(uiText("Inicie a conversa no gravador para falar com o assistente."))
	v.status.Wrapping = fyne.TextWrapWord
	v.counter = widget.NewLabel("")
	v.play = d.iconButton("Reproduzir ou pausar resposta", theme.MediaPlayIcon(), func() { d.playResponse(doc) })
	v.stop = d.iconButton("Parar resposta", theme.MediaStopIcon(), func() {
		if len(v.History) > 0 {
			v.playbackStopped = v.History[v.index].ID
		}
		if d.responsePlayback != nil && d.responsePlayback.doc == doc {
			d.stopResponsePlayback()
		}
		v.syncing = true
		v.position.SetValue(0)
		v.syncing = false
		v.play.SetIcon(theme.MediaPlayIcon())
	})
	v.back = d.iconButton("Resposta anterior", theme.NavigateBackIcon(), func() {
		if v.index > 0 {
			d.stopResponsePlayback()
			v.playbackFailed, v.playbackError = "", ""
			v.index--
			d.renderVoiceHistory(doc)
		}
	})
	v.next = d.iconButton("Próxima resposta", theme.NavigateNextIcon(), func() {
		if v.index+1 < len(v.History) {
			d.stopResponsePlayback()
			v.index++
			d.renderVoiceHistory(doc)
		}
	})
	v.interrupt = widget.NewButton(uiText("Interromper"), d.guard(func() {
		if d.voiceSession != nil && d.voiceSession.doc == doc {
			if len(v.History) != 0 {
				v.playbackStopped = v.History[len(v.History)-1].ID
			}
			d.stopResponsePlayback()
			if d.voiceSession.connection != nil {
				d.voiceSession.connection.Interrupt()
			}
			v.status.SetText(uiText("Resposta interrompida. Continue falando para prosseguir."))
		}
	}))
	v.interrupt.Disable()
	v.position = widget.NewSlider(0, 1)
	v.position.Step = 0.05
	v.position.OnChanged = func(seconds float64) {
		if !v.syncing && d.responsePlayback != nil && d.responsePlayback.doc == doc {
			d.responsePlayback.player.Seek(seconds)
		}
	}
	v.volume = widget.NewSlider(0, 200)
	v.volume.Step = 1
	v.volume.SetValue(float64(d.settings.Conversation.OutputVolume()))
	v.volume.OnChanged = func(value float64) {
		if d.responsePlayback != nil {
			d.responsePlayback.player.SetVolume(int(value))
		}
	}
	v.volume.OnChangeEnded = func(value float64) {
		updated := cloneSettings(d.settings)
		volume := int(value)
		updated.Conversation.Volume = &volume
		d.persistPreferences(updated, func(err error) {
			if err != nil {
				d.showError(err.Error(), "")
			}
		})
	}
	voiceIDs := make([]string, 0)
	for _, voice := range realtime.Voices(d.settings.Conversation.Model()) {
		voiceIDs = append(voiceIDs, voice.ID)
	}
	v.voice = widget.NewSelect(voiceIDs, nil)
	v.voice.SetSelected(d.settings.Conversation.Voice())
	v.voice.OnChanged = func(id string) {
		if v.syncing || id == d.settings.Conversation.Voice() {
			return
		}
		d.changeConversationPreference(doc, func(settings *config.Settings) { settings.Conversation.VoiceID = id }, true)
	}
	v.model = widget.NewSelect([]string{realtime.Nova25, realtime.Nova2}, nil)
	v.model.SetSelected(d.settings.Conversation.Model())
	v.model.OnChanged = func(id string) {
		if v.syncing || id == d.settings.Conversation.Model() {
			return
		}
		d.changeConversationPreference(doc, func(settings *config.Settings) { settings.Conversation.ModelID = id }, true)
	}
	v.output = widget.NewSelect([]string{uiText("Padrão do sistema")}, nil)
	v.output.SetSelected(uiText("Padrão do sistema"))
	refresh := d.iconButton("Atualizar saídas de resposta", theme.ViewRefreshIcon(), func() { d.refreshResponseOutputs(doc) })
	v.autoplay = widget.NewCheck(uiText("Reproduzir respostas automaticamente"), nil)
	v.autoplay.SetChecked(d.settings.Conversation.AutoPlay())
	v.autoplay.OnChanged = func(checked bool) {
		if v.syncing {
			return
		}
		d.changeConversationPreference(doc, func(settings *config.Settings) { settings.Conversation.Autoplay = &checked }, false)
		if !checked {
			d.stopResponsePlayback()
		}
	}
	top := container.NewVBox(
		container.NewBorder(nil, nil, label(uiText("Resposta"), 16, textColor),
			container.NewHBox(v.back, v.counter, v.next), nil),
		container.NewBorder(nil, nil, v.play, container.NewHBox(v.stop, v.interrupt), v.position),
		container.NewBorder(nil, nil, settingLabel(uiText("Volume")), nil, v.volume),
		container.NewBorder(nil, nil, settingLabel(uiText("Voz")), nil, v.voice),
		container.NewBorder(nil, nil, settingLabel(uiText("Saída")), refresh, v.output),
		v.autoplay,
	)
	v.panel = panel(container.NewBorder(top, v.status, nil, nil, v.text))
	d.renderVoiceHistory(doc)
	d.refreshResponseOutputs(doc)
}

func (d *desktop) changeConversationPreference(doc *document, change func(*config.Settings), requiresRestart bool) {
	apply := func() {
		updated := cloneSettings(d.settings)
		change(&updated)
		if !conversationVoiceAvailable(updated.Conversation.Model(), updated.Conversation.Voice()) {
			updated.Conversation.VoiceID = "carolina"
		}
		d.persistPreferences(updated, func(err error) {
			if err != nil {
				d.showError(err.Error(), "")
			}
			d.syncConversationSettings()
		})
	}
	if requiresRestart && d.voiceSession != nil {
		confirmation := dialog.NewConfirm(uiText("Alterar voz da conversa"),
			uiText("A nova voz ou modelo será usado ao iniciar a próxima sessão. A conversa atual continuará com a escolha anterior."),
			func(ok bool) {
				d.guard(func() {
					if ok {
						apply()
					}
					d.syncConversationSettings()
				})()
			}, d.window)
		confirmation.SetConfirmText(uiText("Usar na próxima sessão"))
		confirmation.SetDismissText(uiText("Cancelar"))
		confirmation.Show()
		return
	}
	apply()
}

func (d *desktop) refreshResponseOutputs(doc *document) {
	v := doc.voice
	if v == nil || v.output == nil || d.deps.playbackDevices == nil {
		return
	}
	var devices []recorder.Device
	d.operation(func() error {
		var err error
		devices, err = d.deps.playbackDevices()
		return err
	}, func(err error) {
		if doc.closed || doc.voice != v {
			return
		}
		if err != nil {
			v.status.SetText(err.Error())
			return
		}
		options := []string{"Padrão do sistema"}
		lookup := map[string]string{options[0]: ""}
		var deviceNames []string
		selected := options[0]
		for index, device := range devices {
			name := device.Name
			if _, exists := lookup[name]; exists {
				name += " (" + strconv.Itoa(index+1) + ")"
			}
			options = append(options, name)
			deviceNames = append(deviceNames, name)
			lookup[name] = device.EndpointID
			if device.EndpointID == d.settings.Conversation.OutputEndpoint {
				selected = name
			}
		}
		if d.settings.Conversation.OutputEndpoint != "" && selected == options[0] {
			selected = "Saída indisponível"
			options = append(options, selected)
		}
		v.syncing = true
		preserveUIChoices(v.output, deviceNames)
		v.output.Options = options
		setUISelected(v.output, selected)
		v.syncing = false
		v.output.OnChanged = func(name string) {
			if v.syncing {
				return
			}
			endpoint, ok := lookup[uiSelectedValue(v.output, name)]
			if !ok {
				return
			}
			d.stopResponsePlayback()
			v.playbackFailed, v.playbackError = "", ""
			d.changeConversationPreference(doc, func(settings *config.Settings) { settings.Conversation.OutputEndpoint = endpoint }, false)
		}
	})
}

func (d *desktop) renderVoiceHistory(doc *document) {
	v := doc.voice
	if v == nil || v.panel == nil {
		return
	}
	v.syncing = true
	defer func() { v.syncing = false }()
	v.index = max(0, min(v.index, len(v.History)-1))
	v.counter.SetText(fmt.Sprintf("%d / %d", min(v.index+1, len(v.History)), len(v.History)))
	setEnabled(v.back, v.index > 0)
	setEnabled(v.next, v.index+1 < len(v.History))
	if len(v.History) == 0 {
		v.text.SetText("")
		v.play.Disable()
		return
	}
	turn := v.History[v.index]
	v.text.SetText(turn.Text)
	notice := ""
	if turn.Interrupted || !turn.Complete {
		notice = uiText("Resposta incompleta · conteúdo recebido preservado.")
	}
	if turn.Missing {
		notice = uiText("Áudio indisponível · texto preservado.")
	}
	if v.playbackFailed == turn.ID && v.playbackError != "" {
		notice = v.playbackError
	}
	v.status.SetText(notice)
	setEnabled(v.play, turn.AudioPath != "" && !turn.Missing)
	if d.responsePlayback != nil && d.responsePlayback.doc == doc && d.responsePlayback.id == turn.ID {
		snapshot := d.responsePlayback.player.Snapshot()
		v.position.Max = max(1, snapshot.Duration)
		v.position.SetValue(snapshot.Position)
		v.play.SetIcon(theme.MediaPlayIcon())
		if !snapshot.Paused && snapshot.Position < snapshot.Duration {
			v.play.SetIcon(theme.MediaPauseIcon())
		}
	}
}

func (d *desktop) appendResponsePlayback(doc *document, turn *voiceTurn, pcm []byte, start int) {
	if doc.voice.playbackFailed == turn.ID || doc.voice.playbackStopped == turn.ID {
		return
	}
	if d.responsePlayback == nil || d.responsePlayback.doc != doc || d.responsePlayback.id != turn.ID {
		d.stopResponsePlayback()
		d.startResponsePlayer(doc, turn.ID, turn.SampleRate)
		d.responsePlayback.receivedBytes = start
	}
	if d.responsePlayback != nil {
		if err := appendVoicePCM(d.responsePlayback, voicePCMBlock{start: start, pcm: pcm}); err != nil {
			doc.voice.status.SetText(err.Error())
		}
	}
}

func appendVoicePCM(playback *responsePlayback, block voicePCMBlock) error {
	if playback.loading {
		playback.pending = append(playback.pending, block)
		return nil
	}
	if block.start < 0 || block.start > playback.receivedBytes {
		return io.ErrUnexpectedEOF
	}
	skip := min(len(block.pcm), playback.receivedBytes-block.start)
	pcm := block.pcm[skip:]
	if len(pcm) == 0 {
		return nil
	}
	if err := playback.player.Append(pcm); err != nil {
		return err
	}
	playback.receivedBytes += len(pcm)
	return nil
}

func (d *desktop) finishResponsePlayer(playback *responsePlayback) {
	if playback.loading {
		playback.finishPending = true
		return
	}
	if player, ok := playback.player.(interface{ Finish() }); ok {
		player.Finish()
	}
}

func (d *desktop) startResponsePlayer(doc *document, id string, rate int) {
	ctx, cancel := context.WithCancel(d.ctx)
	newPlayer := d.deps.newResponsePlayer
	if newPlayer == nil {
		newPlayer = func(endpoint string, sampleRate, volume int) responsePlayer {
			return pcmoutput.New(endpoint, sampleRate, volume)
		}
	}
	playback := &responsePlayback{doc: doc, id: id, cancel: cancel, rate: rate,
		player: newPlayer(d.settings.Conversation.OutputEndpoint, rate, d.settings.Conversation.OutputVolume())}
	d.responsePlayback = playback
	d.workers.Go(func() {
		err := playback.player.Run(ctx)
		d.post(func() {
			if d.responsePlayback != playback {
				return
			}
			d.responsePlayback = nil
			doc.voice.syncing = true
			if err == nil {
				doc.voice.position.SetValue(doc.voice.position.Max)
			}
			doc.voice.syncing = false
			doc.voice.play.SetIcon(theme.MediaPlayIcon())
			if err != nil && !errors.Is(err, context.Canceled) {
				doc.voice.playbackFailed, doc.voice.playbackError = id, err.Error()
				doc.voice.status.SetText(doc.voice.playbackError)
			}
		})
	})
}

func (d *desktop) playResponse(doc *document) {
	v := doc.voice
	if v == nil || len(v.History) == 0 {
		return
	}
	turn := v.History[v.index]
	v.playbackFailed, v.playbackError, v.playbackStopped = "", "", ""
	if d.responsePlayback != nil && d.responsePlayback.doc == doc && d.responsePlayback.id == turn.ID {
		snapshot := d.responsePlayback.player.Snapshot()
		if snapshot.Position >= snapshot.Duration {
			d.responsePlayback.player.Seek(0)
		}
		d.responsePlayback.player.SetPaused(!snapshot.Paused && snapshot.Position < snapshot.Duration)
		return
	}
	if turn.AudioPath == "" || turn.Missing {
		return
	}
	d.stopResponsePlayback()
	rateGuess := turn.SampleRate
	if rateGuess == 0 {
		rateGuess = realtime.OutputSampleRate
	}
	d.startResponsePlayer(doc, turn.ID, rateGuess)
	playback := d.responsePlayback
	playback.loading = true
	session := d.voiceSession
	liveFile := false
	if session != nil && session.doc == doc {
		_, liveFile = session.uiAudioBytes[turn.ID]
	}
	var pcm []byte
	var rate int
	d.operation(func() error {
		var err error
		if liveFile {
			pcm, rate, err = session.files.read(turn.ID)
		} else {
			pcm, rate, err = readVoiceAudio(turn.AudioPath)
		}
		return err
	}, func(err error) {
		if doc.closed || doc.voice != v || d.responsePlayback != playback {
			return
		}
		if err != nil {
			d.stopResponsePlayback()
			v.status.SetText(err.Error())
			return
		}
		if playback.rate != rate {
			pending, finishPending := playback.pending, playback.finishPending
			d.stopResponsePlayback()
			d.startResponsePlayer(doc, turn.ID, rate)
			playback = d.responsePlayback
			playback.pending, playback.finishPending = pending, finishPending
		}
		playback.loading = false
		if err := appendVoicePCM(playback, voicePCMBlock{pcm: pcm}); err != nil {
			v.status.SetText(err.Error())
			d.stopResponsePlayback()
			return
		}
		for _, block := range playback.pending {
			if err := appendVoicePCM(playback, block); err != nil {
				v.status.SetText(err.Error())
				d.stopResponsePlayback()
				return
			}
		}
		playback.pending = nil
		growing := liveFile && d.voiceSession == session && !session.stopping
		for _, latest := range v.History {
			if latest.ID == turn.ID {
				growing = growing && !latest.Complete && !latest.Interrupted
				break
			}
		}
		if playback.finishPending || !growing {
			d.finishResponsePlayer(playback)
		}
	})
}

func (d *desktop) stopResponsePlayback() {
	if playback := d.responsePlayback; playback != nil {
		d.responsePlayback = nil
		playback.cancel()
	}
}

func (d *desktop) tickVoicePlayback() {
	if playback := d.responsePlayback; playback != nil && !playback.doc.closed {
		d.renderVoiceHistory(playback.doc)
	}
}

func (d *desktop) syncConversationSettings() {
	for _, doc := range d.documents {
		v := doc.voice
		if v == nil {
			continue
		}
		for _, mode := range v.modes {
			mode.OnChanged = nil
			setUISelected(mode, audioModeName(d.documentAudioMode(doc)))
			mode.OnChanged = d.conversationModeChanged(doc, mode)
		}
		if v.panel == nil {
			continue
		}
		v.syncing = true
		v.voice.Options = conversationVoiceIDs(d.settings.Conversation.Model())
		v.voice.SetSelected(d.settings.Conversation.Voice())
		v.model.SetSelected(d.settings.Conversation.Model())
		v.volume.SetValue(float64(d.settings.Conversation.OutputVolume()))
		v.autoplay.SetChecked(d.settings.Conversation.AutoPlay())
		v.syncing = false
	}
}

func conversationVoiceIDs(modelID string) []string {
	voices := realtime.Voices(modelID)
	ids := make([]string, 0, len(voices))
	for _, voice := range voices {
		ids = append(ids, voice.ID)
	}
	return ids
}

func conversationVoiceAvailable(modelID, voiceID string) bool {
	return slices.Contains(conversationVoiceIDs(modelID), voiceID)
}

func (d *desktop) buildConversationSettings(form *settingsForm) (fyne.CanvasObject, fyne.CanvasObject) {
	modes := []string{audioModeName(voiceModeRecording), audioModeName(voiceModeStream), audioModeName(voiceModeLive)}
	mode := widget.NewSelect(modes, nil)
	mode.SetSelected(audioModeName(d.documentAudioMode(form.document)))
	if form.document.voice == nil {
		form.document.voice = &voiceDocument{Mode: d.documentAudioMode(form.document)}
	}
	for _, previous := range form.document.voice.modes {
		previous.OnChanged = nil
	}
	form.document.voice.modes = []*widget.Select{mode}
	mode.OnChanged = d.conversationModeChanged(form.document, mode)
	model := widget.NewSelect([]string{realtime.Nova25, realtime.Nova2}, nil)
	form.conversationModel = model
	model.SetSelected(d.settings.Conversation.Model())
	model.OnChanged = func(id string) {
		d.changeConversationPreference(form.document, func(settings *config.Settings) { settings.Conversation.ModelID = id }, true)
	}
	notice := widget.NewLabel(uiText("Conversa ao vivo usa o microfone e a conexão AWS. Nova 2.5 é o padrão; OpenRouter não oferece um protocolo contínuo confirmado nesta entrega. O idioma da interface não altera o idioma da conversa."))
	notice.Wrapping = fyne.TextWrapWord
	modeControl := container.NewBorder(nil, nil, settingLabel(uiText("Modo de áudio")), nil, mode)
	details := container.NewVBox(model, notice)
	return modeControl, details
}

func (d *desktop) conversationModeChanged(doc *document, mode *widget.Select) func(string) {
	return func(name string) {
		for _, candidate := range []string{voiceModeRecording, voiceModeStream, voiceModeLive} {
			if uiSelectedValue(mode, name) == audioModeName(candidate) {
				d.selectAudioMode(doc, candidate)
				mode.OnChanged = nil
				setUISelected(mode, audioModeName(d.documentAudioMode(doc)))
				mode.OnChanged = d.conversationModeChanged(doc, mode)
				return
			}
		}
	}
}

func (d *desktop) markVoiceAudioRemoved(item cleanupItem) {
	mark := func(history []voiceTurn) {
		for index := range history {
			turn := &history[index]
			if sameAudio(turn.AudioPath, item.Source) || slices.ContainsFunc(item.Paths, func(path string) bool { return sameAudio(path, turn.AudioPath) }) {
				turn.Missing = true
			}
		}
	}
	for _, doc := range d.cleanupDocuments() {
		if doc.voice == nil {
			continue
		}
		mark(doc.voice.History)
		d.renderVoiceHistory(doc)
	}
	for _, entry := range d.closedTabs {
		if entry.combined == nil {
			continue
		}
		for index := range entry.combined.states {
			if voice := entry.combined.states[index].Voice; voice != nil {
				mark(voice.History)
			}
		}
	}
}

func (d *desktop) voiceAudioInUse(item cleanupItem) string {
	if p := d.responsePlayback; p != nil && p.doc.voice != nil {
		for _, turn := range p.doc.voice.History {
			if turn.ID == p.id && sameAudio(turn.AudioPath, item.Source) {
				return uiText("Em uso no player de resposta; pare a reprodução para limpar.")
			}
		}
	}
	if s := d.voiceSession; s != nil {
		if s.files.containsPath(item.Source) || slices.ContainsFunc(item.Paths, s.files.containsPath) {
			return uiText("Em uso na conversa ao vivo; encerre a sessão para limpar.")
		}
		for _, turn := range s.doc.voice.History {
			if sameAudio(turn.AudioPath, item.Source) {
				return uiText("Em uso na conversa ao vivo; encerre a sessão para limpar.")
			}
		}
	}
	return ""
}
