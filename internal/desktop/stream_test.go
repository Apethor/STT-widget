package desktop

import (
	"context"
	"encoding/binary"
	"errors"
	"image"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"ai-talker/internal/config"
	"ai-talker/internal/live"
	"ai-talker/internal/recorder"
	"ai-talker/internal/store"
	"ai-talker/internal/transcript"

	"fyne.io/fyne/v2/test"
)

// The file contains deterministic PCM. Snapshot admission controls how much the
// streaming scheduler may read while the fake recorder remains active.
func streamFixture(t *testing.T, path string) {
	streamFixtureSeconds(t, path, 30)
}

func streamFixtureSeconds(t *testing.T, path string, seconds int) {
	t.Helper()
	streamFixtureRate(t, path, seconds, 16000)
}

func streamFixtureRate(t *testing.T, path string, seconds, rate int) {
	t.Helper()
	data := make([]byte, 44+rate*seconds*2)
	copy(data[:4], "RIFF")
	binary.LittleEndian.PutUint32(data[4:8], uint32(len(data)-8))
	copy(data[8:16], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:20], 16)
	binary.LittleEndian.PutUint16(data[20:22], 1)
	binary.LittleEndian.PutUint16(data[22:24], 1)
	binary.LittleEndian.PutUint32(data[24:28], uint32(rate))
	binary.LittleEndian.PutUint32(data[28:32], uint32(rate*2))
	binary.LittleEndian.PutUint16(data[32:34], 2)
	binary.LittleEndian.PutUint16(data[34:36], 16)
	copy(data[36:40], "data")
	binary.LittleEndian.PutUint32(data[40:44], uint32(len(data)-44))
	for index := 44; index < len(data); index += 2 {
		binary.LittleEndian.PutUint16(data[index:index+2], uint16(index%3000))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func streamHarness(t *testing.T, runner Runner, configure func(*dependencies)) (*testHarness, *fakeRecording) {
	t.Helper()
	r := &fakeRecording{state: recorder.Snapshot{SampleRate: 16000}}
	h := newHarness(t, runner, func(deps *dependencies) {
		deps.newRecorder = func(_ int, directory string) recording {
			path := filepath.Join(directory, "stream.wav")
			r.update(func(state *recorder.Snapshot) { state.Path = path })
			return r
		}
		deps.selectScreenshot = func(context.Context) (image.Image, error) { return screenshotFixture(), nil }
		if configure != nil {
			configure(deps)
		}
	})
	streamFixture(t, filepath.Join(fixtureRecordingFolder(h), "stream.wav"))
	h.d.settings.Capture.Stream = true
	h.d.syncCaptureOptions()
	return h, r
}

func fixtureRecordingFolder(h *testHarness) string {
	return filepath.Join(recordingFolder(h.d.directory), h.d.deps.now().Format("2006-01-02"))
}

func admitStream(h *testHarness, r *fakeRecording, seconds float64) {
	h.t.Helper()
	r.update(func(state *recorder.Snapshot) {
		state.Duration = seconds
		state.WrittenSamples = uint64(seconds * float64(state.SampleRate))
	})
	h.d.tick()
}

func TestStreamScreenshotAppearsDuringCaptureAndLateSpeechStaysInOrder(t *testing.T) {
	firstStarted := make(chan struct{}, 1)
	firstRelease := make(chan struct{})
	release := sync.OnceFunc(func() { close(firstRelease) })
	var calls atomic.Int32
	copied := make(chan string, 1)
	h, r := streamHarness(t, runnerFunc(func(ctx context.Context, source string, _ config.Settings, _ transcript.Request) (
		transcript.Result, error,
	) {
		calls.Add(1)
		if strings.Contains(filepath.Base(source), "-000000000000-") {
			firstStarted <- struct{}{}
			select {
			case <-firstRelease:
			case <-ctx.Done():
				return transcript.Result{}, ctx.Err()
			}
			return resultFor(source, "Fala antes da screenshot."), nil
		}
		return resultFor(source, "Fala depois da screenshot."), nil
	}), func(deps *dependencies) { deps.copy = func(text string) error { copied <- text; return nil } })
	t.Cleanup(release)
	first := h.d.document
	first.editor.SetText("Texto anterior.")
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	if !h.d.streamButton.Disabled() {
		t.Fatal("stream mode could change halfway through capture")
	}
	admitStream(h, r, 1)
	shot := takeScreenshot(t, h)
	waitValue(t, firstStarted)
	if h.d.capture != captureRunning || !strings.Contains(first.editor.Text, shot.path) ||
		strings.Contains(first.editor.Text, "[00:") ||
		len(collectLinks(first.transcriptPreview.Segments)) != 1 {
		t.Fatal("screenshot did not get a clickable link during the stream")
	}
	admitStream(h, r, 1+streamPartSeconds)
	h.await(func() bool { return strings.Contains(first.editor.Text, "Fala depois") })
	second := h.d.addDocument()
	second.editor.SetText("Texto independente.")
	release()
	h.await(func() bool { return strings.Contains(first.editor.Text, "Fala antes") })
	text := first.editor.Text
	if strings.Index(text, "Fala antes") > strings.Index(text, shot.path) ||
		strings.Index(text, "Fala depois") < strings.Index(text, shot.path) ||
		!strings.HasPrefix(text, "Texto anterior.") || strings.Contains(text, screenshotLinkHeading) ||
		second.editor.Text != "Texto independente." {
		t.Fatalf("late recognition lost the timeline or tab ownership: %q", text)
	}
	h.d.copyDocumentText(first, false)
	if waitValue(t, copied) != text {
		t.Fatal("copy omitted the full screenshot path")
	}
	h.settle()
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureIdle && !first.busy })
	h.settle()
	if calls.Load() != 2 || first.editor.Text != text || first.lastResult == nil ||
		first.lastResult.DurationSeconds != 1+streamPartSeconds || first.lastResult.Source != r.Snapshot().Path {
		t.Fatal("stopping retranscribed or duplicated the accepted stream")
	}
	if saved := readResult(t, h.d.documentLatestPath(first)); saved.Text != text {
		t.Fatal("stream timeline was not persisted on its original tab")
	}
	if err := h.d.saveWorkspace(); err != nil {
		t.Fatal(err)
	}
	h.d.loadWorkspace()
	if h.d.documents[0].live == nil || h.d.documents[0].editor.Text != text ||
		len(collectLinks(h.d.documents[0].transcriptPreview.Segments)) != 1 {
		t.Fatal("restored session lost the timeline or screenshot link")
	}
}

func TestStreamAudioContextPreservesBoundaryWordsWithoutRepeatingBeforeScreenshot(t *testing.T) {
	h, r := streamHarness(t, runnerFunc(func(_ context.Context, source string, _ config.Settings, _ transcript.Request) (
		transcript.Result, error,
	) {
		if strings.Contains(filepath.Base(source), "-context-") {
			snapshot, err := recorder.Inspect(source)
			if err != nil || snapshot.Duration != streamPartSeconds+streamContextSeconds {
				return transcript.Result{}, errors.New("the stream seam lost its two seconds of audio context")
			}
			return resultFor(source, "Hello. This is a short recording."), nil
		}
		return resultFor(source, "Hello. This is a short"), nil
	}), nil)
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	admitStream(h, r, streamPartSeconds)
	h.await(func() bool { return h.d.editor.Text == "Hello. This is a short" })
	shot := takeScreenshot(t, h)
	admitStream(h, r, 2*streamPartSeconds)
	want := "Hello. This is a short\n\n" + shot.path + "\n\nrecording."
	h.await(func() bool { return h.d.editor.Text == want })
	session := h.d.document.live
	last := session.Entries[len(session.Entries)-1]
	if last.ContextSamples != 32000 || last.FirstSample != uint64(streamPartSeconds*16000) ||
		last.Start != streamPartSeconds || last.End != 2*streamPartSeconds {
		t.Fatal("audio context moved the logical screenshot boundary")
	}
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureIdle && !h.d.busy })
	if h.d.lastResult.DurationSeconds != 2*streamPartSeconds || h.d.editor.Text != want {
		t.Fatal("stopping counted overlapping audio twice")
	}
	if err := h.d.saveWorkspace(); err != nil {
		t.Fatal(err)
	}
	h.d.loadWorkspace()
	if h.d.editor.Text != want || !h.d.validLiveArchive(&h.d.live.liveArchive) {
		t.Fatal("restoring a stream lost or rejected its overlap")
	}
}

func TestStreamClearKeepsCaptureAndHidesOutstandingOldText(t *testing.T) {
	started := make(chan struct{}, 1)
	releaseChannel := make(chan struct{})
	release := sync.OnceFunc(func() { close(releaseChannel) })
	h, r := streamHarness(t, runnerFunc(func(ctx context.Context, source string, _ config.Settings, _ transcript.Request) (
		transcript.Result, error,
	) {
		if strings.Contains(filepath.Base(source), "-000000000000-") {
			started <- struct{}{}
			select {
			case <-releaseChannel:
			case <-ctx.Done():
				return transcript.Result{}, ctx.Err()
			}
			return resultFor(source, "Trecho anterior à limpeza."), nil
		}
		return resultFor(source, "Trecho novo após a limpeza."), nil
	}), nil)
	t.Cleanup(release)
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	admitStream(h, r, streamPartSeconds)
	waitValue(t, started)
	test.Tap(h.d.clearButton)
	release()
	admitStream(h, r, 2*streamPartSeconds)
	h.await(func() bool { return strings.Contains(h.d.editor.Text, "Trecho novo") })
	if h.d.capture != captureRunning || strings.Contains(h.d.editor.Text, "Trecho anterior") {
		t.Fatal("clear stopped capture or allowed an old recognition to restore text")
	}
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureIdle && !h.d.busy })
	if strings.Contains(h.d.editor.Text, "Trecho anterior") {
		t.Fatal("stopping restored text hidden by clear")
	}
}

func TestStreamFailedChunkRetriesWithoutDuplicatingSuccessfulSpeech(t *testing.T) {
	var failures atomic.Int32
	h, r := streamHarness(t, runnerFunc(func(_ context.Context, source string, _ config.Settings, _ transcript.Request) (
		transcript.Result, error,
	) {
		if strings.Contains(filepath.Base(source), "-000000000000-") && failures.Add(1) == 1 {
			return transcript.Result{}, errors.New("falha temporária")
		}
		if strings.Contains(filepath.Base(source), "-000000000000-") {
			return resultFor(source, "Primeiro trecho recuperado."), nil
		}
		return resultFor(source, "Segundo trecho preservado."), nil
	}), nil)
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	admitStream(h, r, 2*streamPartSeconds)
	h.await(func() bool { return strings.Contains(h.d.editor.Text, "Segundo trecho") })
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureIdle && !h.d.busy })
	if h.d.lastResult == nil || !h.d.lastResult.NeedsReview || !h.d.retryButton.Visible() {
		t.Fatal("stream failure was not recoverable")
	}
	h.d.retryDocument(h.d.document)
	h.await(func() bool { return !h.d.busy })
	h.settle()
	if strings.Count(h.d.editor.Text, "Primeiro trecho recuperado.") != 1 ||
		strings.Count(h.d.editor.Text, "Segundo trecho preservado.") != 1 ||
		strings.Contains(h.d.editor.Text, "pendente de transcrição") || h.d.lastResult.NeedsReview {
		t.Fatal("retry duplicated accepted speech or did not replace the failed chunk")
	}
}

func TestStreamDiscardKeepsCompletedTextAndRejectsPendingResult(t *testing.T) {
	pending := make(chan struct{}, 1)
	releaseChannel := make(chan struct{})
	release := sync.OnceFunc(func() { close(releaseChannel) })
	h, r := streamHarness(t, runnerFunc(func(ctx context.Context, source string, _ config.Settings, _ transcript.Request) (
		transcript.Result, error,
	) {
		if strings.Contains(filepath.Base(source), "-000000000000-") {
			return resultFor(source, "Texto recebido antes de descartar."), nil
		}
		pending <- struct{}{}
		select {
		case <-releaseChannel:
		case <-ctx.Done():
		}
		return resultFor(source, "Resposta antiga que deve ser rejeitada."), nil
	}), nil)
	t.Cleanup(release)
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	admitStream(h, r, streamPartSeconds)
	h.await(func() bool { return strings.Contains(h.d.editor.Text, "Texto recebido") })
	admitStream(h, r, 2*streamPartSeconds)
	waitValue(t, pending)
	h.d.stopCapture(discardCapture)
	h.await(func() bool { return h.d.capture == captureIdle })
	release()
	h.settle()
	if !strings.Contains(h.d.editor.Text, "Texto recebido") || strings.Contains(h.d.editor.Text, "Resposta antiga") {
		t.Fatal("discard erased completed speech or accepted cancelled recognition")
	}
}

func TestStreamUndoClearRetainsLiveWorkerOwnership(t *testing.T) {
	h, r := streamHarness(t, runnerFunc(func(_ context.Context, source string, _ config.Settings, _ transcript.Request) (
		transcript.Result, error,
	) {
		return resultFor(source, "Trecho "+filepath.Base(source)), nil
	}), nil)
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	admitStream(h, r, streamPartSeconds)
	h.await(func() bool { return h.d.editor.Text != "" })
	original, session := h.d.editor.Text, h.d.live
	test.Tap(h.d.clearButton)
	test.Tap(h.d.undoButton)
	if h.d.editor.Text != original || h.d.live != session || h.d.captureOrigin.live != session {
		t.Fatal("undo changed ownership or failed to restore the exact cleared text")
	}
	admitStream(h, r, 2*streamPartSeconds)
	h.await(func() bool { return strings.Count(h.d.editor.Text, "Trecho ") == 2 })
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureIdle && !h.d.busy })
	if h.d.lastResult == nil || strings.Count(h.d.editor.Text, "Trecho ") != 2 {
		t.Fatal("stream could not finish after undoing a clear")
	}
}

func TestStreamRestoredActiveCheckpointIncludesFinalizedWAVTail(t *testing.T) {
	h, _ := streamHarness(t, runnerFunc(func(_ context.Context, source string, _ config.Settings, _ transcript.Request) (
		transcript.Result, error,
	) {
		return resultFor(source, "Trecho recuperado "+filepath.Base(source)), nil
	}), nil)
	doc := h.d.document
	source := filepath.Join(recordingFolder(h.d.directory), "stream.wav")
	streamFixture(t, source) // A checkpoint from before daily folders remains valid.
	doc.lastAudio = source
	archive := &liveArchive{
		Source: source, Combined: "Trecho já aceito.", InlineScreenshots: true, NextID: 1,
		Entries: []live.Entry{{ID: 1, Track: "microphone", Start: 0, End: 5,
			FirstSample: 0, LastSample: 80000, Text: "Trecho já aceito."}},
		Tracks: []recorder.Track{{ID: "microphone", Label: "Você", Snapshot: recorder.Snapshot{
			Path: source, Active: true, SampleRate: 16000, Duration: 5, WrittenSamples: 80000,
		}}},
	}
	if !h.d.validLiveArchive(archive) {
		t.Fatal("active checkpoint was rejected")
	}
	doc.editor.SetText(archive.Combined)
	h.d.restoreLiveArchive(doc, archive)
	previous := resultFor(filepath.Join(recordingFolder(h.d.directory), "previous.wav"), "Texto anterior.")
	doc.lastResult = &previous
	if err := h.d.saveWorkspace(); err != nil {
		t.Fatal(err)
	}
	h.d.loadWorkspace()
	doc = h.d.document
	if !doc.retryButton.Visible() {
		t.Fatal("active checkpoint could not be retried after an earlier recording succeeded")
	}
	test.Tap(doc.retryButton)
	h.await(func() bool { return !doc.busy })
	h.settle()
	if doc.lastResult == nil || doc.lastResult.DurationSeconds != 30 ||
		strings.Count(doc.editor.Text, "Trecho já aceito.") != 1 ||
		strings.Count(doc.editor.Text, "Trecho recuperado ") != 7 ||
		doc.live.cursors["microphone"] != 480000 {
		t.Fatalf("checkpoint recovery missed the finalized tail: %q", doc.editor.Text)
	}
}

func TestStreamScreenshotDeletionAfterRestoreUpdatesResultAndArchive(t *testing.T) {
	h, r := streamHarness(t, nil, nil)
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	admitStream(h, r, streamPartSeconds)
	shot := takeScreenshot(t, h)
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureIdle && !h.d.busy })
	h.settle()
	if err := h.d.saveWorkspace(); err != nil {
		t.Fatal(err)
	}
	h.d.loadWorkspace()
	doc := h.d.document
	h.d.removeDocumentScreenshotReference(doc, shot.path)
	h.settle()
	var archive liveArchive
	if err := store.ReadJSON(doc.lastAudio+".stream.json", &archive); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(doc.editor.Text, shot.path) || strings.Contains(doc.lastResult.Text, shot.path) ||
		len(doc.screenshotFiles) != 0 || len(collectLinks(doc.transcriptPreview.Segments)) != 0 {
		t.Fatal("restored screenshot remained in the visible or persisted result")
	}
	for _, entry := range archive.Entries {
		if entry.Screenshot {
			t.Fatal("restored screenshot remained in the archived timeline")
		}
	}
}

func TestStreamRestoredStoppedCheckpointQueuesMissingTail(t *testing.T) {
	h, _ := streamHarness(t, nil, nil)
	source := filepath.Join(recordingFolder(h.d.directory), "stream.wav")
	streamFixture(t, source)
	doc := h.d.document
	doc.lastAudio = source
	archive := &liveArchive{
		Source: source, Combined: "Fala recebida.", InlineScreenshots: true, NextID: 1,
		Entries: []live.Entry{{ID: 1, Track: "microphone", Start: 0, End: 5,
			FirstSample: 0, LastSample: 80000, Text: "Fala recebida."}},
		Tracks: []recorder.Track{{ID: "microphone", Label: "Você", Snapshot: recorder.Snapshot{
			Path: source, SampleRate: 16000, Duration: 7, WrittenSamples: 112000,
		}}},
	}
	if !archive.needsRetry() {
		t.Fatal("final PCM written while closing was not recoverable")
	}
	doc.editor.SetText(archive.Combined)
	h.d.restoreLiveArchive(doc, archive)
	h.d.retryLiveRecording(doc)
	h.await(func() bool { return !doc.busy })
	h.settle()
	if len(doc.live.Entries) != 2 || doc.lastResult.DurationSeconds != 7 ||
		strings.Count(doc.editor.Text, "Fala recebida.") != 1 ||
		strings.Count(doc.editor.Text, "Texto desta gravação.") != 1 || doc.live.needsRetry() {
		t.Fatal("retry missed or duplicated the short tail saved while closing")
	}
}

func TestStreamSilenceKeepsPreviousTextAndReportsNoSpeech(t *testing.T) {
	h, r := streamHarness(t, runnerFunc(func(_ context.Context, source string, _ config.Settings, _ transcript.Request) (
		transcript.Result, error,
	) {
		return resultFor(source, ""), nil
	}), nil)
	h.d.editor.SetText("Texto anterior.")
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	admitStream(h, r, streamPartSeconds)
	shot := takeScreenshot(t, h)
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureIdle && !h.d.busy })
	h.settle()
	if !strings.HasPrefix(h.d.editor.Text, "Texto anterior.") || !strings.Contains(h.d.editor.Text, shot.path) ||
		h.d.recordState.Text != "Nenhuma fala detectada." {
		t.Fatal("silent stream removed previous text, screenshot or the no-speech status")
	}
}

func TestStreamPartialTextPrecedesCompletionAndPreservesEditsAcrossUpdates(t *testing.T) {
	partials := make(chan func(transcript.Partial), 1)
	releaseChannel := make(chan struct{})
	release := sync.OnceFunc(func() { close(releaseChannel) })
	h, r := streamHarness(t, runnerFunc(func(ctx context.Context, source string, _ config.Settings, request transcript.Request) (
		transcript.Result, error,
	) {
		if strings.Contains(filepath.Base(source), "-000000000000-") {
			partials <- request.OnPartial
			select {
			case <-releaseChannel:
			case <-ctx.Done():
				return transcript.Result{}, ctx.Err()
			}
			return resultFor(source, "Fala original concluída."), nil
		}
		return resultFor(source, "Nova fala depois da foto."), nil
	}), nil)
	t.Cleanup(release)
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	admitStream(h, r, streamPartSeconds)
	partial := waitValue(t, partials)
	if partial == nil {
		t.Fatal("live capture did not request provisional text")
	}
	partial(transcript.Partial{Text: "Fala original"})
	h.await(func() bool { return h.d.editor.Text == "Fala original" })
	if h.d.capture != captureRunning || h.d.lastResult != nil {
		t.Fatal("partial text required stopping or became a final result")
	}
	h.d.editor.SetText("Minha correção.")
	partial(transcript.Partial{Text: "Fala original ainda chegando"})
	h.drain()
	if h.d.editor.Text != "Minha correção." {
		t.Fatal("first partial update overwrote the user's edit")
	}
	partial(transcript.Partial{Text: "Fala original quase completa"})
	h.drain()
	if h.d.editor.Text != "Minha correção." {
		t.Fatal("a repeated partial update erased a previously preserved edit")
	}
	shot := takeScreenshot(t, h)
	admitStream(h, r, 2*streamPartSeconds)
	h.await(func() bool { return strings.Contains(h.d.editor.Text, "Nova fala") })
	release()
	h.await(func() bool { return len(h.d.live.running) == 0 })
	text := h.d.editor.Text
	if !strings.HasPrefix(text, "Minha correção.") || strings.Contains(text, "Fala original") ||
		strings.Contains(text, "[00:") || strings.Index(text, "Nova fala") < strings.Index(text, shot.path) {
		t.Fatalf("partial completion lost edits or the screenshot position: %q", text)
	}
	partial(transcript.Partial{Text: "Partial atrasada após completar."})
	h.drain()
	if h.d.editor.Text != text {
		t.Fatal("late partial overwrote a completed part")
	}
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureIdle && !h.d.busy })
	if h.d.editor.Text != text || h.d.lastResult.Text != text {
		t.Fatal("completion did not preserve edited live text")
	}
}

func TestStreamFailedResponseRemovesItsProvisionalTextAndAllowsRetry(t *testing.T) {
	started := make(chan struct{}, 1)
	releaseChannel := make(chan struct{})
	release := sync.OnceFunc(func() { close(releaseChannel) })
	var calls atomic.Int32
	h, r := streamHarness(t, runnerFunc(func(ctx context.Context, source string, _ config.Settings, request transcript.Request) (
		transcript.Result, error,
	) {
		if calls.Add(1) > 1 {
			return resultFor(source, "Fala reconhecida corretamente."), nil
		}
		request.OnPartial(transcript.Partial{Text: "Resposta ainda provisória."})
		started <- struct{}{}
		select {
		case <-releaseChannel:
		case <-ctx.Done():
			return transcript.Result{}, ctx.Err()
		}
		return transcript.Result{}, errors.New("a resposta continuou além do áudio")
	}), nil)
	t.Cleanup(release)
	h.d.editor.SetText("Meu texto anterior.")
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	admitStream(h, r, streamPartSeconds)
	waitValue(t, started)
	h.await(func() bool { return strings.Contains(h.d.editor.Text, "ainda provisória") })
	release()
	h.await(func() bool { return h.d.errorLabel.Visible() && len(h.d.live.running) == 0 })
	if h.d.editor.Text != "Meu texto anterior." {
		t.Fatal("failed provisional text remained usable or removed earlier text")
	}
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureIdle && !h.d.busy })
	test.Tap(h.d.retryButton)
	h.await(func() bool { return !h.d.busy })
	if strings.Contains(h.d.editor.Text, "pendente") || strings.Contains(h.d.editor.Text, "provisória") ||
		!strings.Contains(h.d.editor.Text, "Fala reconhecida corretamente.") || h.d.lastResult.NeedsReview {
		t.Fatal("failed stream part could not be recovered cleanly")
	}
}
