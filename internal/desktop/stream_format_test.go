package desktop

import (
	"context"
	"path/filepath"
	"testing"

	"ai-talker/internal/config"
	"ai-talker/internal/live"
	"ai-talker/internal/recorder"
	"ai-talker/internal/transcript"
)

func TestStreamAt48kUsesCodecsAndConvertsItsContextToDecodedSamples(t *testing.T) {
	observed := make(chan transcript.Request, 1)
	h := newHarness(t, runnerFunc(func(_ context.Context, source string, _ config.Settings, request transcript.Request) (
		transcript.Result, error,
	) {
		observed <- request
		return resultFor(source, "Fala do dispositivo."), nil
	}), nil)
	source := filepath.Join(recordingFolder(h.d.directory), "fallback.wav")
	streamFixture(t, source)
	ctx, cancel := context.WithCancel(h.d.ctx)
	session := &liveRecording{
		liveArchive: liveArchive{
			Source: source, InlineScreenshots: true, NextID: 1,
			Tracks: []recorder.Track{{ID: "microphone", Snapshot: recorder.Snapshot{
				Path: source, SampleRate: 48000, WrittenSamples: 4 * 48000, Duration: 4,
			}}},
			Entries: []live.Entry{{
				ID: 1, Track: "microphone", Start: 2, End: 4,
				FirstSample: 2 * 48000, LastSample: 4 * 48000, ContextSamples: 2 * 48000, Pending: true,
			}},
		},
		doc: h.d.document, settings: cloneSettings(h.d.settings), ctx: ctx, cancel: cancel,
		cursors: map[string]uint64{"microphone": 4 * 48000}, running: make(map[uint64]bool), started: h.d.deps.now(),
	}
	h.d.document.live = session
	h.d.finishLiveCapture(session)
	request := waitValue(t, observed)
	h.await(func() bool { return !h.d.busy })
	if request.NativePCM || request.OnPartial == nil || request.NativeContextSamples != 2*16000 {
		t.Fatal("48 kHz stream used the 16 kHz-only shortcut or counted context at the wrong rate")
	}
}
