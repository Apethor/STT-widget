package desktop

import (
	"math"
	"strings"
	"testing"
	"time"

	"ai-talker/internal/config"
	"ai-talker/internal/live"
	"ai-talker/internal/recorder"
	"ai-talker/internal/transcript"
)

func TestOfflinePartProgressExcludesScreenshotsAndDoesNotCountFailuresAsSuccess(t *testing.T) {
	h := newHarness(t, nil, nil)
	doc := h.d.document
	session := &liveRecording{
		liveArchive: liveArchive{
			TranscribeOnStop: true,
			Entries: []live.Entry{
				{ID: 1, Track: "microphone", End: 60, Text: "Fala concluída."},
				{ID: 2, Track: "system", End: 60, Pending: true},
				{ID: 3, Track: "system", Start: 60, End: 90, Error: "Falha temporária.", NeedsReview: true},
				{ID: 4, Screenshot: true, Start: 30, End: 30, Path: "captura.png"},
			},
		},
		doc: doc, stopped: true, running: map[uint64]bool{2: true}, progress: map[uint64]float64{2: .5},
	}
	doc.live, doc.busy = session, true
	d := h.d
	d.updateTeamProgress(session)
	d.renderDocument(doc)
	if !strings.Contains(d.recordState.Text, "1 de 3") ||
		!strings.Contains(d.status.Text, "1 em andamento") || !strings.Contains(d.status.Text, "1 com erro") ||
		math.Abs(d.progress.Value-.6) > .001 {
		t.Fatalf("o progresso contou screenshot/erro como concluídos: %q / %q / %.2f",
			d.recordState.Text, d.status.Text, d.progress.Value)
	}
	session.Entries[1].Pending = false
	session.Entries[1].Text = "Sistema concluído."
	delete(session.running, 2)
	d.updateTeamProgress(session)
	if !strings.Contains(doc.view.status, "2 de 3") || doc.view.progress >= 1 {
		t.Fatal("terminar uma parte exibiu 100% apesar de ainda haver uma falha", doc.view)
	}
	session.Entries[2].Error, session.Entries[2].Pending = "", true
	session.running[3] = true
	session.progress[3] = .25
	d.updateTeamProgress(session)
	if strings.Contains(doc.view.status, "com erro") || !strings.Contains(doc.view.status, "1 em andamento") ||
		math.Abs(doc.view.progress-.85) > .001 {
		t.Fatal("retomar perdeu o progresso das partes aceitas", doc.view)
	}
	session.Entries[2].Pending, session.Entries[2].NeedsReview = false, false
	delete(session.running, 3)
	d.updateTeamProgress(session)
	if !strings.Contains(doc.view.recordText, "3 de 3") || doc.view.progress != 1 {
		t.Fatal("concluir a última parte não terminou o progresso", doc.view)
	}
}

func TestFiveSecondTeamRecordingCountsSourcesInsteadOfTemporalChunks(t *testing.T) {
	h := newHarness(t, nil, nil)
	session := &liveRecording{
		liveArchive: liveArchive{
			TranscribeOnStop: true,
			Tracks: []recorder.Track{
				{ID: "microphone", Snapshot: recorder.Snapshot{SampleRate: 16000, WrittenSamples: 80000}},
				{ID: "system", Snapshot: recorder.Snapshot{SampleRate: 16000, WrittenSamples: 80000}},
			},
		},
		doc: h.d.document, settings: config.Default(), cursors: make(map[string]uint64),
		running: make(map[uint64]bool), progress: make(map[uint64]float64), stopped: true,
	}
	h.d.queueLiveParts(session, true)
	if len(session.Entries) != 2 {
		t.Fatal("a gravação curta produziu mais de uma parte por fonte")
	}
	for _, entry := range session.Entries {
		if entry.Start != 0 || entry.End != 5 || entry.FirstSample != 0 || entry.LastSample != 80000 {
			t.Fatal("uma fonte curta foi cortada em múltiplos intervalos", entry)
		}
	}
	session.Entries[0].Pending = false
	session.running[session.Entries[1].ID] = true
	h.d.updateTeamProgress(session)
	if h.d.view.recordText != "Fontes: 1 de 2 concluídas" ||
		!strings.Contains(h.d.view.status, "Microfone 1/1") || !strings.Contains(h.d.view.status, "Sistema 0/1") {
		t.Fatal("a interface apresentou duas fontes como um corte temporal", h.d.view)
	}
}

func TestFiveSecondStreamRecordingKeepsRealFourSecondPartAndTail(t *testing.T) {
	h := newHarness(t, nil, nil)
	session := &liveRecording{
		liveArchive: liveArchive{
			InlineScreenshots: true,
			Tracks:            []recorder.Track{{ID: "microphone", Snapshot: recorder.Snapshot{SampleRate: 16000, WrittenSamples: 80000}}},
		},
		doc: h.d.document, settings: config.Default(), cursors: make(map[string]uint64), stopped: true,
	}
	h.d.queueLiveParts(session, true)
	if len(session.Entries) != 2 || session.Entries[0].End != 4 ||
		session.Entries[1].Start != 4 || session.Entries[1].End != 5 || session.Entries[1].ContextSamples != 32000 {
		t.Fatal("o Stream perdeu o trecho de quatro segundos, a cauda ou o contexto")
	}
	h.d.updateTeamProgress(session)
	if h.d.view.recordText != "Trechos: 0 de 2 concluídos" {
		t.Fatal("o Stream apresentou trechos temporais como fontes", h.d.view)
	}
}

func TestLongFileProgressKeepsPartCountsWhileAwaitingTheEngineAndChangingTabs(t *testing.T) {
	now := time.Date(2026, time.October, 7, 12, 0, 0, 0, time.Local)
	h := newHarness(t, nil, func(deps *dependencies) { deps.now = func() time.Time { return now } })
	first := h.d.document
	first.busy, first.jobStarted = true, now
	h.d.showDocumentProgress(first, transcript.Progress{
		Stage: "transcribe", Message: "Transcrevendo · parte 3 de 10",
		Completed: 2, Total: 10, Fraction: .31,
	})
	if !strings.Contains(h.d.recordState.Text, "2 de 10") {
		t.Fatal("o progresso do arquivo inteiro não mostrou os trechos", h.d.recordState.Text)
	}
	now = now.Add(12 * time.Second)
	h.d.tick()
	if !strings.Contains(h.d.recordState.Text, "2/10 trechos") {
		t.Fatal("a espera apagou a contagem das partes concluídas", h.d.recordState.Text)
	}
	second := h.d.addGroup()
	h.d.showDocumentProgress(first, transcript.Progress{
		Stage: "retry", Message: "Nova tentativa da parte 3", Completed: 2, Total: 10, Fraction: .31,
	})
	if h.d.document != second || strings.Contains(h.d.recordState.Text, "2 de 10") {
		t.Fatal("o progresso de outra aba alterou a gravação selecionada")
	}
	h.d.tabs.Select(first.item)
	if !strings.Contains(h.d.recordState.Text, "2 de 10") || !strings.Contains(h.d.status.Text, "parte 3") {
		t.Fatal("voltar à aba perdeu o progresso da tarefa", h.d.recordState.Text, h.d.status.Text)
	}
}
