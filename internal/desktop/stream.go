package desktop

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ai-talker/internal/audio"
	"ai-talker/internal/config"
	"ai-talker/internal/live"
	"ai-talker/internal/recorder"
	"ai-talker/internal/store"
	"ai-talker/internal/transcript"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
)

const (
	streamPartSeconds    = 4.0
	streamContextSeconds = 2.0
	teamPartSeconds      = 120
)

// Only this archive is persisted. Contexts, workers and callbacks belong to the
// current desktop lifetime and cannot be resumed by deserializing a session.
type liveArchive struct {
	Source            string           `json:"source"`
	Prefix            string           `json:"prefix"`
	Combined          string           `json:"combined"`
	Entries           []live.Entry     `json:"entries"`
	Tracks            []recorder.Track `json:"tracks"`
	Labels            bool             `json:"labels"`
	Diarization       bool             `json:"diarization,omitempty"`
	InlineScreenshots bool             `json:"inline_screenshots"`
	// Older archives used InlineScreenshots for both placement and streaming.
	// New recordings can keep inline markers while recognizing only on stop.
	TranscribeOnStop bool         `json:"transcribe_on_stop,omitempty"`
	CaptureError     string       `json:"capture_error,omitempty"`
	NextID           uint64       `json:"next_id"`
	PreviousBlocks   []live.Block `json:"previous_blocks,omitempty"`
	PreviousEntries  []live.Entry `json:"previous_entries,omitempty"`
}

func (archive *liveArchive) isStream() bool {
	return archive.InlineScreenshots && !archive.TranscribeOnStop
}

type liveRecording struct {
	liveArchive
	doc       *document
	settings  config.Settings
	ctx       context.Context
	cancel    context.CancelFunc
	cursors   map[string]uint64
	running   map[uint64]bool
	progress  map[uint64]float64
	cuts      []float64
	blocks    []live.Block
	stopped   bool
	canceled  bool
	applying  bool
	show      bool
	completed bool
	started   time.Time
	epoch     uint64
	saveMu    sync.Mutex
	saveEpoch atomic.Uint64
}

func recordingTracks(r recording, mode string) []recorder.Track {
	if multi, ok := r.(interface{ Tracks() []recorder.Track }); ok {
		return multi.Tracks()
	}
	id, name := "microphone", "Você"
	if mode == config.CaptureSystem {
		id, name = "system", "Áudio do sistema"
	}
	return []recorder.Track{{ID: id, Label: name, Snapshot: r.Snapshot()}}
}

func (d *desktop) beginLiveCapture(doc *document, origin *captureOrigin, r recording) {
	settings := cloneSettings(origin.settings)
	if settings.TranscriptionProvider() != config.ProviderBedrock {
		settings.Capture.Stream = false
	}
	if !settings.Capture.Stream && settings.Capture.SourceMode() != config.CaptureTeam {
		doc.live = nil
		return
	}
	d.startCaptureTimeline(doc, origin, r)
}

// Ordinary recordings acquire a timeline on their first screenshot. Without
// screenshots they keep the existing whole-file recognition path.
func (d *desktop) startCaptureTimeline(doc *document, origin *captureOrigin, r recording) *liveRecording {
	settings := cloneSettings(origin.settings)
	if settings.TranscriptionProvider() != config.ProviderBedrock {
		settings.Capture.Stream = false
	}
	if settings.Capture.Stream {
		settings = settings.ForStream()
	}
	ctx, cancel := context.WithCancel(d.ctx)
	session := &liveRecording{
		liveArchive: liveArchive{
			Source: origin.recordingSource, Prefix: doc.editor.Text, Combined: doc.editor.Text,
			Labels:            settings.Capture.SourceMode() != config.CaptureMicrophone,
			Diarization:       settings.SupportsStructuredDiarization(),
			InlineScreenshots: true,
			TranscribeOnStop:  !settings.Capture.Stream,
			Tracks:            recordingTracks(r, settings.Capture.SourceMode()),
		},
		doc: doc, settings: settings, ctx: ctx, cancel: cancel,
		cursors: make(map[string]uint64), running: make(map[uint64]bool),
		show: settings.Capture.Stream, started: d.deps.now(),
	}
	doc.live = session
	origin.live = session
	if session.show {
		d.recordState.SetText("Gravando e transcrevendo em Stream.")
		d.status.SetText("A transcrição aparece por trechos; screenshots entram no instante da captura.")
	}
	return session
}

func (d *desktop) tickLiveCapture() {
	origin := d.captureOrigin
	if origin == nil || origin.live == nil || d.recorder == nil {
		return
	}
	session := origin.live
	if session.canceled {
		return
	}
	session.Tracks = recordingTracks(d.recorder, session.settings.Capture.SourceMode())
	if session.show {
		d.queueLiveParts(session, false)
		d.pumpLiveParts(session)
	}
}

func (d *desktop) queueLiveParts(session *liveRecording, final bool) {
	for _, track := range session.Tracks {
		snapshot := track.Snapshot
		if snapshot.SampleRate <= 0 {
			continue
		}
		cursor := session.cursors[track.ID]
		available := snapshot.WrittenSamples
		partSeconds := streamPartSeconds
		if !session.isStream() || session.doc.transcriptionRun != "" {
			partSeconds = offlinePartSeconds(session.settings)
		}
		for cursor < available {
			end := cursor + uint64(partSeconds*float64(snapshot.SampleRate))
			boundary := false
			startTime := track.Offset + float64(cursor)/float64(snapshot.SampleRate)
			// Tracks can start between sample boundaries. The logical start
			// after a cut must equal the marker, even when rounding the PCM
			// index would place it a fraction of a sample before that marker.
			for _, cut := range session.cuts {
				if cut >= track.Offset && uint64(math.Round((cut-track.Offset)*float64(snapshot.SampleRate))) == cursor {
					startTime = cut
				}
			}
			endTime := track.Offset + float64(end)/float64(snapshot.SampleRate)
			for _, cut := range session.cuts {
				if cut <= startTime+0.00001 {
					continue
				}
				sample := uint64(math.Round(max(0, cut-track.Offset) * float64(snapshot.SampleRate)))
				if sample > cursor && sample <= end {
					end, boundary = sample, true
					endTime = cut
					break
				}
			}
			if end > available {
				if final {
					end = available
					endTime = track.Offset + float64(end)/float64(snapshot.SampleRate)
				} else {
					break
				}
			}
			if !final && !boundary && end-cursor < uint64(partSeconds*float64(snapshot.SampleRate)) {
				break
			}
			session.NextID++
			var contextSamples uint64
			if session.isStream() || session.Diarization {
				contextSamples = min(cursor, uint64(streamContextSeconds*float64(snapshot.SampleRate)))
			}
			session.Entries = append(session.Entries, live.Entry{
				ID: session.NextID, Track: track.ID, Label: track.Label, Pending: true,
				Start: startTime, End: endTime,
				FirstSample: cursor, LastSample: end, ContextSamples: contextSamples,
				ContextSeconds: float64(contextSamples) / float64(snapshot.SampleRate),
			})
			cursor = end
		}
		session.cursors[track.ID] = cursor
	}
}

func (d *desktop) pumpLiveParts(session *liveRecording) {
	if session.canceled || session.ctx.Err() != nil {
		return
	}
	limit := session.settings.Parallelism()
	if session.settings.TranscriptionProvider() == config.ProviderWhisper {
		limit = 1
	}
	pending := slices.Clone(session.Entries)
	slices.SortStableFunc(pending, func(a, b live.Entry) int {
		if a.Start < b.Start {
			return -1
		}
		if a.Start > b.Start {
			return 1
		}
		return 0
	})
	for _, entry := range pending {
		if len(session.running) >= limit {
			break
		}
		if entry.Screenshot || !entry.Pending || session.running[entry.ID] {
			continue
		}
		trackIndex := slices.IndexFunc(session.Tracks, func(track recorder.Track) bool { return track.ID == entry.Track })
		if trackIndex < 0 {
			continue
		}
		snapshot := session.Tracks[trackIndex].Snapshot
		if snapshot.SampleRate <= 0 || entry.LastSample > snapshot.WrittenSamples {
			continue
		}
		session.running[entry.ID] = true
		d.updateTeamProgress(session)
		d.renderDocument(session.doc)
		settings := cloneSettings(session.settings)
		settings.OverlapSeconds = 0
		retranscribing := session.doc.transcriptionRun != ""
		streaming := session.isStream() && !retranscribing
		if streaming {
			settings.ChunkSeconds = 15
		}
		directory := filepath.Join(d.directory, "streams", filepath.Base(session.Source))
		name := fmt.Sprintf("%s-%012d-%012d", entry.Track, entry.FirstSample, entry.LastSample)
		if entry.ContextSamples > 0 {
			name += fmt.Sprintf("-context-%012d", entry.ContextSamples)
		}
		source := filepath.Join(directory, name+".wav")
		audioStart := entry.FirstSample - entry.ContextSamples
		nativePCM := streaming && snapshot.SampleRate == audio.SampleRate
		contextSamples := entry.ContextSamples * audio.SampleRate / uint64(snapshot.SampleRate)
		ctx, epoch := session.ctx, session.epoch
		var onPartial func(transcript.Partial)
		if streaming {
			onPartial = func(partial transcript.Partial) {
				d.post(func() {
					if session.doc.closed || session.doc.live != session || epoch != session.epoch ||
						session.canceled || !session.running[entry.ID] {
						return
					}
					index := slices.IndexFunc(session.Entries, func(item live.Entry) bool { return item.ID == entry.ID })
					if index < 0 || !session.Entries[index].Pending {
						return
					}
					session.Entries[index].Text = partial.Text
					session.doc.lastProgressAt = d.deps.now()
					d.renderLiveText(session)
				})
			}
		}
		cacheDirectory := d.transcriptionCacheDirectory(session.doc)
		d.workers.Go(func() {
			var result transcript.Result
			err := callOperation(func() error {
				if err := os.MkdirAll(directory, 0o700); err != nil {
					return fmt.Errorf("preparar os trechos do stream: %w", err)
				}
				if err := recorder.ExportSlice(ctx, snapshot, audioStart, entry.LastSample, source); err != nil {
					if !errors.Is(err, os.ErrExist) {
						return err
					}
				}
				var err error
				result, err = d.runner.Transcribe(ctx, source, settings, transcript.Request{
					OutputDir: filepath.Join(directory, "transcricoes"), Workers: 1, NativePCM: nativePCM,
					Streaming:       streaming,
					ContinueOnError: true,
					CacheDir:        cacheDirectory,
					OnPartial:       onPartial, NativeContextSamples: contextSamples,
					OnProgress: func(progress transcript.Progress) {
						if streaming {
							return
						}
						d.post(func() {
							if session.doc.closed || session.doc.live != session || epoch != session.epoch ||
								session.canceled || !session.running[entry.ID] {
								return
							}
							session.doc.lastProgressAt = d.deps.now()
							if session.progress == nil {
								session.progress = make(map[uint64]float64)
							}
							session.progress[entry.ID] = boundedLevel(progress.Fraction)
							d.updateTeamProgress(session)
							if progress.Stage == "retry" {
								session.doc.view.status = progress.Message + " · " + session.doc.view.recordText
							}
							d.renderDocument(session.doc)
						})
					},
				})
				return err
			})
			d.post(func() {
				if session.doc.closed || session.doc.live != session || epoch != session.epoch || session.canceled {
					return
				}
				delete(session.running, entry.ID)
				session.doc.lastProgressAt = d.deps.now()
				index := slices.IndexFunc(session.Entries, func(item live.Entry) bool { return item.ID == entry.ID })
				if index < 0 {
					return
				}
				item := &session.Entries[index]
				item.Pending = false
				if err != nil {
					item.Text = ""
					var failure *transcript.Error
					if errors.As(err, &failure) {
						item.Text = failure.PartialText
						item.Parts = slices.Clone(failure.Result.Parts)
					}
					item.Error = shortMessage(err.Error())
					item.NeedsReview = true
					session.doc.errorLabel.SetText(item.Error)
					session.doc.errorLabel.Show()
				} else {
					item.Text, item.Error, item.NeedsReview = result.Text, "", result.NeedsReview
					item.Parts = slices.Clone(result.Parts)
				}
				d.updateTeamProgress(session)
				d.renderLiveText(session)
				d.pumpLiveParts(session)
				d.completeLiveIfReady(session)
			})
		})
	}
}

func (d *desktop) updateTeamProgress(session *liveRecording) {
	if session.isStream() && session.doc.transcriptionRun == "" && !session.stopped {
		return
	}
	var duration, completed float64
	var total, done, running, failed, review int
	type sourceProgress struct{ done, total int }
	sources := make(map[string]sourceProgress)
	multipleParts := false
	for _, entry := range session.Entries {
		if entry.Screenshot || entry.Hidden {
			continue
		}
		seconds := max(0, entry.End-entry.Start)
		duration += seconds
		total++
		source := sources[entry.Track]
		source.total++
		multipleParts = multipleParts || source.total > 1
		if entry.Error != "" {
			failed++
		} else if !entry.Pending {
			done++
			source.done++
			completed += seconds
			if entry.NeedsReview {
				review++
			}
		} else {
			completed += seconds * session.progress[entry.ID]
			if session.running[entry.ID] {
				running++
			}
		}
		sources[entry.Track] = source
	}
	if duration > 0 {
		session.doc.view.progress = boundedLevel(completed / duration)
	}
	message := "Transcrevendo a gravação"
	if len(session.Tracks) > 1 {
		message += " em Equipe"
	}
	if session.doc.transcriptionRun != "" {
		message = "Retranscrevendo o áudio"
	} else if session.isStream() {
		message = "Concluindo o Stream"
	}
	session.doc.view.recordText = fmt.Sprintf("Trechos: %d de %d concluídos", done, total)
	if len(sources) > 1 && !multipleParts {
		session.doc.view.recordText = fmt.Sprintf("Fontes: %d de %d concluídas", done, total)
	}
	details := session.doc.view.recordText
	if len(sources) > 1 {
		for _, track := range []struct{ id, name string }{{"microphone", "Microfone"}, {"system", "Sistema"}} {
			if source, ok := sources[track.id]; ok {
				details += fmt.Sprintf(" · %s %d/%d", track.name, source.done, source.total)
			}
		}
	}
	if running > 0 {
		details += fmt.Sprintf(" · %d em andamento", running)
	}
	if failed > 0 {
		details += fmt.Sprintf(" · %d com erro", failed)
	}
	if review > 0 {
		details += fmt.Sprintf(" · %d para conferir", review)
	}
	session.doc.view.status = fmt.Sprintf("%s · %s · %.0f%%", message, details, session.doc.view.progress*100)
}

func (d *desktop) renderLiveText(session *liveRecording) {
	if session.doc.closed || session.doc.live != session || !session.show {
		return
	}
	doc := session.doc
	defer d.enterCurrentPrompt(doc)()
	blocks, retainingPrevious := session.displayBlocks(doc.transcriptionRun != "")
	reading := doc.transcriptReading
	follow := d.window.Canvas().Focused() != doc.editor
	if reading {
		scroll := doc.transcriptReadScroll
		follow = scroll.Offset.Y >= max(0, scroll.Content.Size().Height-scroll.Size().Height)-4
	}
	text := live.Render(session.Prefix, blocks)
	if doc.editor.Text != live.Render(session.Prefix, session.blocks) {
		text = live.Reconcile(doc.editor.Text, session.blocks, blocks)
	}
	session.blocks = blocks
	if retainingPrevious {
		session.PreviousBlocks = slices.Clone(blocks)
	}
	session.Combined = text
	if text == doc.editor.Text {
		return
	}
	session.applying = true
	doc.editor.SetText(text)
	session.applying = false
	d.updateTranscriptLinks(doc, reading)
	if follow {
		if doc.transcriptReading {
			doc.transcriptReadScroll.ScrollToBottom()
		} else {
			doc.editor.TypedKey(&fyne.KeyEvent{Name: fyne.KeyPageDown})
		}
	}
	if doc.recordedAppend != nil {
		doc.recordedAppend.Combined = text
		doc.recordedAppend.Content = text
	}
}

func (d *desktop) reserveLiveScreenshot(session *liveRecording, seconds float64) uint64 {
	session.NextID++
	session.Entries = append(session.Entries, live.Entry{
		ID: session.NextID, Start: seconds, End: seconds, Screenshot: true, Pending: true,
	})
	marker := session.NextID
	if session.InlineScreenshots {
		session.cuts = append(session.cuts, seconds)
		slices.Sort(session.cuts)
	}
	if session.show {
		d.tickLiveCapture()
	}
	return marker
}

func (d *desktop) attachLiveScreenshot(session *liveRecording, marker uint64, path string) {
	index := slices.IndexFunc(session.Entries, func(entry live.Entry) bool { return entry.ID == marker })
	if index < 0 || session.doc.live != session || session.canceled && !session.stopped {
		return
	}
	session.Entries[index].Path, session.Entries[index].Pending = path, false
	d.renderLiveText(session)
	if session.show && session.doc.live == session {
		d.updateTranscriptLinks(session.doc, true)
		session.doc.transcriptReadScroll.ScrollToBottom()
	}
	if session.completed {
		d.persistLiveResult(session)
	}
}

func (d *desktop) finishLiveCapture(session *liveRecording) {
	if !session.isStream() {
		// Ordinary recognition uses the motor selected when the user stops,
		// while the recording keeps its original sources and devices.
		capture := session.settings.Capture
		session.settings = cloneSettings(d.settings)
		session.settings.Capture = capture
	}
	session.Diarization = session.settings.SupportsStructuredDiarization()
	session.stopped, session.show = true, session.isStream()
	session.doc.busy = true
	session.doc.jobStarted, session.doc.lastProgressAt = d.deps.now(), d.deps.now()
	session.doc.jobCancel = func() { d.cancelLiveRecording(session) }
	session.doc.jobProvider = session.settings.TranscriptionProvider()
	session.doc.jobProgress = transcript.Progress{}
	session.doc.view.recordText = "Concluindo os trechos da gravação."
	session.doc.view.status = "O áudio foi salvo; aguardando a transcrição dos últimos trechos."
	d.queueLiveParts(session, true)
	d.renderLiveText(session)
	if !session.isStream() {
		session.doc.view.recordText = "Transcrevendo a gravação."
		if len(session.Tracks) > 1 {
			session.doc.view.recordText = "Transcrevendo a gravação em Equipe."
		}
		session.doc.view.status = "O áudio está salvo. O texto completo será acrescentado quando terminar."
		d.renderDocument(session.doc)
	}
	d.updateTeamProgress(session)
	d.renderDocument(session.doc)
	if !d.ensureTranscriptionSetup(session.doc, session.settings, func() { d.retryLiveRecording(session.doc) }) {
		d.cancelLiveRecording(session)
		session.doc.busy, session.doc.jobCancel = false, nil
		d.syncControls()
		return
	}
	d.pumpLiveParts(session)
	d.completeLiveIfReady(session)
}

func (d *desktop) removeLiveScreenshot(session *liveRecording, marker uint64) {
	session.Entries = slices.DeleteFunc(session.Entries, func(entry live.Entry) bool {
		return entry.Screenshot && entry.ID == marker
	})
	session.rebuildScreenshotCuts()
	if !session.stopped && !session.isStream() && session.settings.Capture.SourceMode() != config.CaptureTeam &&
		len(session.Entries) == 0 && d.captureOrigin != nil && d.captureOrigin.live == session {
		// Cancelling the only selection restores ordinary recognition.
		d.cancelLiveRecording(session)
		d.captureOrigin.live = nil
		session.doc.live = nil
		return
	}
	d.renderLiveText(session)
	if session.completed && !session.canceled {
		d.persistLiveResult(session)
	}
}

func (session *liveRecording) rebuildScreenshotCuts() {
	session.cuts = nil
	if !session.InlineScreenshots {
		return
	}
	for _, entry := range session.Entries {
		if entry.Screenshot {
			session.cuts = append(session.cuts, entry.Start)
		}
	}
	slices.Sort(session.cuts)
	session.cuts = slices.Compact(session.cuts)
}

func (d *desktop) completeLiveIfReady(session *liveRecording) {
	if !session.stopped || session.canceled || session.completed || len(session.running) > 0 {
		return
	}
	for _, entry := range session.Entries {
		if !entry.Screenshot && entry.Pending {
			return
		}
	}
	session.completed = true
	session.doc.busy, session.doc.jobCancel = false, nil
	if session.doc.transcriptionRun != "" {
		failed := slices.IndexFunc(session.Entries, func(entry live.Entry) bool {
			return !entry.Screenshot && entry.Error != ""
		})
		if failed >= 0 {
			session.show = false
			session.cancel()
			d.showDocumentError(session.doc, session.Entries[failed].Error, "")
			session.doc.retryButton.Show()
			d.saveLiveArchive(session)
			d.syncControls()
			d.finishWhenReady()
			return
		}
	}
	session.show = true
	d.renderLiveText(session)
	session.PreviousBlocks = nil
	session.PreviousEntries = nil
	d.updateTranscriptLinks(session.doc, true)
	session.cancel()
	d.persistLiveResult(session)
	d.syncControls()
	d.finishWhenReady()
}

func (d *desktop) persistLiveResult(session *liveRecording) {
	doc := session.doc
	defer d.enterCurrentPrompt(doc)()
	if doc.closed || doc.live != session {
		return
	}
	var duration float64
	review := session.CaptureError != ""
	hasSpeech := false
	var parts []transcript.Part
	for _, track := range session.Tracks {
		duration = max(duration, track.Offset+track.Snapshot.Duration)
	}
	for _, entry := range session.Entries {
		if entry.Screenshot {
			continue
		}
		review = review || !entry.Hidden && (entry.NeedsReview || entry.Error != "" || entry.Pending)
		hasSpeech = hasSpeech || !entry.Hidden && strings.TrimSpace(entry.Text) != ""
		parts = append(parts, transcript.Part{
			Index: len(parts), Start: entry.Start, Duration: entry.End - entry.Start,
			Text: entry.Text, NeedsReview: entry.NeedsReview || entry.Error != "" || entry.Pending,
			ReviewReason: entry.Error, DetectedSpeech: strings.TrimSpace(entry.Text) != "",
			Children: shiftedParts(entry.Parts, entry.Start-entry.ContextSeconds), Engine: entryEngine(entry, session.settings.ModelID()),
		})
	}
	result := transcript.Result{
		Text: doc.editor.Text, Source: session.Source, DurationSeconds: duration, Parts: parts,
		ElapsedSeconds: d.deps.now().Sub(session.started).Seconds(), NeedsReview: review,
		ModelID: session.settings.ModelID(), Region: session.settings.Region,
	}
	if session.settings.TranscriptionProvider() == config.ProviderWhisper {
		result.Region = "local"
	} else if session.settings.TranscriptionProvider() == config.ProviderAPI {
		result.Region = "api"
	}
	doc.lastResult = &result
	d.syncTranscriptionDetails(doc)
	d.recordTranscriptionPrompt(doc)
	doc.view.timer = clockText(duration)
	doc.view.importance = widget.MediumImportance
	doc.view.recordText = "Gravação transcrita."
	doc.view.status = "Falas e screenshots seguem a linha do tempo da gravação."
	doc.view.progress = 1
	if !hasSpeech && !review {
		doc.view.recordText = "Nenhuma fala detectada."
		doc.view.status = "O texto anterior e as screenshots foram mantidos."
	}
	if !review {
		doc.errorLabel.Hide()
		doc.retryButton.Hide()
	}
	if review {
		d.updateTeamProgress(session)
		summary := doc.view.recordText
		doc.view.recordText = "Há trechos para conferir."
		doc.view.status = summary + ". Tentar novamente retoma os trechos pendentes sem duplicar o texto."
		doc.retryButton.Show()
		if session.CaptureError != "" {
			doc.errorLabel.SetText(session.CaptureError)
			doc.errorLabel.Show()
		}
	}
	if err := d.persistDocumentResult(doc, result); err != nil {
		d.showDocumentError(doc, "Não foi possível salvar o resultado da gravação: "+err.Error(), "")
	}
	d.saveLiveArchive(session)
	d.renderDocument(doc)
}

func (d *desktop) saveLiveArchive(session *liveRecording) {
	doc := session.doc
	archive := session.archive()
	epoch := session.saveEpoch.Add(1)
	d.operation(func() error {
		session.saveMu.Lock()
		defer session.saveMu.Unlock()
		if epoch != session.saveEpoch.Load() {
			return nil
		}
		return store.WriteJSON(archive.Source+".stream.json", archive)
	}, func(err error) {
		if err != nil && doc.live == session && !doc.closed {
			d.showDocumentError(doc, "Não foi possível salvar a linha do tempo: "+err.Error(), "")
		}
	})
}

func (session *liveRecording) archive() *liveArchive {
	result := session.liveArchive
	result.Entries, result.Tracks = slices.Clone(result.Entries), slices.Clone(result.Tracks)
	result.PreviousBlocks = slices.Clone(result.PreviousBlocks)
	result.PreviousEntries = slices.Clone(result.PreviousEntries)
	return &result
}

func (session *liveRecording) textBlocks() []live.Block {
	if session.InlineScreenshots {
		return live.Blocks(session.Entries, session.Labels)
	}
	speech := slices.DeleteFunc(slices.Clone(session.Entries), func(entry live.Entry) bool { return entry.Screenshot })
	blocks := live.Blocks(speech, session.Labels)
	var paths []string
	for _, entry := range session.Entries {
		if entry.Screenshot && entry.Path != "" && !entry.Hidden {
			paths = append(paths, entry.Path)
		}
	}
	if footer := screenshotFooter(paths); footer != "" {
		blocks = append(blocks, live.Block{ID: math.MaxUint64, Text: footer})
	}
	return blocks
}

func (d *desktop) cancelLiveRecording(session *liveRecording) {
	session.cancel()
	session.canceled = true
	session.epoch++
	clear(session.running)
}

func (d *desktop) retryLiveRecording(doc *document) {
	session := doc.live
	if session == nil || doc.closed || d.capture != captureIdle {
		return
	}
	d.invalidateDocumentJob(doc)
	session.ctx, session.cancel = context.WithCancel(d.ctx)
	session.settings = cloneSettings(d.settings)
	if session.isStream() && doc.transcriptionRun == "" {
		session.settings = session.settings.ForStream()
	}
	session.Diarization = session.settings.SupportsStructuredDiarization()
	if !d.ensureTranscriptionSetup(doc, session.settings, func() { d.retryLiveRecording(doc) }) {
		session.cancel()
		return
	}
	session.canceled, session.completed, session.stopped, session.show = false, false, true,
		session.isStream() && doc.transcriptionRun == ""
	session.epoch++
	session.running = make(map[uint64]bool)
	session.progress = make(map[uint64]float64)
	for index := range session.Entries {
		entry := &session.Entries[index]
		if !entry.Screenshot && (entry.Pending || entry.Error != "" || entry.NeedsReview) {
			entry.Pending, entry.Error = true, ""
		}
	}
	session.rechunkPendingParts()
	doc.busy = true
	doc.jobStarted, doc.lastProgressAt = d.deps.now(), d.deps.now()
	doc.jobProvider = session.settings.TranscriptionProvider()
	doc.jobProgress = transcript.Progress{}
	doc.jobCancel = func() { d.cancelLiveRecording(session) }
	doc.errorLabel.Hide()
	doc.retryButton.Hide()
	doc.view.recordText = "Retomando os trechos pendentes."
	d.recoverLiveTracks(session, func(err error) {
		if err != nil {
			doc.busy, doc.jobCancel = false, nil
			d.cancelLiveRecording(session)
			d.showDocumentError(doc, "Não foi possível ler as trilhas preservadas: "+err.Error(), "")
			doc.retryButton.Show()
			return
		}
		d.pumpLiveParts(session)
		d.completeLiveIfReady(session)
	})
	d.renderDocument(doc)
	d.syncControls()
}

func (d *desktop) restoreLiveArchive(doc *document, archive *liveArchive) {
	if archive == nil || archive.Source != doc.lastAudio {
		return
	}
	if d.captureOrigin != nil && d.captureOrigin.document == doc && d.captureOrigin.live == doc.live && doc.live != nil {
		// Undo during capture keeps the same contexts and worker ownership.
		// Only visibility and the generated-text baseline are restored.
		session := doc.live
		if session.isStream() {
			visibility := make(map[uint64]bool, len(archive.Entries))
			for _, entry := range archive.Entries {
				visibility[entry.ID] = !entry.Hidden
			}
			for index := range session.Entries {
				session.Entries[index].Hidden = !visibility[session.Entries[index].ID]
			}
			previous := &liveRecording{liveArchive: *archive}
			session.blocks = previous.textBlocks()
		} else {
			// No generated blocks have been shown during offline capture.
			// Keep every photo, including those taken after the clear.
			session.blocks = nil
		}
		session.Prefix, session.Combined = archive.Prefix, doc.editor.Text
		return
	}
	session := &liveRecording{
		liveArchive: *archive, doc: doc, settings: cloneSettings(d.settings),
		cursors: make(map[string]uint64), running: make(map[uint64]bool),
		stopped: true, show: true, completed: true, canceled: true, started: d.deps.now(),
		ctx: d.ctx, cancel: func() {},
	}
	session.Entries, session.Tracks = slices.Clone(archive.Entries), slices.Clone(archive.Tracks)
	session.PreviousEntries = slices.Clone(archive.PreviousEntries)
	session.blocks = session.textBlocks()
	if len(archive.PreviousBlocks) > 0 {
		session.PreviousBlocks = slices.Clone(archive.PreviousBlocks)
		session.blocks = slices.Clone(archive.PreviousBlocks)
	}
	for _, entry := range session.Entries {
		if entry.Screenshot {
			continue
		}
		session.cursors[entry.Track] = max(session.cursors[entry.Track], entry.LastSample)
	}
	session.rebuildScreenshotCuts()
	doc.live = session
}

func (d *desktop) clearLiveText(doc *document) {
	if doc.live == nil {
		return
	}
	session := doc.live
	if d.captureDocument != doc || d.capture == captureIdle || session.isStream() {
		for index := range session.Entries {
			session.Entries[index].Hidden = true
		}
	}
	session.Prefix, session.Combined, session.blocks = "", "", nil
	session.PreviousBlocks, session.PreviousEntries = nil, nil
}
