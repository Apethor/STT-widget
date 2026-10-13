package desktop

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"ai-talker/internal/config"
	"ai-talker/internal/live"
	"ai-talker/internal/processing"
	"ai-talker/internal/recorder"
	"ai-talker/internal/transcript"

	"fyne.io/fyne/v2/test"
)

func TestRetranscribeUpdatesCurrentAudioWithSelectedModelAndFreshAttempts(t *testing.T) {
	type call struct {
		source   string
		settings config.Settings
		request  transcript.Request
	}
	started := make(chan call, 4)
	release := make(chan struct{})
	releaseJobs := sync.OnceFunc(func() { close(release) })
	var calls atomic.Int32
	h := newHarness(t, runnerFunc(func(ctx context.Context, source string, settings config.Settings, request transcript.Request) (
		transcript.Result, error,
	) {
		index := calls.Add(1)
		started <- call{source, settings, request}
		if index == 2 {
			select {
			case <-release:
			case <-ctx.Done():
				return transcript.Result{}, ctx.Err()
			}
		}
		result := resultFor(source, fmt.Sprintf("Versão %d.", index))
		result.ModelID = settings.ModelID()
		return result, nil
	}), nil)
	t.Cleanup(releaseJobs)
	doc := h.d.document
	if !doc.retranscribeButton.Disabled() {
		t.Fatal("retranscription was available without loaded audio")
	}
	doc.editor.SetText("Texto anterior.")
	doc.processEditor.SetText("Resposta já processada.")
	doc.processHistory = []processing.Turn{{Prompt: "Pedido anterior", Response: "Resposta anterior"}}
	h.d.startImportedJob(doc, "comparar.wav")
	first := waitValue(t, started)
	h.await(func() bool { return !doc.busy })
	if first.request.CacheDir != "" || doc.retranscribeButton.Disabled() || doc.retryButton.Visible() {
		t.Fatal("ordinary transcription lost its normal cache or the completed audio cannot be retranscribed")
	}
	shot := filepath.Join(h.d.directory, "screenshots", "preservada.png")
	doc.recordedAppend.Screenshots = []string{shot}
	doc.editor.SetText(appendTranscriptText(doc.editor.Text, screenshotFooter([]string{shot})))
	doc.recordedAppend.Combined = doc.editor.Text
	previous := doc.editor.Text

	test.Tap(doc.retranscribeButton)
	second := waitValue(t, started)
	if !doc.busy || !doc.retranscribeButton.Disabled() || doc.editor.Text != previous ||
		second.source != first.source || second.settings.ModelID() != first.settings.ModelID() ||
		second.request.CacheDir == "" || second.request.PersistLatest {
		t.Fatal("forced recognition reused the old attempt, cleared the editor, or changed the model")
	}
	releaseJobs()
	h.await(func() bool { return !doc.busy })
	want := appendTranscriptText("Texto anterior.\n\nVersão 2.", screenshotFooter([]string{shot}))
	if doc.editor.Text != want || doc.retranscribeButton.Disabled() || len(h.d.documents) != 1 ||
		doc.processEditor.Text != "Resposta já processada." || len(doc.processHistory) != 1 {
		t.Fatalf("retranscription duplicated old speech or changed another editor: %q", doc.editor.Text)
	}

	h.d.settings.Quality = "fast"
	test.Tap(doc.retranscribeButton)
	third := waitValue(t, started)
	h.await(func() bool { return !doc.busy })
	want = appendTranscriptText("Texto anterior.\n\nVersão 3.", screenshotFooter([]string{shot}))
	if third.request.CacheDir == second.request.CacheDir || third.settings.ModelID() == second.settings.ModelID() ||
		doc.editor.Text != want || doc.lastResult.ModelID != third.settings.ModelID() ||
		doc.lastAudio != "comparar.wav" || readResult(t, h.d.latestPath()).Text != want {
		t.Fatal("changing quality did not recognize the same audio again with the newly selected model")
	}
}

func TestRetranscribeFailurePreservesTextAndResumesItsAttemptAfterReopen(t *testing.T) {
	requests := make(chan transcript.Request, 3)
	h := newHarness(t, runnerFunc(func(_ context.Context, _ string, _ config.Settings, request transcript.Request) (
		transcript.Result, error,
	) {
		requests <- request
		return transcript.Result{}, &transcript.Error{
			Cause: errors.New("falha temporária"), PartialText: "Nova parte incompleta.",
		}
	}), nil)
	doc := h.d.document
	previous := resultFor("carregado.wav", "Transcrição completa anterior.")
	h.d.showDocumentResult(doc, previous, true)
	test.Tap(doc.retranscribeButton)
	attempt := waitValue(t, requests)
	h.await(func() bool { return !doc.busy })
	if doc.editor.Text != previous.Text || !doc.retryButton.Visible() || doc.retranscribeButton.Disabled() ||
		doc.lastResult.Text != previous.Text || attempt.CacheDir == "" {
		t.Fatal("a failed comparison replaced the previous complete text or lost its retry")
	}
	if err := h.d.saveWorkspace(); err != nil {
		t.Fatal(err)
	}
	restored := newHarness(t, runnerFunc(func(_ context.Context, source string, settings config.Settings, request transcript.Request) (
		transcript.Result, error,
	) {
		requests <- request
		if settings.TranscriptionProvider() != config.ProviderWhisper {
			t.Error("retry ignored the currently selected provider")
		}
		return resultFor(source, "Nova transcrição completa."), nil
	}), nil)
	restored.d.directory = h.d.directory
	if !restored.d.loadWorkspace() || restored.d.document.retranscribeButton.Disabled() {
		t.Fatal("reopening lost the loaded audio or retranscription control")
	}
	restored.d.settings.Provider = config.ProviderWhisper
	test.Tap(restored.d.retryButton)
	retry := waitValue(t, requests)
	restored.await(func() bool { return !restored.d.busy })
	if retry.CacheDir != attempt.CacheDir || restored.d.editor.Text != "Nova transcrição completa." ||
		restored.d.lastAudio != previous.Source || restored.d.retryButton.Visible() {
		t.Fatal("retry reused the original cache or appended rather than updating the current audio")
	}
}

func TestRetranscribeCancellationAndTabSwitchRejectLateResult(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	releaseJob := sync.OnceFunc(func() { close(release) })
	h := newHarness(t, runnerFunc(func(_ context.Context, source string, _ config.Settings, _ transcript.Request) (
		transcript.Result, error,
	) {
		started <- struct{}{}
		<-release
		return resultFor(source, "Resultado antigo que chegou depois."), nil
	}), nil)
	t.Cleanup(releaseJob)
	first := h.d.document
	h.d.showDocumentResult(first, resultFor("original.wav", "Texto preservado."), true)
	test.Tap(first.retranscribeButton)
	waitValue(t, started)
	second := h.d.addDocument()
	second.editor.SetText("Outra aba.")
	if !second.retranscribeButton.Disabled() {
		t.Fatal("another tab borrowed the first tab's audio")
	}
	h.d.tabs.Select(first.item)
	test.Tap(h.d.recordButton)
	if first.busy || first.editor.Text != "Texto preservado." || first.retranscribeButton.Disabled() {
		t.Fatal("cancelling retranscription did not promptly release the original tab")
	}
	h.d.tabs.Select(second.item)
	releaseJob()
	h.settle()
	if first.editor.Text != "Texto preservado." || second.editor.Text != "Outra aba." {
		t.Fatal("a cancelled comparison replaced an editor after a tab switch")
	}
}

func TestRetranscribeNewAudioResetsAttemptAndPreservesEditsDuringRecognition(t *testing.T) {
	release := make(chan struct{})
	releaseJob := sync.OnceFunc(func() { close(release) })
	requests := make(chan transcript.Request, 3)
	var calls atomic.Int32
	h := newHarness(t, runnerFunc(func(ctx context.Context, source string, _ config.Settings, request transcript.Request) (
		transcript.Result, error,
	) {
		requests <- request
		if calls.Add(1) == 1 {
			select {
			case <-release:
			case <-ctx.Done():
				return transcript.Result{}, ctx.Err()
			}
		}
		return resultFor(source, "Nova fala."), nil
	}), nil)
	t.Cleanup(releaseJob)
	doc := h.d.document
	h.d.showDocumentResult(doc, resultFor("original.wav", "Fala anterior."), true)
	test.Tap(doc.retranscribeButton)
	waitValue(t, requests)
	doc.editor.SetText("Minha correção durante a espera.")
	releaseJob()
	h.await(func() bool { return !doc.busy })
	if doc.editor.Text != "Minha correção durante a espera.\n\nNova fala." {
		t.Fatal("retranscription overwrote an edit made while recognition was running")
	}
	h.d.startImportedJob(doc, "outro.wav")
	next := waitValue(t, requests)
	h.await(func() bool { return !doc.busy })
	if next.CacheDir != "" || doc.transcriptionRun != "" || doc.lastAudio != "outro.wav" {
		t.Fatal("importing another audio retained the previous forced attempt")
	}
	h.d.newAudio()
	if !doc.retranscribeButton.Disabled() || doc.lastAudio != "" || doc.transcriptionRun != "" {
		t.Fatal("New audio left the retranscription button attached to old audio")
	}
}

func completedLiveForRetranscription(t *testing.T, h *testHarness, inline, team bool, seconds int) *liveRecording {
	t.Helper()
	doc := h.d.document
	archive := liveArchive{Prefix: "Texto anterior.", Labels: team, InlineScreenshots: inline}
	ids := []string{"microphone"}
	if team {
		ids = append(ids, "system")
	}
	for _, id := range ids {
		path := filepath.Join(recordingFolder(h.d.directory), "retranscrever-"+id+".wav")
		streamFixtureSeconds(t, path, seconds)
		snapshot := recorder.Snapshot{
			Path: path, SampleRate: 16000, WrittenSamples: uint64(seconds * 16000), Duration: float64(seconds),
		}
		archive.Tracks = append(archive.Tracks, recorder.Track{ID: id, Snapshot: snapshot})
		archive.NextID++
		archive.Entries = append(archive.Entries, live.Entry{
			ID: archive.NextID, Track: id, End: float64(seconds), LastSample: snapshot.WrittenSamples,
			Text: "Transcrição anterior de " + id + ".",
		})
	}
	archive.Source = archive.Tracks[0].Snapshot.Path
	archive.NextID++
	archive.Entries = append(archive.Entries, live.Entry{
		ID: archive.NextID, Start: 2, End: 2, Screenshot: true,
		Path: filepath.Join(h.d.directory, "screenshots", "contexto.png"),
	})
	previous := &liveRecording{liveArchive: archive}
	archive.Combined = live.Render(archive.Prefix, previous.textBlocks())
	doc.lastAudio = archive.Source
	doc.editor.SetText(archive.Combined)
	doc.lastResult = &transcript.Result{Source: archive.Source, Text: archive.Combined}
	h.d.restoreLiveArchive(doc, &archive)
	h.d.syncControls()
	return doc.live
}

func TestRetranscribeTeamAndStreamRecognizesBothTracksAndKeepsScreenshotPositions(t *testing.T) {
	for _, inline := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", inline), func(t *testing.T) {
			type call struct {
				source  string
				request transcript.Request
			}
			requests := make(chan call, 8)
			release := make(chan struct{})
			releaseJobs := sync.OnceFunc(func() { close(release) })
			h := newHarness(t, runnerFunc(func(ctx context.Context, source string, settings config.Settings, request transcript.Request) (
				transcript.Result, error,
			) {
				if request.OnPartial != nil || request.NativePCM || settings.ChunkSeconds < 120 || settings.SpeakerLabels {
					t.Error("retranscription used tiny streaming requests or anonymous speaker labels")
				}
				requests <- call{source, request}
				select {
				case <-release:
				case <-ctx.Done():
					return transcript.Result{}, ctx.Err()
				}
				text := "Minha nova fala."
				if strings.HasPrefix(filepath.Base(source), "system-") {
					text = "Nova explicação do tutorial."
				}
				name := filepath.Base(source)
				if strings.Contains(name, "-000000960000-") {
					text = "Minha pergunta seguinte."
					if strings.HasPrefix(filepath.Base(source), "system-") {
						text = "Outra explicação depois."
					}
				} else if !strings.Contains(name, "-000000000000-") {
					text = "Minha pergunta final."
					if strings.HasPrefix(filepath.Base(source), "system-") {
						text = "Outra explicação final."
					}
				}
				return resultFor(source, text), nil
			}), nil)
			t.Cleanup(releaseJobs)
			h.d.settings.Recovery.Concurrency = 4
			session := completedLiveForRetranscription(t, h, inline, true, 121)
			previous := h.d.editor.Text
			h.d.settings.Quality = "fast"
			test.Tap(h.d.document.retranscribeButton)
			var started []call
			for range 4 {
				started = append(started, waitValue(t, requests))
			}
			if !h.d.busy || h.d.editor.Text != previous || len(session.Entries) != 7 {
				t.Fatal("retranscription reused completed entries, omitted a track or changed the editor early")
			}
			for _, call := range started {
				if call.request.CacheDir == "" || call.request.CacheDir != started[0].request.CacheDir {
					t.Fatal("track recognition shared the previous transcription cache")
				}
			}
			if !inline {
				for _, id := range []string{"microphone", "system"} {
					long := false
					for _, entry := range session.Entries {
						long = long || entry.Track == id && entry.LastSample-entry.FirstSample == cloudTeamPartSeconds*16000
					}
					if !long {
						t.Fatal("offline screenshots unnecessarily split the larger team audio parts")
					}
				}
			}
			releaseJobs()
			h.await(func() bool { return !h.d.busy })
			h.settle()
			text := h.d.editor.Text
			shot := session.Entries[0].Path
			if !strings.HasPrefix(text, "Texto anterior.\n\n") || strings.Contains(text, "Transcrição anterior de") ||
				!strings.Contains(text, "Mic: Minha nova fala.") || !strings.Contains(text, "Sis: Nova explicação") ||
				strings.Count(text, shot) != 1 || h.d.document.retranscribeButton.Disabled() ||
				h.d.lastResult.ModelID != h.d.settings.ModelID() {
				t.Fatalf("retranscription lost a participant, retained old speech or duplicated a screenshot: %q", text)
			}
			if inline {
				lastSpeech := strings.LastIndex(text, "Outra explicação")
				if strings.Index(text, shot) > lastSpeech || strings.Contains(text, "Link dos screenshots") {
					t.Fatalf("a screenshot captured during Stream moved out of its place in the speech: %q", text)
				}
			} else if !strings.HasSuffix(text, screenshotFooter([]string{shot})) {
				t.Fatal("offline team retranscription lost the screenshot footer")
			}
		})
	}
}

func TestRetranscribeJoinUndoRestoresBothTracksAndAttempt(t *testing.T) {
	var calls atomic.Int32
	h := newHarness(t, runnerFunc(func(_ context.Context, source string, _ config.Settings, _ transcript.Request) (
		transcript.Result, error,
	) {
		calls.Add(1)
		return resultFor(source, "Fala reconhecida novamente."), nil
	}), nil)
	completedLiveForRetranscription(t, h, false, true, 4)
	first := h.d.document
	test.Tap(first.retranscribeButton)
	h.await(func() bool { return !first.busy })
	h.settle()
	run, source, text := first.transcriptionRun, first.lastAudio, first.editor.Text
	second := h.d.addDocument()
	second.editor.SetText("Texto de outra aba.")
	test.Tap(h.d.joinButton)
	if first.lastAudio != "" || first.transcriptionRun != "" || !first.retranscribeButton.Disabled() {
		t.Fatal("joined texts still offered recognition of an unrelated audio")
	}
	test.Tap(first.undoButton)
	if first.lastAudio != source || first.transcriptionRun != run || first.editor.Text != text ||
		first.live == nil || len(first.live.Tracks) != 2 || first.retranscribeButton.Disabled() {
		t.Fatal("undo lost the original team tracks or the comparison's resumable attempt")
	}
	test.Tap(first.retranscribeButton)
	h.await(func() bool { return !first.busy })
	h.settle()
	if calls.Load() != 4 || first.transcriptionRun == run || len(first.live.Tracks) != 2 {
		t.Fatal("recognition after undo omitted the system audio or reused the previous attempt")
	}
}

func TestRetranscribeStreamFailureAndReopenKeepsOriginalUntilLocalRetryFinishes(t *testing.T) {
	requests := make(chan transcript.Request, 3)
	h := newHarness(t, runnerFunc(func(_ context.Context, _ string, _ config.Settings, request transcript.Request) (
		transcript.Result, error,
	) {
		requests <- request
		return transcript.Result{}, errors.New("falha no novo modelo")
	}), nil)
	completedLiveForRetranscription(t, h, true, false, 4)
	previous := h.d.editor.Text
	test.Tap(h.d.document.retranscribeButton)
	attempt := waitValue(t, requests)
	waitValue(t, requests)
	h.await(func() bool { return !h.d.busy })
	h.settle()
	if h.d.editor.Text != previous || !h.d.retryButton.Visible() {
		t.Fatal("failed Stream comparison replaced the previous complete text")
	}
	if err := h.d.saveWorkspace(); err != nil {
		t.Fatal(err)
	}
	restored := newHarness(t, runnerFunc(func(_ context.Context, source string, settings config.Settings, request transcript.Request) (
		transcript.Result, error,
	) {
		requests <- request
		if request.OnPartial != nil || request.NativePCM || settings.TranscriptionProvider() != config.ProviderWhisper {
			t.Error("local retry used the streaming transport or ignored the selected provider")
		}
		return resultFor(source, "Fala nova em português."), nil
	}), nil)
	restored.d.directory = h.d.directory
	if !restored.d.loadWorkspace() || restored.d.editor.Text != previous {
		t.Fatal("reopening changed the original Stream transcription")
	}
	restored.d.settings.Provider = config.ProviderWhisper
	test.Tap(restored.d.retryButton)
	restored.await(func() bool { return !restored.d.busy })
	restored.settle()
	for range 2 {
		retry := waitValue(t, requests)
		if retry.CacheDir != attempt.CacheDir {
			t.Fatal("reopening lost the comparison's resumable cache")
		}
	}
	if strings.Contains(restored.d.editor.Text, "Transcrição anterior de") ||
		strings.Count(restored.d.editor.Text, "contexto.png") != 1 || restored.d.retryButton.Visible() ||
		!strings.HasPrefix(restored.d.editor.Text, "Texto anterior.\n\nFala nova") {
		t.Fatalf("local retry retained old speech or lost the screenshot: %q", restored.d.editor.Text)
	}
}
