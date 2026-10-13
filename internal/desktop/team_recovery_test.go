package desktop

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"ai-talker/internal/config"
	"ai-talker/internal/live"
	"ai-talker/internal/recorder"
	"ai-talker/internal/transcript"

	"fyne.io/fyne/v2/test"
)

func TestLongTeamTimeoutAfterReopenSplitsOnlyPendingAudioAndPreservesTextAndScreenshots(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.d.settings.Capture.Mode = config.CaptureTeam
	doc := h.d.document
	var tracks []recorder.Track
	originals := make(map[string][32]byte)
	for _, id := range []string{"microphone", "system"} {
		path := filepath.Join(recordingFolder(h.d.directory), "long-"+id+".wav")
		streamFixtureSeconds(t, path, 240)
		payload, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		originals[path] = sha256.Sum256(payload)
		tracks = append(tracks, recorder.Track{
			ID: id, Snapshot: recorder.Snapshot{Path: path, SampleRate: 16000, Duration: 240, WrittenSamples: 240 * 16000},
		})
	}
	shot := filepath.Join(h.d.directory, "screenshots", "contexto.png")
	archive := &liveArchive{
		Source: tracks[0].Snapshot.Path, Prefix: "Texto anterior.", Tracks: tracks,
		Labels: true, InlineScreenshots: true, TranscribeOnStop: true, NextID: 5,
		Entries: []live.Entry{
			{ID: 1, Track: "microphone", End: 120, LastSample: 120 * 16000, Text: "Minha primeira fala."},
			{ID: 2, Track: "system", End: 120, LastSample: 120 * 16000, Text: "Tutorial já reconhecido."},
			{ID: 3, Track: "system", Start: 120, End: 240, FirstSample: 120 * 16000, LastSample: 240 * 16000,
				Error: "A AWS demorou demais para responder.", NeedsReview: true},
			{ID: 4, Track: "microphone", Start: 120, End: 240, FirstSample: 120 * 16000, LastSample: 240 * 16000,
				Text: "Minha fala final preservada."},
			{ID: 5, Screenshot: true, Start: 120, End: 120, Path: shot},
		},
	}
	previous := &liveRecording{liveArchive: *archive}
	archive.Combined = live.Render(archive.Prefix, previous.textBlocks())
	doc.lastAudio = archive.Source
	doc.editor.SetText(archive.Combined)
	doc.processEditor.SetText("Resposta anterior preservada.")
	doc.lastResult = &transcript.Result{Source: archive.Source, Text: archive.Combined, NeedsReview: true}
	h.d.restoreLiveArchive(doc, archive)
	if err := h.d.saveWorkspace(); err != nil {
		t.Fatal(err)
	}
	started := make(chan string, 4)
	release := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	t.Cleanup(unblock)
	restored := newHarness(t, runnerFunc(func(ctx context.Context, source string, _ config.Settings, request transcript.Request) (
		transcript.Result, error,
	) {
		started <- filepath.Base(source)
		if request.OnPartial != nil || request.NativePCM {
			t.Error("offline recovery became Stream")
		}
		request.OnProgress(transcript.Progress{Stage: "retry", Message: "AWS · nova tentativa 2 de 3; progresso salvo.", Fraction: .5})
		select {
		case <-release:
		case <-ctx.Done():
			return transcript.Result{}, ctx.Err()
		}
		text := "Tutorial recuperado antes."
		if strings.Contains(source, "-000002880000-") {
			text = "Tutorial recuperado depois."
		}
		return resultFor(source, text), nil
	}), nil)
	restored.d.directory = h.d.directory
	restored.d.settings.Capture.Mode = config.CaptureTeam
	if !restored.d.loadWorkspace() || !restored.d.retryButton.Visible() {
		t.Fatal("reopening lost the failed recording or its recovery action")
	}
	restoredDoc := restored.d.document
	test.Tap(restored.d.retryButton)
	calls := []string{waitValue(t, started), waitValue(t, started)}
	slices.Sort(calls)
	wantCalls := []string{
		"system-000001920000-000002880000.wav",
		"system-000002880000-000003840000.wav",
	}
	if !slices.Equal(calls, wantCalls) {
		t.Fatalf("recovery repeated completed audio or kept the two-minute failing range: %v", calls)
	}
	restored.await(func() bool { return strings.Contains(restoredDoc.view.status, "nova tentativa 2 de 3") })
	if restoredDoc.editor.Text != archive.Combined || restoredDoc.errorLabel.Visible() {
		t.Fatal("retry exposed provisional offline text or a red error while still recovering")
	}
	unblock()
	restored.await(func() bool { return !restoredDoc.busy })
	restored.settle()
	text := restoredDoc.editor.Text
	for _, preserved := range []string{"Texto anterior.", "Minha primeira fala.", "Tutorial já reconhecido.", "Minha fala final preservada."} {
		if strings.Count(text, preserved) != 1 {
			t.Fatalf("retry lost or repeated previous text: %q", text)
		}
	}
	if strings.Count(text, shot) != 1 || !strings.Contains(text, "Tutorial recuperado antes.") ||
		!strings.Contains(text, "Tutorial recuperado depois.") || strings.Index(text, shot) > strings.Index(text, "Tutorial recuperado antes.") ||
		restoredDoc.live.needsRetry() || restoredDoc.lastResult.DurationSeconds != 240 ||
		restoredDoc.processEditor.Text != "Resposta anterior preservada." {
		t.Fatalf("recovery lost the screenshot position, final coverage or completion: %q", text)
	}
	select {
	case extra := <-started:
		t.Fatalf("completed audio was retranscribed: %s", extra)
	default:
	}
	for path, original := range originals {
		payload, err := os.ReadFile(path)
		if err != nil || sha256.Sum256(payload) != original {
			t.Fatal("recovery modified an original recording")
		}
	}
	if err := restored.d.saveWorkspace(); err != nil {
		t.Fatal(err)
	}
	if !restored.d.loadWorkspace() || restored.d.document.live.needsRetry() ||
		restored.d.editor.Text != text || !restored.d.validLiveArchive(restored.d.document.live.archive()) {
		t.Fatal("the recovered smaller ranges did not survive another reopen")
	}
}

func TestRechunkPendingTeamKeepsSampleOffsetsAndLocalParts(t *testing.T) {
	for _, provider := range []string{config.ProviderBedrock, config.ProviderWhisper} {
		t.Run(provider, func(t *testing.T) {
			settings := config.Default()
			settings.Provider = provider
			const offset = 0.025875
			session := &liveRecording{
				settings: settings,
				liveArchive: liveArchive{
					Source: "synthetic.wav", TranscribeOnStop: true, NextID: 2,
					Tracks: []recorder.Track{{ID: "system", Offset: offset, Snapshot: recorder.Snapshot{SampleRate: 48000}}},
					Entries: []live.Entry{
						{ID: 1, Track: "system", Start: offset, End: 60 + offset, LastSample: 60 * 48000, Text: "Pronta."},
						{ID: 2, Track: "system", Start: 60 + offset, End: 180 + offset, FirstSample: 60 * 48000,
							LastSample: 180 * 48000, Pending: true},
					},
				},
			}
			session.rechunkPendingParts()
			if provider == config.ProviderWhisper {
				if len(session.Entries) != 2 {
					t.Fatal("cloud retry policy changed local model parts")
				}
				return
			}
			if len(session.Entries) != 3 || session.Entries[0].Text != "Pronta." ||
				session.Entries[1].FirstSample != 60*48000 || session.Entries[1].LastSample != 120*48000 ||
				session.Entries[2].FirstSample != 120*48000 || session.Entries[2].LastSample != 180*48000 ||
				session.Entries[1].End != session.Entries[2].Start || session.Entries[2].End != 180+offset {
				t.Fatal("pending-only rechunking lost exact sample coverage or its source offset")
			}
		})
	}
}
