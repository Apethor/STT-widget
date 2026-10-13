//go:build windows && windowsmoke

package desktop

import (
	"context"
	"encoding/binary"
	"fmt"
	"sync"
	"time"

	"ai-talker/internal/config"
	"ai-talker/internal/pcmoutput"
	"ai-talker/internal/processing"
	"ai-talker/internal/realtime"

	"fyne.io/fyne/v2/widget"
)

type release012SmokeState struct {
	Language, PersistedLanguage, Mode, SettingsMode string
	LanguageSelect, AudioModeSelect, ModelSelect    smokeRectangle
	LanguageChoices, ModeChoices, ModelChoices      []string
	RecordText, TranscriptLabel, ProcessLabel       string
	ReadySuccess, ResponseVisible, ProcessVisible   bool
	ResponseText, VoiceID, ModelID, Output          string
	ResponsePanel, ResponseBack, ResponseNext       smokeRectangle
	ResponsePlay, ResponseStop, ResponseInterrupt   smokeRectangle
	ResponsePosition, ResponseVolume                smokeRectangle
	ResponseVoice, ResponseOutput, ResponseAuto     smokeRectangle
	ResponseHistory                                 []voiceTurn
	ResponseIndex, InputBytes, PlayerVolume         int
	PreferenceVolume, PersistedVolume               int
	PlayerPosition, PlayerDuration                  float64
	PlayerPaused, PlayerActive, Autoplay            bool
}

// These seams belong exclusively to the explicitly compiled native harness.
// Microphone PCM goes through the real relay; network and audible output are
// replaced while the canvas, dialogs, menus and persistence remain native.
func configureRelease012Smoke(deps *dependencies) {
	deps.newConversation = func(_ context.Context, _ config.Settings, _ realtime.Options,
		receive func(realtime.Event),
	) (voiceConnection, error) {
		return &release012VoiceConnection{receive: receive, done: make(chan struct{})}, nil
	}
	deps.newResponsePlayer = func(_ string, rate, volume int) responsePlayer {
		return &release012ResponsePlayer{rate: rate, volume: volume}
	}
}

func (d *desktop) prepareRelease012Smoke() {
	doc := d.documents[0]
	d.tabs.Select(doc.item)
	d.selectDocument(doc)
	previous := processing.Turn{Prompt: "Earlier prompt · do not translate.",
		Response: "Earlier result · do not translate."}
	current := processing.Turn{Prompt: doc.editor.Text, Response: doc.processEditor.Text}
	doc.processHistory = []processing.Turn{previous, current}
	doc.currentConversation, doc.nextPrompt, doc.promptCursor = 1, 2, 1
	options := doc.processOptions
	doc.promptSteps = []promptStep{
		{ID: 1, Conversation: 1, Prompt: previous.Prompt, Response: previous.Response, Sent: true, Processed: true, Options: &options},
		{ID: 2, Conversation: 1, Prompt: current.Prompt, Response: current.Response, Sent: true, Processed: true, Options: &options},
	}
	d.syncPromptNavigation(doc)
	_ = d.saveWorkspace()
}

func (d *desktop) windowRelease012State() *release012SmokeState {
	doc := d.document
	state := &release012SmokeState{
		Language: d.settings.InterfaceLocale(), Mode: d.documentAudioMode(doc),
		RecordText: d.recordState.Text, ReadySuccess: d.recordState.Importance == widget.SuccessImportance,
		TranscriptLabel: doc.transcriptName.Text,
		ProcessVisible:  doc.editors.Trailing == doc.processPanel,
		ModelID:         d.settings.Conversation.Model(), VoiceID: d.settings.Conversation.Voice(),
		Autoplay: d.settings.Conversation.AutoPlay(), PreferenceVolume: d.settings.Conversation.OutputVolume(),
	}
	for _, object := range smokeCanvasObjects(doc.processPanel) {
		if text, ok := object.(*themedText); ok && uiSource(text.Text) == "Processar texto" {
			state.ProcessLabel = text.Text
		}
	}
	if saved, err := config.Load(d.directory); err == nil {
		state.PersistedLanguage = saved.InterfaceLocale()
		state.PersistedVolume = saved.Conversation.OutputVolume()
	}
	if form := d.settingsForm; form != nil {
		state.LanguageSelect = d.smokeRectangle(form.interfaceLanguage)
		state.LanguageChoices = append([]string(nil), form.interfaceLanguage.Options...)
		if v := form.document.voice; v != nil && len(v.modes) > 0 {
			mode := v.modes[len(v.modes)-1]
			state.AudioModeSelect = d.smokeRectangle(mode)
			state.SettingsMode = uiSelected(mode)
			state.ModeChoices = append([]string(nil), mode.Options...)
		}
		if choice := form.conversationModel; choice != nil {
			state.ModelSelect = d.smokeRectangle(choice)
			state.ModelChoices = append([]string(nil), choice.Options...)
		}
	}
	if v := doc.voice; v != nil {
		state.ResponseHistory = append([]voiceTurn(nil), v.History...)
		state.ResponseIndex = v.index
		if v.panel != nil {
			state.ResponseVisible = doc.editors.Trailing == v.panel
			state.ResponsePanel = d.smokeRectangle(v.panel)
			state.ResponseText = v.text.Text
			state.ResponseBack, state.ResponseNext = d.smokeRectangle(v.back), d.smokeRectangle(v.next)
			state.ResponsePlay, state.ResponseStop = d.smokeRectangle(v.play), d.smokeRectangle(v.stop)
			state.ResponseInterrupt = d.smokeRectangle(v.interrupt)
			state.ResponsePosition, state.ResponseVolume = d.smokeRectangle(v.position), d.smokeRectangle(v.volume)
			state.ResponseVoice, state.ResponseOutput = d.smokeRectangle(v.voice), d.smokeRectangle(v.output)
			state.ResponseAuto = d.smokeRectangle(v.autoplay)
			state.Output = v.output.Selected
		}
	}
	if session := d.voiceSession; session != nil {
		if connection, ok := session.connection.(*release012VoiceConnection); ok {
			connection.mu.Lock()
			state.InputBytes = connection.inputBytes
			connection.mu.Unlock()
		}
	}
	if playback := d.responsePlayback; playback != nil && playback.player != nil {
		snapshot := playback.player.Snapshot()
		state.PlayerPosition, state.PlayerDuration, state.PlayerPaused = snapshot.Position, snapshot.Duration, snapshot.Paused
		state.PlayerActive = snapshot.Active
		if player, ok := playback.player.(*release012ResponsePlayer); ok {
			player.mu.Lock()
			state.PlayerVolume = player.volume
			player.mu.Unlock()
		}
	}
	return state
}

type release012VoiceConnection struct {
	mu         sync.Mutex
	inputBytes int
	emitted    bool
	receive    func(realtime.Event)
	done       chan struct{}
	once       sync.Once
}

func (c *release012VoiceConnection) SendAudio(ctx context.Context, pcm []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	c.inputBytes += len(pcm)
	emit := c.inputBytes >= realtime.InputSampleRate*2 && !c.emitted
	c.emitted = c.emitted || emit
	c.mu.Unlock()
	if emit {
		for index := 1; index <= 2; index++ {
			id := fmt.Sprintf("native-smoke-turn-%d", index)
			c.receive(realtime.Event{Kind: realtime.UserText, CompletionID: id,
				Text: fmt.Sprintf("User turn %d · Salvar unchanged.", index), Final: true})
			c.receive(realtime.Event{Kind: realtime.AssistantText, CompletionID: id,
				Text: fmt.Sprintf("Assistant turn %d · Grupo unchanged.", index), Final: true})
			samples := make([]byte, realtime.OutputSampleRate*2*10)
			for sample := range len(samples) / 2 {
				binary.LittleEndian.PutUint16(samples[sample*2:], uint16(sample%100))
			}
			c.receive(realtime.Event{Kind: realtime.AssistantAudio, CompletionID: id,
				PCM: samples, SampleRate: realtime.OutputSampleRate})
			c.receive(realtime.Event{Kind: realtime.ResponseEnd, CompletionID: id, Final: true})
		}
	}
	return nil
}

func (c *release012VoiceConnection) Close(context.Context) error { c.Cancel(); return nil }
func (c *release012VoiceConnection) Cancel()                     { c.once.Do(func() { close(c.done) }) }
func (c *release012VoiceConnection) Interrupt() {
	c.receive(realtime.Event{Kind: realtime.Interrupted, CompletionID: "native-smoke-turn-2"})
}
func (c *release012VoiceConnection) Done() <-chan struct{} { return c.done }
func (c *release012VoiceConnection) Err() error            { return nil }

type release012ResponsePlayer struct {
	mu       sync.Mutex
	rate     int
	volume   int
	duration float64
	position float64
	paused   bool
	active   bool
	finished bool
}

func (p *release012ResponsePlayer) Run(ctx context.Context) error {
	p.mu.Lock()
	p.active = true
	p.mu.Unlock()
	defer func() { p.mu.Lock(); p.active = false; p.mu.Unlock() }()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			p.mu.Lock()
			if !p.paused {
				p.position = min(p.duration, p.position+0.05)
			}
			done := p.finished && p.position >= p.duration
			p.mu.Unlock()
			if done {
				return nil
			}
		}
	}
}
func (p *release012ResponsePlayer) Finish() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.finished = true
}
func (p *release012ResponsePlayer) Append(pcm []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.duration += float64(len(pcm)) / float64(p.rate*2)
	return nil
}
func (p *release012ResponsePlayer) SetPaused(paused bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.paused = paused
}
func (p *release012ResponsePlayer) SetVolume(volume int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.volume = volume
}
func (p *release012ResponsePlayer) Seek(seconds float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.position = max(0, min(p.duration, seconds))
}
func (p *release012ResponsePlayer) Snapshot() pcmoutput.Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	return pcmoutput.Snapshot{Position: p.position, Duration: p.duration, Paused: p.paused, Active: p.active}
}
