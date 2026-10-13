package desktop

import (
	"context"
	"image"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ai-talker/internal/config"
	"ai-talker/internal/live"
	"ai-talker/internal/recorder"
	"ai-talker/internal/transcript"

	"fyne.io/fyne/v2/test"
)

type fakeTeamCapture struct {
	*fakeRecording
	outputMu sync.Mutex
	output   recorder.Snapshot
	offset   float64
}

func TestLongTeamRecordingUsesBoundedPartsAndPublishesOnlyAfterCompletion(t *testing.T) {
	type call struct {
		source  string
		release chan struct{}
	}
	started := make(chan call, 8)
	var releases []chan struct{}
	var releaseMu sync.Mutex
	h := newHarness(t, runnerFunc(func(ctx context.Context, source string, settings config.Settings, request transcript.Request) (
		transcript.Result, error,
	) {
		if request.OnPartial != nil || request.NativePCM || settings.SpeakerLabels ||
			settings.ChunkSeconds < 120 || settings.OverlapSeconds != 0 {
			t.Error("offline team recording still used the Stream recognition contract")
		}
		release := make(chan struct{})
		releaseMu.Lock()
		releases = append(releases, release)
		releaseMu.Unlock()
		started <- call{source, release}
		select {
		case <-release:
		case <-ctx.Done():
			return transcript.Result{}, ctx.Err()
		}
		return resultFor(source, "[Locutor 1] Trecho reconhecido."), nil
	}), nil)
	t.Cleanup(func() {
		releaseMu.Lock()
		defer releaseMu.Unlock()
		for _, release := range releases {
			select {
			case <-release:
			default:
				close(release)
			}
		}
	})
	doc := h.d.document
	// Exercise the advanced four-slot schedule; the managed default is two.
	h.d.settings.Recovery.Concurrency = 4
	doc.editor.SetText("Texto que já estava na aba.")
	doc.processEditor.SetText("Resultado preservado.")
	ctx, cancel := context.WithCancel(h.d.ctx)
	session := &liveRecording{
		liveArchive: liveArchive{
			Source: filepath.Join(recordingFolder(h.d.directory), "team-mic.wav"),
			Prefix: doc.editor.Text, Combined: doc.editor.Text, Labels: true,
		},
		doc: doc, settings: cloneSettings(h.d.settings), ctx: ctx, cancel: cancel,
		cursors: make(map[string]uint64), running: make(map[uint64]bool), started: h.d.deps.now(),
	}
	session.settings.Capture.Mode = config.CaptureTeam
	for _, id := range []string{"microphone", "system"} {
		path := filepath.Join(recordingFolder(h.d.directory), "team-"+id+".wav")
		streamFixtureSeconds(t, path, 121)
		session.Tracks = append(session.Tracks, recorder.Track{
			ID: id, Snapshot: recorder.Snapshot{Path: path, SampleRate: 16000, Duration: 121, WrittenSamples: 121 * 16000},
		})
	}
	doc.live = session
	h.d.finishLiveCapture(session)
	var requests []call
	for range 4 {
		requests = append(requests, waitValue(t, started))
	}
	if len(session.Entries) != 6 || !doc.busy || doc.editor.Text != session.Prefix {
		t.Fatal("a long team recording was divided into tiny requests or changed the editor prematurely")
	}
	for _, id := range []string{"microphone", "system"} {
		var covered uint64
		for _, entry := range session.Entries {
			if entry.Track == id {
				if entry.FirstSample != covered || entry.ContextSamples != 0 ||
					entry.LastSample-entry.FirstSample > cloudTeamPartSeconds*16000 {
					t.Fatal("offline team parts lost or duplicated their audio coverage")
				}
				covered = entry.LastSample
			}
		}
		if covered != 121*16000 {
			t.Fatal("team recording lost the final second")
		}
	}
	close(requests[0].release)
	h.await(func() bool { return doc.view.progress > 0 })
	requests = append(requests, waitValue(t, started))
	if doc.editor.Text != session.Prefix || doc.view.progress <= 0 {
		t.Fatal("an offline partial reached the editor or completed work had no progress feedback")
	}
	for _, request := range requests[1:] {
		close(request.release)
	}
	h.await(func() bool { return session.running[session.Entries[len(session.Entries)-1].ID] })
	close(waitValue(t, started).release)
	h.await(func() bool { return !doc.busy })
	h.settle()
	if !strings.HasPrefix(doc.editor.Text, session.Prefix+"\n\n") ||
		strings.Count(doc.editor.Text, "Trecho reconhecido.") != 6 ||
		strings.Contains(doc.editor.Text, "Locutor") || strings.Contains(doc.editor.Text, "Áudio do sistema") ||
		doc.processEditor.Text != "Resultado preservado." || doc.view.progress != 1 {
		t.Fatalf("final team publication lost previous text or repeated technical labels: %q", doc.editor.Text)
	}
	select {
	case extra := <-started:
		t.Fatalf("team unnecessarily rerecognized the full recording: %s", extra.source)
	case <-time.After(20 * time.Millisecond):
	}
}

func (r *fakeTeamCapture) Tracks() []recorder.Track {
	r.outputMu.Lock()
	system := r.output
	r.outputMu.Unlock()
	offset := r.offset
	if offset == 0 {
		offset = 0.25
	}
	return []recorder.Track{
		{ID: "microphone", Label: "Você", Snapshot: r.Snapshot()},
		{ID: "system", Label: "Áudio do sistema", Offset: offset, Snapshot: system},
	}
}

func (r *fakeTeamCapture) Stop() (string, error) {
	path, err := r.fakeRecording.Stop()
	r.outputMu.Lock()
	r.output.Active = false
	r.outputMu.Unlock()
	return path, err
}

func (r *fakeTeamCapture) Pause() {
	r.fakeRecording.Pause()
	r.outputMu.Lock()
	r.output.Paused = true
	r.outputMu.Unlock()
}

func (r *fakeTeamCapture) Resume() {
	r.fakeRecording.Resume()
	r.outputMu.Lock()
	r.output.Paused = false
	r.outputMu.Unlock()
}

func TestTeamWithoutStreamRecognizesSeparateSourcesOnStopAndKeepsScreenshotPosition(t *testing.T) {
	var calls atomic.Int32
	r := &fakeTeamCapture{fakeRecording: &fakeRecording{state: recorder.Snapshot{SampleRate: 16000}}}
	h := newHarness(t, runnerFunc(func(_ context.Context, source string, settings config.Settings, _ transcript.Request) (
		transcript.Result, error,
	) {
		calls.Add(1)
		if strings.HasPrefix(filepath.Base(source), "system-") {
			if settings.SpeakerLabels {
				t.Error("system source requested anonymous labels that reset in each part")
			}
			return resultFor(source, "[Locutor 1] Explicação do tutorial.\n[Locutor 2] Resposta de outra pessoa."), nil
		}
		if settings.SpeakerLabels {
			t.Error("local source lost the known source label")
		}
		return resultFor(source, "Minha pergunta sobre o tutorial."), nil
	}), func(deps *dependencies) {
		deps.newConfiguredRecorder = func(settings config.Settings, directory string) recording {
			if settings.Capture.SourceMode() != config.CaptureTeam {
				t.Error("configured recorder ignored the team choice")
			}
			r.update(func(state *recorder.Snapshot) { state.Path = filepath.Join(directory, "team-mic.wav") })
			r.outputMu.Lock()
			r.output = recorder.Snapshot{Path: filepath.Join(directory, "team-system.wav"), SampleRate: 16000, Active: true}
			r.outputMu.Unlock()
			return r
		}
		deps.selectScreenshot = func(context.Context) (image.Image, error) { return screenshotFixture(), nil }
	})
	h.d.settings.Capture.Mode, h.d.settings.Capture.Stream = config.CaptureTeam, false
	for _, name := range []string{"team-mic.wav", "team-system.wav"} {
		streamFixture(t, filepath.Join(fixtureRecordingFolder(h), name))
	}
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	r.update(func(state *recorder.Snapshot) { state.Duration, state.WrittenSamples = 4, 64000 })
	r.outputMu.Lock()
	r.output.Duration, r.output.WrittenSamples = 3.75, 60000
	r.outputMu.Unlock()
	h.d.tick()
	shot := takeScreenshot(t, h)
	if calls.Load() != 0 || h.d.editor.Text != "" {
		t.Fatal("team started recognition despite Stream being disabled")
	}
	test.Tap(h.d.pauseButton)
	if !r.Snapshot().Paused || !r.Tracks()[1].Snapshot.Paused {
		t.Fatal("team controls did not pause both sources")
	}
	test.Tap(h.d.pauseButton)
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureIdle && !h.d.busy })
	h.settle()
	text := h.d.editor.Text
	if calls.Load() != 2 || !strings.Contains(text, "Mic: Minha pergunta") ||
		!strings.Contains(text, "Sis: Explicação do tutorial.") || strings.Contains(text, "[00:") ||
		strings.Contains(text, "Locutor") || strings.Contains(text, "Áudio do sistema") ||
		!strings.Contains(text, "Resposta de outra pessoa.") ||
		!strings.HasSuffix(text, "\n\n"+shot.path) || strings.Contains(text, screenshotLinkHeading) ||
		len(collectLinks(h.d.transcriptPreview.Segments)) != 1 {
		t.Fatalf("team mixed sources, lost speaker turns or changed the screenshot position: %q", text)
	}
}

func TestTeamRecognitionStartsEarliestPartsOfBothSources(t *testing.T) {
	started := make(chan string, 24)
	releaseChannel := make(chan struct{})
	release := sync.OnceFunc(func() { close(releaseChannel) })
	h := newHarness(t, runnerFunc(func(ctx context.Context, source string, _ config.Settings, _ transcript.Request) (
		transcript.Result, error,
	) {
		started <- filepath.Base(source)
		select {
		case <-releaseChannel:
		case <-ctx.Done():
			return transcript.Result{}, ctx.Err()
		}
		return resultFor(source, "Fala da fonte "+filepath.Base(source)), nil
	}), nil)
	t.Cleanup(release)
	ctx, cancel := context.WithCancel(h.d.ctx)
	session := &liveRecording{
		liveArchive: liveArchive{
			Source: filepath.Join(recordingFolder(h.d.directory), "team-mic.wav"),
			Labels: true, InlineScreenshots: true,
		},
		doc: h.d.document, settings: cloneSettings(h.d.settings), ctx: ctx, cancel: cancel,
		cursors: make(map[string]uint64), running: make(map[uint64]bool), started: h.d.deps.now(),
	}
	for index, trackID := range []string{"microphone", "system"} {
		path := filepath.Join(recordingFolder(h.d.directory), "team-"+trackID+".wav")
		streamFixture(t, path)
		session.Tracks = append(session.Tracks, recorder.Track{
			ID: trackID, Offset: float64(index) / 4, Snapshot: recorder.Snapshot{
				Path: path, SampleRate: 16000, Duration: 20, WrittenSamples: 320000,
			},
		})
	}
	h.d.document.live = session
	h.d.finishLiveCapture(session)
	opened := []string{waitValue(t, started), waitValue(t, started)}
	var microphone, system bool
	for _, name := range opened {
		if strings.Contains(name, "-000000000000-") {
			microphone = microphone || strings.HasPrefix(name, "microphone-")
			system = system || strings.HasPrefix(name, "system-")
		}
	}
	if !microphone || !system {
		t.Fatalf("team backlog delayed the start of one source: %v", opened)
	}
	release()
	h.await(func() bool { return !h.d.busy })
	h.settle()
	if len(session.Entries) != 10 || len(live.Blocks(session.Entries, true)) != 10 {
		t.Fatal("team backlog lost a source or part")
	}
}

func TestTeamStreamShowsBothSourcesWhileRecordingAndKeepsScreenshotInSpeech(t *testing.T) {
	releaseChannel := make(chan struct{})
	release := sync.OnceFunc(func() { close(releaseChannel) })
	var calls atomic.Int32
	r := &fakeTeamCapture{fakeRecording: &fakeRecording{state: recorder.Snapshot{SampleRate: 16000}}}
	h := newHarness(t, runnerFunc(func(ctx context.Context, source string, settings config.Settings, request transcript.Request) (
		transcript.Result, error,
	) {
		calls.Add(1)
		system := strings.HasPrefix(filepath.Base(source), "system-")
		if settings.SpeakerLabels {
			t.Error("team stream requested anonymous labels that reset in each part")
		}
		text := "Minha fala depois da foto."
		if system {
			text = "Tutorial depois da foto."
		}
		if strings.Contains(filepath.Base(source), "-000000000000-") {
			text = "Minha fala antes da foto."
			if system {
				text = "Tutorial antes da foto."
			}
			if request.OnPartial == nil || !request.NativePCM {
				t.Error("team stream lost incremental recognition or native preparation")
			} else {
				request.OnPartial(transcript.Partial{Text: text})
			}
			select {
			case <-releaseChannel:
			case <-ctx.Done():
				return transcript.Result{}, ctx.Err()
			}
		}
		return resultFor(source, text), nil
	}), func(deps *dependencies) {
		deps.newConfiguredRecorder = func(_ config.Settings, directory string) recording {
			r.update(func(state *recorder.Snapshot) { state.Path = filepath.Join(directory, "team-mic.wav") })
			r.outputMu.Lock()
			r.output = recorder.Snapshot{Path: filepath.Join(directory, "team-system.wav"), SampleRate: 16000, Active: true}
			r.outputMu.Unlock()
			return r
		}
		deps.selectScreenshot = func(context.Context) (image.Image, error) { return screenshotFixture(), nil }
	})
	t.Cleanup(release)
	// Hold both initial parts while newer partials run in the advanced mode.
	h.d.settings.Recovery.Concurrency = 4
	h.d.settings.Capture.Mode, h.d.settings.Capture.Stream = config.CaptureTeam, true
	h.d.syncCaptureOptions()
	for _, name := range []string{"team-mic.wav", "team-system.wav"} {
		streamFixture(t, filepath.Join(fixtureRecordingFolder(h), name))
	}
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	r.outputMu.Lock()
	r.output.Duration, r.output.WrittenSamples = streamPartSeconds-0.25, uint64((streamPartSeconds-0.25)*16000)
	r.outputMu.Unlock()
	admitStream(h, r.fakeRecording, streamPartSeconds)
	h.await(func() bool { return strings.Contains(h.d.editor.Text, "Minha fala antes") })
	shot := takeScreenshot(t, h)
	h.await(func() bool { return strings.Contains(h.d.editor.Text, "Sis: Tutorial antes") })
	if h.d.capture != captureRunning || h.d.lastResult != nil {
		t.Fatal("team sources appeared only after stopping")
	}
	r.outputMu.Lock()
	r.output.Duration, r.output.WrittenSamples = 2*streamPartSeconds-0.25, uint64((2*streamPartSeconds-0.25)*16000)
	r.outputMu.Unlock()
	admitStream(h, r.fakeRecording, 2*streamPartSeconds)
	h.await(func() bool {
		return strings.Contains(h.d.editor.Text, "Mic: Minha fala depois") &&
			strings.Contains(h.d.editor.Text, "Sis: Tutorial depois")
	})
	release()
	h.await(func() bool { return len(h.d.live.running) == 0 })
	text := h.d.editor.Text
	for _, before := range []string{"Minha fala antes", "Tutorial antes"} {
		if strings.Index(text, before) > strings.Index(text, shot.path) {
			t.Fatal("speech before the screenshot arrived after its link")
		}
	}
	for _, after := range []string{"Minha fala depois", "Tutorial depois"} {
		if strings.Index(text, after) < strings.Index(text, shot.path) {
			t.Fatal("speech after the screenshot arrived before its link")
		}
	}
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureIdle && !h.d.busy })
	if h.d.editor.Text != text || strings.Contains(text, "[00:") || calls.Load() != 4 {
		t.Fatal("team completion added timestamps, repeated audio or lost live text")
	}
}
