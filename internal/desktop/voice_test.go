package desktop

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"ai-talker/internal/config"
	"ai-talker/internal/pcmoutput"
	"ai-talker/internal/processing"
	"ai-talker/internal/realtime"
	"ai-talker/internal/recorder"
	"ai-talker/internal/store"
	"ai-talker/internal/transcript"
)

func TestVoiceResponseFilePreservesPartialPCMAndRejectsBrokenFiles(t *testing.T) {
	file, err := newVoiceAudioFile(t.TempDir(), 24000)
	if err != nil {
		t.Fatal(err)
	}
	first, second := []byte{1, 0, 2, 0}, []byte{3, 0}
	if err := file.write(first); err != nil {
		t.Fatal(err)
	}
	partial, rate, err := readVoiceAudio(file.path)
	if err != nil || rate != 24000 || !bytes.Equal(partial, first) {
		t.Fatalf("partial lost: %x/%d/%v", partial, rate, err)
	}
	if err := file.write(second); err != nil {
		t.Fatal(err)
	}
	if err := file.file.Close(); err != nil {
		t.Fatal(err)
	}
	all, _, err := readVoiceAudio(file.path)
	if err != nil || !bytes.Equal(all, append(first, second...)) {
		t.Fatal("final audio changed", err)
	}
	broken := filepath.Join(t.TempDir(), "broken.wav")
	if err := os.WriteFile(broken, []byte("not wave"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readVoiceAudio(broken); err == nil {
		t.Fatal("broken response accepted")
	}
}

func TestVoiceModePreservesProcessingAndHistoryAcrossWorkspaceReopen(t *testing.T) {
	h := newHarness(t, nil, nil)
	doc := h.d.document
	doc.editor.SetText("Entrada preservada")
	doc.processEditor.SetText("Resultado manual preservado")
	h.d.selectAudioMode(doc, voiceModeLive)
	if doc.editors.Trailing != doc.voice.panel || doc.processEditor.Text != "Resultado manual preservado" {
		t.Fatal("response did not replace the panel safely")
	}
	doc.voice.History = []voiceTurn{{ID: "1", UserText: "Olá", Text: "Resposta", Complete: true}}
	h.d.selectAudioMode(doc, voiceModeRecording)
	if doc.editors.Trailing != doc.processPanel || len(doc.voice.History) != 1 {
		t.Fatal("mode lost data")
	}
	h.d.selectAudioMode(doc, voiceModeLive)
	if err := h.d.saveWorkspace(); err != nil {
		t.Fatal(err)
	}
	var snapshot workspaceSnapshot
	if err := store.ReadJSON(filepath.Join(h.d.directory, "workspace-go-v1.json"), &snapshot); err != nil {
		t.Fatal(err)
	}
	reopened := newHarness(t, nil, nil)
	reopened.d.directory = h.d.directory
	if !reopened.d.loadWorkspace() || reopened.d.documentAudioMode(reopened.d.document) != voiceModeLive ||
		reopened.d.processEditor.Text != "Resultado manual preservado" || reopened.d.voice.History[0].Text != "Resposta" {
		t.Fatal("reopened state lost")
	}
	newDoc := h.d.addDocument()
	if h.d.documentAudioMode(newDoc) != voiceModeRecording {
		t.Fatal("new tab inherited live mode")
	}
}

type testVoiceConnection struct {
	mu        sync.Mutex
	done      chan struct{}
	closeOnce sync.Once
	sent      []byte
}

func (c *testVoiceConnection) SendAudio(ctx context.Context, pcm []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	c.sent = append(c.sent, pcm...)
	c.mu.Unlock()
	return nil
}
func (c *testVoiceConnection) Close(context.Context) error { c.Cancel(); return nil }
func (c *testVoiceConnection) Cancel()                     { c.closeOnce.Do(func() { close(c.done) }) }
func (*testVoiceConnection) Interrupt()                    {}
func (c *testVoiceConnection) Done() <-chan struct{}       { return c.done }
func (*testVoiceConnection) Err() error                    { return nil }

func TestVoiceSessionUsesMicrophoneAndNeverStartsTranscriptionOrProcessing(t *testing.T) {
	var callback func(realtime.Event)
	connection := &testVoiceConnection{done: make(chan struct{})}
	var captured config.Settings
	var r *fakeRecording
	h := newHarness(t, runnerFunc(func(context.Context, string, config.Settings, transcript.Request) (transcript.Result, error) {
		t.Error("voice session started transcription")
		return transcript.Result{}, errors.New("unexpected")
	}), func(deps *dependencies) {
		deps.newConversation = func(_ context.Context, settings config.Settings, options realtime.Options, receive func(realtime.Event)) (voiceConnection, error) {
			captured = settings
			callback = receive
			if options.ModelID != realtime.Nova25 || options.VoiceID != "carolina" {
				t.Error("voice defaults lost")
			}
			return connection, nil
		}
		deps.newConfiguredRecorder = func(settings config.Settings, folder string) recording {
			if settings.Capture.SourceMode() != config.CaptureMicrophone || settings.Capture.Stream {
				t.Error("voice source is not microphone")
			}
			path := filepath.Join(folder, "synthetic.wav")
			_ = os.MkdirAll(folder, 0o700)
			_ = os.WriteFile(path, append(voiceWAVHeader(16000, 4), 1, 0, 2, 0), 0o600)
			r = &fakeRecording{state: recorder.Snapshot{Path: path, SampleRate: 16000, WrittenSamples: 2}}
			return r
		}
	})
	autoplay := false
	h.d.settings.Conversation.Autoplay = &autoplay
	h.d.settings.Capture.Mode = config.CaptureTeam
	h.d.selectAudioMode(h.d.document, voiceModeLive)
	h.d.recordAction()
	h.await(func() bool { return h.d.capture == captureRunning })
	if captured.Capture.SourceMode() != config.CaptureMicrophone || h.d.settings.Capture.SourceMode() != config.CaptureTeam {
		t.Fatal("conversation altered conventional capture")
	}
	callback(realtime.Event{Kind: realtime.UserText, CompletionID: "turn", Text: "Olá", Final: true})
	callback(realtime.Event{Kind: realtime.AssistantText, CompletionID: "turn", Text: "Bom dia", Final: true})
	callback(realtime.Event{Kind: realtime.AssistantAudio, CompletionID: "turn", PCM: []byte{1, 0, 2, 0}, SampleRate: 24000})
	callback(realtime.Event{Kind: realtime.ResponseEnd, CompletionID: "turn", Final: true})
	h.drain()
	if h.d.editor.Text != "Olá" || h.d.voice.History[0].Text != "Bom dia" || !h.d.voice.History[0].Complete {
		t.Fatal("response association lost")
	}
	h.d.stopVoiceSession()
	h.await(func() bool { return h.d.capture == captureIdle })
	if pcm, rate, err := readVoiceAudio(h.d.voice.History[0].AudioPath); err != nil || rate != 24000 || len(pcm) != 4 {
		t.Fatal("response was not persisted", err)
	}
	h.d.markVoiceAudioRemoved(cleanupItem{Source: h.d.voice.History[0].AudioPath})
	if !h.d.voice.History[0].Missing || h.d.voice.History[0].Text != "Bom dia" {
		t.Fatal("cleanup lost response text")
	}
}

func TestVoiceFilesStayClosedAfterLateResponseCallback(t *testing.T) {
	files := responseFiles{folder: t.TempDir()}
	first, err := files.append("turn", []byte{1, 0}, 24000)
	if err != nil {
		t.Fatal(err)
	}
	if err := files.close(); err != nil {
		t.Fatal(err)
	}
	defer files.close()
	if path, err := files.append("late-turn", []byte{2, 0}, 24000); err == nil {
		t.Fatalf("closed response files reopened at %s", path)
	}
	entries, err := os.ReadDir(files.folder)
	if err != nil || len(entries) != 1 || filepath.Join(files.folder, entries[0].Name()) != first {
		t.Fatalf("late callback created files: %v / %v", entries, err)
	}
}

func TestVoiceInterruptionTargetsItsTurnAndUserTextDoesNotDuplicate(t *testing.T) {
	h := newHarness(t, nil, nil)
	doc := h.d.document
	h.d.selectAudioMode(doc, voiceModeLive)
	session := &voiceSession{doc: doc}
	h.d.voiceSession = session
	h.d.receiveVoiceEvent(session, realtime.Event{Kind: realtime.UserText, CompletionID: "first", Text: "Hello", Final: true}, "")
	h.d.receiveVoiceEvent(session, realtime.Event{Kind: realtime.UserText, CompletionID: "first", Text: "Hello world", Final: true}, "")
	if doc.editor.Text != "Hello world" {
		t.Fatalf("cumulative USER text duplicated: %q", doc.editor.Text)
	}
	h.d.receiveVoiceEvent(session, realtime.Event{Kind: realtime.UserText, CompletionID: "second", Text: "Next question", Final: true}, "")
	h.d.receiveVoiceEvent(session, realtime.Event{Kind: realtime.Interrupted, CompletionID: "first"}, "")
	if !doc.voice.History[0].Interrupted || doc.voice.History[1].Interrupted {
		t.Fatalf("interrupted wrong turn: %+v", doc.voice.History)
	}
	h.d.voiceSession = nil
}

func TestVoiceRelayKeepsPausedTransportAndPreservesFinal48kSamples(t *testing.T) {
	path := filepath.Join(t.TempDir(), "microphone.wav")
	input := []byte{3, 0, 6, 0, 9, 0, 12, 0}
	if err := os.WriteFile(path, append(voiceWAVHeader(48000, uint32(len(input))), input...), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &fakeRecording{state: recorder.Snapshot{Path: path, SampleRate: 48000, WrittenSamples: 4}}
	var received []byte
	if err := relayVoiceCapture(t.Context(), r, func(_ context.Context, pcm []byte) error {
		received = append(received, pcm...)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(received, []byte{6, 0, 12, 0}) {
		t.Fatalf("48 kHz tail lost: %v", received)
	}
	r.state.Active, r.state.Paused, r.state.WrittenSamples = true, true, 0
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	count := 0
	if err := relayVoiceCapture(ctx, r, func(_ context.Context, pcm []byte) error {
		if len(pcm) != 640 || !bytes.Equal(pcm, make([]byte, 640)) {
			t.Errorf("paused transport is not 20 ms silence: %d bytes", len(pcm))
		}
		count++
		if count == 2 {
			cancel()
		}
		return nil
	}); !errors.Is(err, context.Canceled) || count != 2 {
		t.Fatalf("paused transport stopped: count=%d err=%v", count, err)
	}
	if info, err := os.Stat(path); err != nil || info.Size() != int64(44+len(input)) {
		t.Fatalf("transport silence modified microphone file: %v / %v", info, err)
	}
}

func TestVoiceRelayBacklogMaintainsCadenceAndRejectsRewoundCapture(t *testing.T) {
	path := filepath.Join(t.TempDir(), "microphone.wav")
	pcm := make([]byte, 640*3)
	if err := os.WriteFile(path, append(voiceWAVHeader(16000, uint32(len(pcm))), pcm...), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &fakeRecording{state: recorder.Snapshot{Path: path, SampleRate: 16000, WrittenSamples: uint64(len(pcm) / 2)}}
	var timestamps []time.Time
	if err := relayVoiceCapture(t.Context(), r, func(context.Context, []byte) error {
		timestamps = append(timestamps, time.Now())
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(timestamps) != 3 || timestamps[2].Sub(timestamps[0]) < 30*time.Millisecond {
		t.Fatalf("buffered microphone sent in a burst: %v", timestamps)
	}
	r.state.Active = true
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	err := relayVoiceCapture(ctx, r, func(context.Context, []byte) error {
		r.mu.Lock()
		r.state.WrittenSamples = 0
		r.mu.Unlock()
		return nil
	})
	if err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("rewound capture silently underflowed: %v", err)
	}
}

type snapshotVoiceConnection struct {
	*testVoiceConnection
	responses []realtime.Response
}

func (c snapshotVoiceConnection) Snapshot() []realtime.Response { return c.responses }

func TestVoiceShutdownPreservesProviderEventsWaitingForUI(t *testing.T) {
	h := newHarness(t, nil, nil)
	doc := h.d.document
	h.d.selectAudioMode(doc, voiceModeLive)
	session := &voiceSession{doc: doc, files: responseFiles{folder: t.TempDir()}}
	path, err := session.files.append("pending", []byte{1, 0, 2, 0}, 24000)
	if err != nil {
		t.Fatal(err)
	}
	defer session.files.close()
	session.connection = snapshotVoiceConnection{
		testVoiceConnection: &testVoiceConnection{done: make(chan struct{})},
		responses:           []realtime.Response{{CompletionID: "pending", UserText: "Question", Text: "Partial reply", PCM: []byte{1, 0, 2, 0}, SampleRate: 24000}},
	}
	h.d.voiceSession = session
	h.d.preserveVoiceSnapshot()
	if len(doc.voice.History) != 1 || doc.voice.History[0].AudioPath != path ||
		!doc.voice.History[0].Interrupted || doc.voice.History[0].Text != "Partial reply" || doc.editor.Text != "Question" {
		t.Fatalf("shutdown lost queued events: %+v", doc.voice.History)
	}
	h.d.preserveVoiceSnapshot()
	if doc.editor.Text != "Question" || len(doc.voice.History) != 1 {
		t.Fatal("repeated snapshot duplicated history")
	}
	h.d.voiceSession = nil
}

type finalVoiceConnection struct {
	*testVoiceConnection
	begin     sync.Once
	release   chan struct{}
	files     *responseFiles
	responses []realtime.Response
}

func (c *finalVoiceConnection) Cancel() {
	c.begin.Do(func() {
		go func() {
			<-c.release
			pcm := []byte{1, 0, 2, 0, 3, 0}
			_, _ = c.files.append("last", pcm, 24000)
			c.mu.Lock()
			c.responses = []realtime.Response{{CompletionID: "last", UserText: "Last question", Text: "Last partial reply", PCM: pcm, SampleRate: 24000}}
			c.mu.Unlock()
			c.testVoiceConnection.Cancel()
		}()
	})
}

func (c *finalVoiceConnection) Snapshot() []realtime.Response {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.responses)
}

func TestVoiceQuitWaitsOffUIAndSavesTheFinalReceivedBlock(t *testing.T) {
	h := newHarness(t, nil, nil)
	doc := h.d.document
	h.d.selectAudioMode(doc, voiceModeLive)
	ctx, cancel := context.WithCancel(h.d.ctx)
	session := &voiceSession{doc: doc, ctx: ctx, cancel: cancel, files: responseFiles{folder: t.TempDir()}}
	connection := &finalVoiceConnection{
		testVoiceConnection: &testVoiceConnection{done: make(chan struct{})},
		release:             make(chan struct{}), files: &session.files,
	}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(connection.release) }) }
	defer release()
	session.connection = connection
	h.d.voiceSession = session
	h.d.capture = captureRunning
	if !h.d.finishVoiceBeforeQuit() || h.d.closing {
		t.Fatal("quit did not defer saving until final callbacks finished")
	}
	if !h.d.finishVoiceBeforeQuit() {
		t.Fatal("a repeated quit escaped the pending finalization")
	}
	release()
	h.await(func() bool { return h.d.closing })
	var workspace workspaceSnapshot
	if err := store.ReadJSON(filepath.Join(h.d.directory, "workspace-go-v1.json"), &workspace); err != nil {
		t.Fatal(err)
	}
	if len(doc.voice.History) != 1 || doc.voice.History[0].Text != "Last partial reply" ||
		doc.editor.Text != "Last question" || !doc.voice.History[0].Interrupted {
		t.Fatalf("quit lost final response: %+v", doc.voice.History)
	}
	pcm, rate, err := readVoiceAudio(doc.voice.History[0].AudioPath)
	if err != nil || rate != 24000 || !bytes.Equal(pcm, []byte{1, 0, 2, 0, 3, 0}) {
		t.Fatalf("quit lost final PCM: %v/%d/%v", pcm, rate, err)
	}
}

func TestVoiceQueuedAudioCannotRestartPlaybackAfterManualInterruption(t *testing.T) {
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.newResponsePlayer = func(string, int, int) responsePlayer {
			t.Fatal("queued PCM restarted an interrupted response")
			return nil
		}
	})
	doc := h.d.document
	h.d.selectAudioMode(doc, voiceModeLive)
	session := &voiceSession{doc: doc, connection: &testVoiceConnection{done: make(chan struct{})}}
	h.d.voiceSession = session
	h.d.receiveVoiceEvent(session, realtime.Event{Kind: realtime.UserText, CompletionID: "current", Text: "Question", Final: true}, "")
	doc.voice.interrupt.OnTapped()
	h.d.receiveVoiceEvent(session, realtime.Event{Kind: realtime.AssistantAudio, CompletionID: "current", PCM: []byte{1, 0}, SampleRate: 24000}, "partial.wav")
	if doc.voice.History[0].AudioPath != "partial.wav" {
		t.Fatal("interruption discarded retained audio")
	}
	h.d.voiceSession = nil
}

type manualVoicePlayer struct {
	mu       sync.Mutex
	pcm      []byte
	finished bool
	done     chan struct{}
}

func (p *manualVoicePlayer) Run(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.done:
		return nil
	}
}

func (p *manualVoicePlayer) Append(pcm []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.finished {
		return errors.New("completed manual response rejected more PCM")
	}
	p.pcm = append(p.pcm, pcm...)
	return nil
}

func (*manualVoicePlayer) SetPaused(bool) {}
func (*manualVoicePlayer) SetVolume(int)  {}
func (*manualVoicePlayer) Seek(float64)   {}
func (p *manualVoicePlayer) Snapshot() pcmoutput.Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	return pcmoutput.Snapshot{Duration: float64(len(p.pcm)) / 48000}
}
func (p *manualVoicePlayer) Finish() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.finished {
		p.finished = true
		close(p.done)
	}
}

func TestVoiceManualGrowingPlaybackIncludesQueuedAndNewPCMWithAutoplayOff(t *testing.T) {
	for _, scenario := range []struct{ endWhileLoading, final bool }{{false, true}, {true, true}, {true, false}} {
		endWhileLoading, final := scenario.endWhileLoading, scenario.final
		t.Run(fmt.Sprintf("end_while_loading_%t_final_%t", endWhileLoading, final), func(t *testing.T) {
			player := &manualVoicePlayer{done: make(chan struct{})}
			h := newHarness(t, nil, func(deps *dependencies) {
				deps.newResponsePlayer = func(string, int, int) responsePlayer { return player }
			})
			autoplay := false
			h.d.settings.Conversation.Autoplay = &autoplay
			doc := h.d.document
			h.d.selectAudioMode(doc, voiceModeLive)
			h.drain()
			session := &voiceSession{doc: doc, files: responseFiles{folder: t.TempDir()}}
			defer session.files.close()
			h.d.voiceSession = session
			emit := func(pcm []byte, dispatch bool) (string, int) {
				t.Helper()
				path, end, err := session.files.appendBlock("growing", pcm, 24000)
				if err != nil {
					t.Fatal(err)
				}
				if dispatch {
					h.d.receiveVoiceEvent(session, realtime.Event{Kind: realtime.AssistantAudio, CompletionID: "growing", PCM: pcm, SampleRate: 24000}, path, end)
				}
				return path, end
			}
			emit([]byte{1, 0}, true)
			// This block is already on disk but its UI callback is queued.
			path, end := emit([]byte{2, 0}, false)
			session.files.mu.Lock()
			h.d.playResponse(doc)
			h.d.receiveVoiceEvent(session, realtime.Event{Kind: realtime.AssistantAudio, CompletionID: "growing", PCM: []byte{2, 0}, SampleRate: 24000}, path, end)
			session.files.mu.Unlock()
			// Hold the completed file read before it seeds playback on the UI.
			loaded := waitValue(t, h.queue)
			emit([]byte{3, 0}, true)
			if endWhileLoading {
				h.d.receiveVoiceEvent(session, realtime.Event{Kind: realtime.ResponseEnd, CompletionID: "growing", Final: final}, "")
			}
			player.mu.Lock()
			finishedEarly := player.finished
			player.mu.Unlock()
			if finishedEarly {
				t.Fatal("EOS closed playback before its file prefix loaded")
			}
			loaded()
			expected := []byte{1, 0, 2, 0, 3, 0}
			if !endWhileLoading {
				emit([]byte{4, 0}, true)
				expected = append(expected, 4, 0)
			}
			player.mu.Lock()
			actual, finished := bytes.Clone(player.pcm), player.finished
			player.mu.Unlock()
			if !bytes.Equal(actual, expected) || finished != endWhileLoading {
				t.Fatalf("manual streaming duplicated or lost PCM: got=%v expected=%v finished=%v", actual, expected, finished)
			}
			if !endWhileLoading {
				h.d.receiveVoiceEvent(session, realtime.Event{Kind: realtime.ResponseEnd, CompletionID: "growing", Final: true}, "")
			}
			h.await(func() bool { return h.d.responsePlayback == nil })
			h.d.voiceSession = nil
		})
	}
}

func TestVoiceHistoricalResponseCanPlayDuringANewLiveSession(t *testing.T) {
	player := &manualVoicePlayer{done: make(chan struct{})}
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.newResponsePlayer = func(string, int, int) responsePlayer { return player }
	})
	doc := h.d.document
	h.d.selectAudioMode(doc, voiceModeLive)
	writer, err := newVoiceAudioFile(t.TempDir(), 24000)
	if err != nil {
		t.Fatal(err)
	}
	pcm := []byte{1, 0, 2, 0}
	if err := writer.write(pcm); err != nil {
		t.Fatal(err)
	}
	if err := writer.file.Close(); err != nil {
		t.Fatal(err)
	}
	doc.voice.History = []voiceTurn{{ID: "previous-session", AudioPath: writer.path, SampleRate: 24000, Complete: true}}
	h.d.voiceSession = &voiceSession{doc: doc, files: responseFiles{folder: t.TempDir()}}
	h.d.playResponse(doc)
	h.await(func() bool { return h.d.responsePlayback == nil })
	player.mu.Lock()
	defer player.mu.Unlock()
	if !player.finished || !bytes.Equal(player.pcm, pcm) {
		t.Fatalf("historical audio failed during a new session: pcm=%v finished=%v", player.pcm, player.finished)
	}
	h.d.voiceSession = nil
}

type startupFinalVoiceConnection struct {
	*testVoiceConnection
	receive   func(realtime.Event)
	begin     sync.Once
	release   chan struct{}
	canceled  chan struct{}
	responses []realtime.Response
}

func (c *startupFinalVoiceConnection) Cancel() {
	c.begin.Do(func() {
		close(c.canceled)
		go func() {
			<-c.release
			pcm := []byte{7, 0, 8, 0}
			c.receive(realtime.Event{Kind: realtime.AssistantAudio, CompletionID: "startup-final", PCM: pcm, SampleRate: 24000})
			c.mu.Lock()
			c.responses = []realtime.Response{{CompletionID: "startup-final", UserText: "Startup question", Text: "Startup partial reply", PCM: pcm, SampleRate: 24000}}
			c.mu.Unlock()
			c.testVoiceConnection.Cancel()
		}()
	})
}
func (c *startupFinalVoiceConnection) Snapshot() []realtime.Response {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.responses)
}

func TestVoiceQuitDuringStartupKeepsFilesOpenAndOwnsRepeatedClose(t *testing.T) {
	started := make(chan struct{})
	connection := &startupFinalVoiceConnection{
		testVoiceConnection: &testVoiceConnection{done: make(chan struct{})},
		release:             make(chan struct{}), canceled: make(chan struct{}),
	}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(connection.release) }) }
	defer release()
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.newConversation = func(_ context.Context, _ config.Settings, _ realtime.Options, receive func(realtime.Event)) (voiceConnection, error) {
			connection.receive = receive
			return connection, nil
		}
		deps.newConfiguredRecorder = func(_ config.Settings, folder string) recording {
			path := filepath.Join(folder, "startup-mic.wav")
			if err := os.MkdirAll(folder, 0o700); err != nil {
				t.Error(err)
			}
			if err := os.WriteFile(path, append(voiceWAVHeader(16000, 2), 1, 0), 0o600); err != nil {
				t.Error(err)
			}
			return &fakeRecording{state: recorder.Snapshot{Path: path, SampleRate: 16000, WrittenSamples: 1}, start: func(ctx context.Context) (string, error) {
				close(started)
				<-ctx.Done()
				return path, ctx.Err()
			}}
		}
	})
	autoplay := false
	h.d.settings.Conversation.Autoplay = &autoplay
	h.d.selectAudioMode(h.d.document, voiceModeLive)
	h.d.startVoiceSession()
	waitValue(t, started)
	session := h.d.voiceSession
	h.d.finish()
	waitValue(t, connection.canceled)
	h.drain()
	session.files.mu.Lock()
	closedEarly := session.files.closed
	session.files.mu.Unlock()
	if closedEarly || h.d.voiceSession != session || h.d.closing {
		t.Fatal("startup cancellation closed files or lost the pending Quit owner")
	}
	h.d.finish()
	if h.d.closing {
		t.Fatal("repeated close escaped startup finalization")
	}
	release()
	h.await(func() bool { return h.d.closing })
	if len(session.doc.voice.History) != 1 || session.doc.voice.History[0].Text != "Startup partial reply" {
		t.Fatalf("startup Quit lost final text: %+v", session.doc.voice.History)
	}
	pcm, rate, err := readVoiceAudio(session.doc.voice.History[0].AudioPath)
	if err != nil || rate != 24000 || !bytes.Equal(pcm, []byte{7, 0, 8, 0}) {
		t.Fatalf("startup Quit lost final PCM: %v/%d/%v", pcm, rate, err)
	}
}

func TestVoiceFullSessionPreservesManualProcessingResultAndSeparateHistories(t *testing.T) {
	var callback func(realtime.Event)
	connection := &testVoiceConnection{done: make(chan struct{})}
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.newConversation = func(_ context.Context, _ config.Settings, _ realtime.Options, receive func(realtime.Event)) (voiceConnection, error) {
			callback = receive
			return connection, nil
		}
		deps.newConfiguredRecorder = func(_ config.Settings, folder string) recording {
			path := filepath.Join(folder, "synthetic-microphone.wav")
			if err := os.MkdirAll(folder, 0o700); err != nil {
				t.Error(err)
			}
			if err := os.WriteFile(path, append(voiceWAVHeader(16000, 4), 1, 0, 2, 0), 0o600); err != nil {
				t.Error(err)
			}
			return &fakeRecording{state: recorder.Snapshot{Path: path, SampleRate: 16000, WrittenSamples: 2}}
		}
	})
	doc := h.d.document
	doc.editor.SetText("Existing transcription")
	const manual = "Manual processed result without a processing.Result"
	doc.processEditor.SetText(manual)
	doc.processHistory = []processing.Turn{{Prompt: "Earlier prompt", Response: "Earlier reply"}, {Prompt: doc.editor.Text, Response: manual}}
	doc.promptSteps = []promptStep{
		{ID: 1, Conversation: 1, Prompt: "Earlier prompt", Response: "Earlier reply", Sent: true, Processed: true},
		{ID: 2, Conversation: 1, Prompt: doc.editor.Text, Response: manual, Sent: true, Processed: true},
	}
	doc.currentConversation, doc.nextPrompt, doc.promptCursor = 1, 2, 1
	originalSteps := slices.Clone(doc.promptSteps)
	autoplay := false
	h.d.settings.Conversation.Autoplay = &autoplay
	h.d.selectAudioMode(doc, voiceModeLive)
	h.d.startVoiceSession()
	h.await(func() bool { return h.d.capture == captureRunning })
	for _, id := range []string{"first", "second"} {
		callback(realtime.Event{Kind: realtime.UserText, CompletionID: id, Text: "User " + id, Final: true})
		callback(realtime.Event{Kind: realtime.AssistantText, CompletionID: id, Text: "Assistant " + id, Final: true})
		callback(realtime.Event{Kind: realtime.AssistantAudio, CompletionID: id, PCM: []byte{1, 0, 2, 0}, SampleRate: 24000})
		callback(realtime.Event{Kind: realtime.ResponseEnd, CompletionID: id, Final: true})
	}
	h.drain()
	h.d.stopVoiceSession()
	h.await(func() bool { return h.d.capture == captureIdle })
	h.d.selectAudioMode(doc, voiceModeRecording)
	if doc.processEditor.Text != manual || doc.processResult != nil || len(doc.processHistory) != 2 ||
		len(doc.voice.History) != 2 || len(doc.promptSteps) != 3 || !reflect.DeepEqual(doc.promptSteps[:2], originalSteps) {
		t.Fatalf("voice session changed manual processing content/history: result=%q processed=%v turns=%d prompts=%+v voice=%+v",
			doc.processEditor.Text, doc.processResult, len(doc.processHistory), doc.promptSteps, doc.voice.History)
	}
	if doc.processHistory[1].Response != manual || doc.editor.Text != "Existing transcription\n\nUser first\n\nUser second" {
		t.Fatalf("shared transcription or previous response changed: %q / %+v", doc.editor.Text, doc.processHistory)
	}
	reopened := newHarness(t, nil, nil)
	reopened.d.directory = h.d.directory
	if !reopened.d.loadWorkspace() || reopened.d.processEditor.Text != manual || reopened.d.processResult != nil ||
		len(reopened.d.processHistory) != 2 || len(reopened.d.voice.History) != 2 {
		t.Fatal("manual processing result or either history failed to reopen")
	}
}
