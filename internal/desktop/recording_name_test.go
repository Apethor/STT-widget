package desktop

import (
	"path/filepath"
	"strings"
	"testing"

	"ai-talker/internal/recorder"
	"ai-talker/internal/store"

	"fyne.io/fyne/v2/test"
)

func TestRecordingCaptionCopiesBothActualTeamPathsIncludingImportedManifest(t *testing.T) {
	copied := make(chan string, 2)
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.copy = func(text string) error { copied <- text; return nil }
	})
	mic := historyAudio(t, h.d.directory, "20261008-043000-mic.wav")
	system := historyAudio(t, h.d.directory, "20261008-043000-sis.wav")
	tracks := []recorder.Track{
		{ID: "microphone", Snapshot: recorder.Snapshot{Path: mic}},
		{ID: "system", Snapshot: recorder.Snapshot{Path: system}},
	}
	h.d.lastAudio = mic
	h.d.live = &liveRecording{liveArchive: liveArchive{Source: mic, Tracks: tracks}}
	h.d.syncRecordingName()
	if h.d.caption.Text != "04:30 · 08/10" || !h.d.copyAudioPath.Visible() ||
		h.d.copyAudioPath.MinSize().Width > 22 {
		t.Fatal("o gravador não apresentou o nome real com a cópia compacta")
	}
	test.Tap(h.d.copyAudioPath)
	if text := waitValue(t, copied); text != mic+"\r\n"+system {
		t.Fatal("copiar Equipe não preservou ambos os caminhos absolutos", text)
	}
	h.settle()
	h.d.live = nil
	if err := store.WriteJSON(mic+".team.json", recorder.TeamManifest{Version: 1, Tracks: tracks}); err != nil {
		t.Fatal(err)
	}
	test.Tap(h.d.copyAudioPath)
	if text := waitValue(t, copied); text != mic+"\r\n"+system {
		t.Fatal("importar/reabrir perdeu a segunda fonte na cópia", text)
	}
	h.settle()
	h.d.lastAudio = ""
	h.d.syncRecordingName()
	if h.d.caption.Text != "Novo áudio" || h.d.copyAudioPath.Visible() ||
		strings.Contains(h.d.sourceLabel.Text, filepath.Base(mic)) {
		t.Fatal("o estado vazio manteve um nome de áudio carregado")
	}
}
