package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"ai-talker/internal/live"
	"ai-talker/internal/platform"
	"ai-talker/internal/recorder"
	"ai-talker/internal/store"
	"ai-talker/internal/transcript"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
)

const (
	cleanupScreenshots    = "Screenshots"
	cleanupRecordings     = "Gravações"
	cleanupTranscriptions = "Transcrições"
	cleanupHistory        = "Histórico de áudio"

	cleanupCustomPeriod = "Intervalo personalizado"
)

type cleanupItem struct {
	ID          string
	Category    string
	Name        string
	Source      string
	Paths       []string
	DocID       uint64
	HistoryID   uint64
	HistoryItem bool
	Index       int
	Current     bool
	Modified    time.Time
	Size        int64
	InUse       string
	Record      []byte
}

type cleanupSeed struct {
	item cleanupItem
}

type cleanupForm struct {
	category *widget.Select
	period   *widget.Select
	from     *widget.Entry
	until    *widget.Entry
	rangeRow *fyne.Container
	all      *widget.Check
	list     *fyne.Container
	scroll   *container.Scroll
	summary  *widget.Label
	status   *widget.Label
	remove   *widget.Button
	items    []cleanupItem
	selected map[string]bool
	checks   map[string]*widget.Check
	epoch    uint64
	busy     bool
	syncing  bool
}

func (d *desktop) buildCleanupSettings(form *settingsForm) fyne.CanvasObject {
	cleanup := &cleanupForm{
		category: widget.NewSelect(
			[]string{cleanupScreenshots, cleanupRecordings, cleanupTranscriptions, cleanupHistory}, nil,
		),
		period: widget.NewSelect(
			[]string{"Últimos 7 dias", "Últimos 30 dias", "Últimos 90 dias", cleanupCustomPeriod}, nil,
		),
		from: widget.NewEntry(), until: widget.NewEntry(),
		all:      widget.NewCheck(uiText("Selecionar todos os itens"), nil),
		list:     container.NewVBox(),
		summary:  widget.NewLabel(""),
		status:   widget.NewLabel(""),
		selected: make(map[string]bool),
		checks:   make(map[string]*widget.Check),
	}
	form.cleanup = cleanup
	setUISelected(cleanup.category, cleanupScreenshots)
	setUISelected(cleanup.period, "Últimos 30 dias")
	cleanup.from.SetPlaceHolder(uiText("De · AAAA-MM-DD"))
	cleanup.until.SetPlaceHolder(uiText("Até · AAAA-MM-DD"))
	cleanup.from.SetText(d.deps.now().AddDate(0, 0, -30).Format("2006-01-02"))
	cleanup.until.SetText(d.deps.now().Format("2006-01-02"))
	cleanup.status.Wrapping = fyne.TextWrapWord
	cleanup.status.Importance = widget.DangerImportance
	cleanup.remove = widget.NewButton(uiText("Mover selecionados para a Lixeira"), d.guard(func() {
		d.confirmCleanup(cleanup)
	}))
	cleanup.remove.Importance = widget.HighImportance
	cleanup.remove.Disable()
	cleanup.all.OnChanged = func(checked bool) {
		if cleanup.syncing {
			return
		}
		cleanup.syncing = true
		for id, check := range cleanup.checks {
			selected := checked && !check.Disabled()
			cleanup.selected[id] = selected
			check.SetChecked(selected)
		}
		cleanup.syncing = false
		d.updateCleanupSummary(cleanup)
	}
	cleanup.category.OnChanged = func(string) { d.reloadCleanupItems(cleanup) }
	cleanup.period.OnChanged = func(value string) {
		if value == cleanupCustomPeriod {
			cleanup.rangeRow.Show()
		} else {
			cleanup.rangeRow.Hide()
		}
		d.reloadCleanupItems(cleanup)
	}
	cleanup.from.OnSubmitted = func(string) { d.reloadCleanupItems(cleanup) }
	cleanup.until.OnSubmitted = func(string) { d.reloadCleanupItems(cleanup) }
	cleanup.rangeRow = container.NewHBox(cleanup.from, cleanup.until)
	cleanup.rangeRow.Hide()
	controls := container.NewVBox(
		settingLabel(uiText("Categoria")), cleanup.category,
		settingLabel(uiText("Período")), cleanup.period,
		cleanup.rangeRow,
		cleanup.all,
	)
	cleanup.scroll = container.NewVScroll(cleanup.list)
	notice := widget.NewLabel("A lista inclui somente dados internos do STT-widget. Exportações em transcricoes e arquivos fora das pastas internas ficam preservados.")
	notice.Wrapping = fyne.TextWrapWord
	return container.NewBorder(
		container.NewVBox(controls, notice),
		container.NewVBox(cleanup.summary, cleanup.status, container.NewHBox(layout.NewSpacer(), cleanup.remove)),
		nil, nil, cleanup.scroll,
	)
}

func (d *desktop) cleanupAllowed() bool {
	return !d.closing && !d.discardDialog && !d.tabsDialog
}

// Read usage on the UI thread. The scan worker measures files, never widgets
// or the live capture/player state.
func (d *desktop) cleanupItemInUse(item cleanupItem) string {
	if item.Category == cleanupRecordings {
		if message := d.voiceAudioInUse(item); message != "" {
			return message
		}
	}
	contains := func(path string) bool {
		return sameAudio(path, item.Source) || slices.ContainsFunc(item.Paths, func(candidate string) bool {
			return sameAudio(path, candidate)
		})
	}
	if item.Category == cleanupScreenshots {
		for editor := range d.screenshotEditors {
			if !editor.closed && contains(editor.path) {
				return "Em uso no editor de screenshot; feche o editor para limpar."
			}
		}
	}
	if item.Category == cleanupRecordings && d.playback != nil {
		if contains(d.playback.source) {
			return "Em uso no player; pare a reprodução para limpar."
		}
		if doc := d.playback.document; doc != nil && doc.live != nil {
			for _, track := range doc.live.Tracks {
				if contains(track.Snapshot.Path) {
					return "Em uso no player; pare a reprodução para limpar."
				}
			}
		}
	}
	if d.capture != captureIdle && d.captureOrigin != nil {
		if d.captureOrigin.document != nil && item.DocID == d.captureOrigin.document.id || contains(d.captureOrigin.recordingSource) {
			return "Em uso na gravação; encerre a captura para limpar."
		}
		if d.recorder != nil {
			for _, track := range recordingTracks(d.recorder, d.captureOrigin.settings.Capture.SourceMode()) {
				if contains(track.Snapshot.Path) {
					return "Em uso na gravação; encerre a captura para limpar."
				}
			}
		}
	}
	for _, doc := range d.documents {
		belongs := item.DocID == doc.id || contains(doc.lastAudio)
		if doc.live != nil {
			belongs = belongs || slices.ContainsFunc(doc.live.Tracks, func(track recorder.Track) bool {
				return contains(track.Snapshot.Path)
			}) || slices.ContainsFunc(doc.live.Entries, func(entry live.Entry) bool {
				return entry.Screenshot && contains(entry.Path)
			})
		}
		if belongs && (doc.busy || doc.processBusy || doc.audioLoading) {
			return "Em uso em uma tarefa; aguarde sua conclusão para limpar."
		}
		if item.Category == cleanupTranscriptions && item.DocID == 0 &&
			(doc.busy || doc.live != nil && len(doc.live.running) > 0) {
			// An unfinished recognition may not have published its JobID yet.
			return "Cache em uso em uma transcrição; aguarde sua conclusão para limpar."
		}
	}
	return ""
}

func (d *desktop) reloadCleanupItems(form *cleanupForm) {
	if form == nil || form.busy || d.settingsForm == nil || d.settingsForm.cleanup != form {
		return
	}
	if !d.cleanupAllowed() {
		form.status.SetText("Pare gravações e aguarde as tarefas terminarem antes de limpar dados.")
		form.remove.Disable()
		return
	}
	fromText, untilText := form.from.Text, form.until.Text
	from, until, cutoff, err := d.cleanupPeriod(uiSelected(form.period), fromText, untilText, d.deps.now())
	if err != nil {
		form.status.SetText(err.Error())
		form.remove.Disable()
		return
	}
	form.epoch++
	epoch := form.epoch
	form.busy = true
	form.status.SetText("Lendo os itens internos…")
	form.remove.Disable()
	form.list.Objects = []fyne.CanvasObject{widget.NewLabel("Lendo…")}
	form.list.Refresh()

	var seeds []cleanupSeed
	switch uiSelected(form.category) {
	case cleanupTranscriptions:
		seeds = d.cleanupTranscriptSeeds()
	case cleanupHistory:
		for _, doc := range d.documents {
			if doc.lastAudio != "" && !d.preserveAudioHistory(doc) {
				form.busy = false
				form.status.SetText("Não foi possível preparar o histórico atual para limpeza.")
				return
			}
		}
		seeds = d.cleanupHistorySeeds()
	}
	category := uiSelected(form.category)
	period := uiSelected(form.period)
	now := d.deps.now()
	var loadedItems []cleanupItem
	d.operation(func() error {
		var items []cleanupItem
		switch category {
		case cleanupScreenshots, cleanupRecordings:
			items, err = d.scanCleanupFiles(category)
			if err != nil {
				return err
			}
		case cleanupTranscriptions:
			items, err = d.measureCleanupSeeds(seeds)
			if err != nil {
				return err
			}
			remaining, scanErr := d.scanUnreferencedTranscriptFiles(items)
			if scanErr != nil {
				return scanErr
			}
			items = append(items, remaining...)
		case cleanupHistory:
			items, err = d.measureCleanupSeeds(seeds)
			if err != nil {
				return err
			}
			archive, archiveErr := d.readEvaluationArchive()
			if archiveErr != nil {
				return archiveErr
			}
			for _, entry := range archive.Entries {
				payload, _ := json.Marshal(entry)
				items = append(items, cleanupItem{
					ID:       fmt.Sprintf("closed-evaluation:%d", entry.TabID),
					Category: cleanupHistory, Name: fmt.Sprintf("Aba %d · avaliação fechada", entry.TabID),
					HistoryID: entry.TabID, HistoryItem: true, Modified: entry.ClosedAt, Size: int64(len(payload)),
					Record: payload,
				})
			}
		}
		items = slices.DeleteFunc(items, func(item cleanupItem) bool {
			return !cleanupPeriodMatches(item.Modified, period, from, until, cutoff, now)
		})
		sort.SliceStable(items, func(i, j int) bool {
			if items[i].Modified.Equal(items[j].Modified) {
				return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
			}
			return items[i].Modified.After(items[j].Modified)
		})
		loadedItems = items
		return nil
	}, func(scanErr error) {
		if d.settingsForm == nil || d.settingsForm.cleanup != form || form.epoch != epoch {
			return
		}
		form.busy = false
		if uiSelected(form.category) != category || uiSelected(form.period) != period ||
			form.from.Text != fromText || form.until.Text != untilText {
			d.reloadCleanupItems(form)
			return
		}
		form.items = loadedItems
		if scanErr != nil {
			form.status.SetText("Não foi possível listar os dados internos: " + shortMessage(scanErr.Error()))
			form.remove.Disable()
			return
		}
		form.selected = make(map[string]bool, len(form.items))
		form.checks = make(map[string]*widget.Check, len(form.items))
		form.syncing = true
		form.all.SetChecked(len(form.items) > 0)
		form.syncing = false
		d.renderCleanupItems(form)
	})
}

func (d *desktop) cleanupPeriod(choice, fromText, untilText string, now time.Time) (time.Time, time.Time, time.Time, error) {
	switch choice {
	case "Últimos 7 dias":
		return time.Time{}, time.Time{}, now.AddDate(0, 0, -7), nil
	case "Últimos 30 dias":
		return time.Time{}, time.Time{}, now.AddDate(0, 0, -30), nil
	case "Últimos 90 dias":
		return time.Time{}, time.Time{}, now.AddDate(0, 0, -90), nil
	case cleanupCustomPeriod:
		from, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(fromText), time.Local)
		if err != nil {
			return time.Time{}, time.Time{}, time.Time{}, errors.New("informe a data inicial no formato AAAA-MM-DD")
		}
		until, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(untilText), time.Local)
		if err != nil {
			return time.Time{}, time.Time{}, time.Time{}, errors.New("informe a data final no formato AAAA-MM-DD")
		}
		if until.Before(from) {
			return time.Time{}, time.Time{}, time.Time{}, errors.New("a data final deve ser igual ou posterior à inicial")
		}
		return from, until.AddDate(0, 0, 1), time.Time{}, nil
	default:
		return time.Time{}, time.Time{}, now.AddDate(0, 0, -30), nil
	}
}

func cleanupPeriodMatches(modified time.Time, choice string, from, until, cutoff, now time.Time) bool {
	if modified.IsZero() {
		return false
	}
	switch choice {
	case cleanupCustomPeriod:
		return !modified.Before(from) && modified.Before(until)
	default:
		return !modified.Before(cutoff) && !modified.After(now)
	}
}

func (d *desktop) cleanupHistorySeeds() []cleanupSeed {
	var seeds []cleanupSeed
	for _, doc := range d.documents {
		for index, snapshot := range doc.audioHistory {
			if snapshot.HistoryRemoved {
				continue
			}
			payload, _ := json.Marshal(snapshot)
			seeds = append(seeds, cleanupSeed{item: cleanupItem{
				ID:       fmt.Sprintf("history:%d:%s", doc.id, strings.ToLower(filepath.Clean(snapshot.Source))),
				Category: cleanupHistory, Name: filepath.Base(snapshot.Source), Source: snapshot.Source,
				DocID: doc.id, Index: index, Current: sameAudio(doc.lastAudio, snapshot.Source),
				Modified: snapshot.SavedAt, Size: int64(len(payload)), Record: payload,
			}})
		}
	}
	return seeds
}

func (d *desktop) cleanupTranscriptSeeds() []cleanupSeed {
	var seeds []cleanupSeed
	for _, doc := range d.documents {
		foundCurrent := false
		for index, snapshot := range doc.audioHistory {
			if snapshot.HistoryRemoved {
				continue
			}
			current := sameAudio(doc.lastAudio, snapshot.Source)
			foundCurrent = foundCurrent || current
			if current {
				savedAt := snapshot.SavedAt
				snapshot = d.snapshotAudio(doc)
				snapshot.SavedAt = savedAt
			}
			payload, _ := json.Marshal(snapshot)
			item := cleanupItem{
				ID:       fmt.Sprintf("transcript:%d:%s", doc.id, strings.ToLower(filepath.Clean(snapshot.Source))),
				Category: cleanupTranscriptions, Name: filepath.Base(snapshot.Source), Source: snapshot.Source,
				DocID: doc.id, Index: index, Current: current, Modified: snapshot.SavedAt,
				Size: int64(len(payload)), Record: payload,
			}
			item.Paths = append(item.Paths, d.transcriptCachePaths(snapshot.Result, snapshot.TranscriptionRun)...)
			d.appendCleanupLastResults(&item)
			seeds = append(seeds, cleanupSeed{item: item})
		}
		if foundCurrent || doc.lastAudio == "" && strings.TrimSpace(doc.editor.Text) == "" {
			continue
		}
		snapshot := d.snapshotAudio(doc)
		payload, _ := json.Marshal(snapshot)
		name := filepath.Base(snapshot.Source)
		if snapshot.Source == "" {
			name = doc.item.Text + " · texto"
		}
		item := cleanupItem{
			ID:       fmt.Sprintf("transcript:%d:%s", doc.id, strings.ToLower(filepath.Clean(snapshot.Source))),
			Category: cleanupTranscriptions, Name: name, Source: snapshot.Source,
			DocID: doc.id, Index: -1, Current: true, Modified: d.deps.now(),
			Size: int64(len(payload)), Record: payload,
		}
		item.Paths = append(item.Paths, d.transcriptCachePaths(snapshot.Result, snapshot.TranscriptionRun)...)
		d.appendCleanupLastResults(&item)
		seeds = append(seeds, cleanupSeed{item: item})
	}
	return seeds
}

func (d *desktop) appendCleanupLastResults(item *cleanupItem) {
	candidates := []string{d.latestPath()}
	for _, doc := range d.documents {
		if doc.id == item.DocID {
			candidates = append(candidates, d.documentLatestPath(doc))
		}
	}
	for _, path := range candidates {
		var result transcript.Result
		if store.ReadJSON(path, &result) == nil && sameAudio(result.Source, item.Source) &&
			!slices.Contains(item.Paths, path) {
			item.Paths = append(item.Paths, path)
		}
	}
}

func (d *desktop) scanUnreferencedTranscriptFiles(existing []cleanupItem) ([]cleanupItem, error) {
	seen := make(map[string]bool)
	for _, item := range existing {
		for _, path := range item.Paths {
			seen[strings.ToLower(filepath.Clean(path))] = true
		}
	}
	var items []cleanupItem
	appendFile := func(path, name string) error {
		key := strings.ToLower(filepath.Clean(path))
		if seen[key] {
			return nil
		}
		size, modified, exists, err := internalTreeInfo(path)
		if err != nil || !exists {
			return err
		}
		seen[key] = true
		item := cleanupItem{
			ID: "transcript-file:" + key, Category: cleanupTranscriptions,
			Name: name, Paths: []string{path}, Modified: modified, Size: size,
		}
		candidates := []string{path}
		if info, err := os.Lstat(path); err == nil && info.IsDir() {
			candidates = []string{filepath.Join(path, "result.json"), filepath.Join(path, "partial-result.json")}
		}
		for _, candidate := range candidates {
			var result transcript.Result
			if store.ReadJSON(candidate, &result) == nil && result.Source != "" {
				item.Source = result.Source
				break
			}
		}
		items = append(items, item)
		return nil
	}
	for _, folder := range []string{"jobs-go-v1", "jobs-retranscribe-go-v1", "abas"} {
		root := filepath.Join(d.directory, folder)
		entries, err := os.ReadDir(root)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			name := entry.Name()
			path := filepath.Join(root, name)
			switch folder {
			case "jobs-go-v1":
				if !validCacheJobID(name) {
					continue
				}
			case "jobs-retranscribe-go-v1":
				if !validTranscriptionRun(name) {
					continue
				}
			case "abas":
				if id, err := strconv.ParseUint(name, 10, 64); err != nil || id == 0 {
					continue
				}
				path = filepath.Join(path, "last-transcript.json")
			}
			if err := appendFile(path, "Transcrição interna · "+name); err != nil {
				return nil, err
			}
		}
	}
	if err := appendFile(d.latestPath(), "Última transcrição interna"); err != nil {
		return nil, err
	}
	return items, nil
}

func (d *desktop) transcriptCachePaths(result *transcript.Result, run string) []string {
	var paths []string
	if result != nil && validCacheJobID(result.JobID) {
		paths = append(paths, filepath.Join(d.directory, "jobs-go-v1", result.JobID))
	}
	if validTranscriptionRun(run) && run != "" {
		paths = append(paths, filepath.Join(d.directory, "jobs-retranscribe-go-v1", run))
	}
	return paths
}

func validCacheJobID(id string) bool {
	if len(id) != 64 {
		return false
	}
	for _, character := range id {
		if character < '0' || character > '9' {
			if character < 'a' || character > 'f' {
				return false
			}
		}
	}
	return true
}

func (d *desktop) measureCleanupSeeds(seeds []cleanupSeed) ([]cleanupItem, error) {
	items := make([]cleanupItem, 0, len(seeds))
	for _, seed := range seeds {
		item := seed.item
		for _, path := range item.Paths {
			size, modified, exists, err := internalTreeInfo(path)
			if err != nil {
				return nil, err
			}
			if exists {
				item.Size += size
				if item.Modified.IsZero() || modified.After(item.Modified) {
					item.Modified = modified
				}
			}
		}
		items = append(items, item)
	}
	return items, nil
}

func (d *desktop) scanCleanupFiles(category string) ([]cleanupItem, error) {
	root := filepath.Join(d.directory, "screenshots")
	if category == cleanupRecordings {
		root = recordingFolder(d.directory)
	}
	items := make([]cleanupItem, 0)
	visited := make(map[string]bool)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if errors.Is(walkErr, os.ErrNotExist) && path == root {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil
		}
		directory := filepath.Dir(absolute)
		internal := captureDirectory(directory, root)
		if !internal && category == cleanupRecordings {
			responseRoot := filepath.Join(root, "respostas")
			// Response audio has its own dated directory. Keep arbitrary
			// subfolders and files directly under respostas protected.
			internal = !strings.EqualFold(directory, responseRoot) && captureDirectory(directory, responseRoot)
		}
		if !internal {
			return nil
		}
		extension := strings.ToLower(filepath.Ext(absolute))
		if category == cleanupScreenshots && extension != ".png" ||
			category == cleanupRecordings && extension != ".wav" {
			return nil
		}
		key := strings.ToLower(filepath.Clean(absolute))
		if visited[key] {
			return nil
		}
		info, err := os.Lstat(absolute)
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		item := cleanupItem{
			ID: "file:" + key, Category: category, Name: filepath.Base(absolute),
			Source: absolute, Paths: []string{absolute}, Modified: info.ModTime(), Size: info.Size(),
		}
		if category == cleanupRecordings {
			visited[key] = true
			var manifest recorder.TeamManifest
			if store.ReadJSON(absolute+".team.json", &manifest) == nil && manifest.Version == 1 {
				item.Name = "Equipe · " + filepath.Base(absolute)
				manifestPath := absolute + ".team.json"
				if internalCaptureFile(manifestPath, root) {
					item.Paths = append(item.Paths, manifestPath)
				}
				for _, track := range manifest.Tracks {
					trackPath := track.Snapshot.Path
					if !internalCaptureFile(trackPath, root) {
						continue
					}
					trackKey := strings.ToLower(filepath.Clean(trackPath))
					if visited[trackKey] {
						continue
					}
					trackInfo, statErr := os.Lstat(trackPath)
					if statErr != nil || !trackInfo.Mode().IsRegular() {
						continue
					}
					visited[trackKey] = true
					item.Paths = append(item.Paths, trackPath)
					item.Size += trackInfo.Size()
					if trackInfo.ModTime().After(item.Modified) {
						item.Modified = trackInfo.ModTime()
					}
				}
			}
		}
		visited[key] = true
		items = append(items, item)
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return items, nil
	}
	return items, err
}

func internalCaptureFile(path, root string) bool {
	absolute, err := filepath.Abs(path)
	if err != nil || !captureDirectory(filepath.Dir(absolute), root) {
		return false
	}
	info, err := os.Lstat(absolute)
	return err == nil && info.Mode().IsRegular()
}

func internalTreeInfo(path string) (int64, time.Time, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, time.Time{}, false, nil
	}
	if err != nil {
		return 0, time.Time{}, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return 0, time.Time{}, false, errors.New("cache contém um link simbólico e foi excluído da lista")
	}
	if !info.IsDir() {
		if !info.Mode().IsRegular() {
			return 0, time.Time{}, false, nil
		}
		return info.Size(), info.ModTime(), true, nil
	}
	var size int64
	modified := info.ModTime()
	err = filepath.WalkDir(path, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		fileInfo, statErr := entry.Info()
		if statErr != nil {
			return statErr
		}
		if fileInfo.Mode().IsRegular() {
			size += fileInfo.Size()
			if fileInfo.ModTime().After(modified) {
				modified = fileInfo.ModTime()
			}
		}
		return nil
	})
	return size, modified, true, err
}

func (d *desktop) renderCleanupItems(form *cleanupForm) {
	form.list.Objects = nil
	form.checks = make(map[string]*widget.Check, len(form.items))
	form.selected = make(map[string]bool, len(form.items))
	form.syncing = true
	eligible := 0
	for index := range form.items {
		form.items[index].InUse = d.cleanupItemInUse(form.items[index])
		item := form.items[index]
		label := fmt.Sprintf("%s · %s · %s", item.Modified.Format("02/01/2006"), formatBytes(item.Size), item.Name)
		if item.InUse != "" {
			label += " · " + item.InUse
		}
		check := widget.NewCheck(label, nil)
		selectable := item.InUse == ""
		form.selected[item.ID] = selectable
		check.SetChecked(selectable)
		if selectable {
			eligible++
		} else {
			check.Disable()
		}
		id := item.ID
		check.OnChanged = func(checked bool) {
			if form.syncing {
				return
			}
			form.selected[id] = checked
			allSelected := eligible > 0
			for _, candidate := range form.items {
				if candidate.InUse == "" {
					allSelected = allSelected && form.selected[candidate.ID]
				}
			}
			form.syncing = true
			form.all.SetChecked(allSelected)
			form.syncing = false
			d.updateCleanupSummary(form)
		}
		form.checks[item.ID] = check
		form.list.Add(check)
	}
	form.all.SetChecked(eligible > 0)
	setEnabled(form.all, eligible > 0)
	if len(form.items) == 0 {
		form.list.Add(widget.NewLabel("Nenhum item interno corresponde ao período escolhido."))
	}
	form.syncing = false
	form.list.Refresh()
	form.status.SetText("")
	d.updateCleanupSummary(form)
}

func (d *desktop) updateCleanupSummary(form *cleanupForm) {
	count := 0
	var size int64
	for _, item := range form.items {
		if form.selected[item.ID] && d.cleanupItemInUse(item) == "" {
			count++
			size += item.Size
		}
	}
	form.summary.SetText(fmt.Sprintf("%d item(ns) selecionado(s) · %s", count, formatBytes(size)))
	setEnabled(form.remove, count > 0 && !form.busy && d.cleanupAllowed())
}

func formatBytes(value int64) string {
	if value < 1024 {
		return fmt.Sprintf("%d B", value)
	}
	if value < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(value)/1024)
	}
	if value < 1024*1024*1024 {
		return fmt.Sprintf("%.1f MB", float64(value)/(1024*1024))
	}
	return fmt.Sprintf("%.2f GB", float64(value)/(1024*1024*1024))
}

func (d *desktop) confirmCleanup(form *cleanupForm) {
	if form == nil || form.busy || !d.cleanupAllowed() {
		return
	}
	var selected []cleanupItem
	var size int64
	for _, item := range form.items {
		if form.selected[item.ID] && d.cleanupItemInUse(item) == "" {
			selected = append(selected, item)
			size += item.Size
		}
	}
	if len(selected) == 0 {
		return
	}
	message := fmt.Sprintf(
		"Mover %d item(ns), totalizando %s, para a Lixeira do Windows?\n\n"+
			"Transcrições: o texto e o cache serão limpos, mantendo o áudio e os demais registros.\n"+
			"Áudios e screenshots: o histórico permanece e indica o arquivo removido.\n"+
			"Exportações e arquivos fora das pastas internas não aparecem nesta lista.",
		len(selected), formatBytes(size),
	)
	confirm := dialog.NewConfirm(uiText("Limpeza de dados internos"), message, func(accepted bool) {
		if accepted {
			d.executeCleanup(form, selected)
		}
	}, d.window)
	confirm.SetConfirmText(uiText("Mover para a Lixeira"))
	confirm.SetDismissText(uiText("Cancelar"))
	confirm.Show()
}

func (d *desktop) executeCleanup(form *cleanupForm, selected []cleanupItem) {
	if form == nil || form.busy || !d.cleanupAllowed() {
		return
	}
	selected = slices.DeleteFunc(slices.Clone(selected), func(item cleanupItem) bool {
		return d.cleanupItemInUse(item) != ""
	})
	if len(selected) == 0 {
		d.renderCleanupItems(form)
		form.status.SetText("Os itens selecionados estão em uso. Libere-os antes de limpar.")
		return
	}
	form.busy = true
	form.remove.Disable()
	form.status.SetText("Movendo os itens selecionados para a Lixeira…")
	successful := make([]cleanupItem, 0, len(selected))
	d.operation(func() error {
		for _, item := range selected {
			if err := d.recycleCleanupItem(d.ctx, item, platform.Recycle); err != nil {
				return fmt.Errorf("%s: %w", item.Name, err)
			}
			successful = append(successful, item)
		}
		return nil
	}, func(err error) {
		form.busy = false
		if len(successful) > 0 {
			if applyErr := d.applyCleanupItems(successful); applyErr != nil {
				err = errors.Join(err, applyErr)
			}
		}
		if err != nil {
			form.status.SetText("A limpeza foi parcial ou falhou: " + shortMessage(err.Error()))
		} else {
			form.status.SetText(fmt.Sprintf("%d item(ns) movido(s) para a Lixeira.", len(successful)))
		}
		d.reloadCleanupItems(form)
	})
}

// Workspace records are embedded in JSON. Give them a real file before
// recycling so deleting text/history has the same recovery as deleting audio.
func (d *desktop) recycleCleanupItem(
	ctx context.Context, item cleanupItem, recycle func(context.Context, []string) error,
) error {
	paths := make([]string, 0, len(item.Paths)+1)
	for _, path := range item.Paths {
		if _, err := os.Lstat(path); err == nil {
			paths = append(paths, path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if len(item.Record) > 0 {
		folder := filepath.Join(d.directory, "limpeza")
		if err := os.MkdirAll(folder, 0o700); err != nil {
			return err
		}
		file, err := os.CreateTemp(folder, "registro-*.json")
		if err != nil {
			return err
		}
		path := file.Name()
		if err := file.Close(); err != nil {
			return err
		}
		if err := store.Write(path, item.Record); err != nil {
			return err
		}
		paths = append(paths, path)
	}
	if len(paths) == 0 {
		return nil
	}
	return recycle(ctx, paths)
}

func (d *desktop) applyCleanupItems(items []cleanupItem) error {
	d.invalidateCleanupEvaluations(items)
	var failures error
	for _, item := range items {
		switch item.Category {
		case cleanupScreenshots:
			d.markScreenshotRemoved(item.Source)
			for editor := range d.screenshotEditors {
				if sameAudio(editor.path, item.Source) {
					editor.window.Close()
				}
			}
		case cleanupRecordings:
			d.markAudioRemoved(item)
			d.markVoiceAudioRemoved(item)
		case cleanupTranscriptions:
			failures = errors.Join(failures, d.clearTranscriptForCleanup(item))
		case cleanupHistory:
			d.removeAudioHistoryForCleanup(item)
		}
	}
	if err := d.pruneClosedEvaluations(items); err != nil {
		failures = errors.Join(failures, err)
	}
	if err := d.saveWorkspace(); err != nil {
		failures = errors.Join(failures, fmt.Errorf("os arquivos foram para a Lixeira, mas o histórico atualizado não pôde ser salvo: %w", err))
	}
	d.syncControls()
	return failures
}

func (d *desktop) cleanupDocuments() []*document {
	documents := slices.Clone(d.documents)
	for _, entry := range d.closedTabs {
		documents = append(documents, entry.document)
		if entry.combined != nil {
			documents = append(documents, entry.combined.documents...)
		}
	}
	seen := make(map[*document]bool, len(documents))
	return slices.DeleteFunc(documents, func(doc *document) bool {
		if doc == nil || seen[doc] {
			return true
		}
		seen[doc] = true
		return false
	})
}

func (d *desktop) removeAudioHistoryForCleanup(item cleanupItem) {
	if item.HistoryItem {
		// pruneClosedEvaluations owns this write and propagates its error.
		return
	}
	for _, doc := range d.cleanupDocuments() {
		if doc.id != item.DocID {
			continue
		}
		history := make([]audioSnapshot, 0, len(doc.audioHistory))
		for _, snapshot := range doc.audioHistory {
			if !sameAudio(snapshot.Source, item.Source) {
				history = append(history, snapshot)
			} else if sameAudio(doc.lastAudio, item.Source) {
				history = append(history, audioSnapshot{
					Source: snapshot.Source, SavedAt: snapshot.SavedAt, HistoryRemoved: true,
				})
			}
		}
		doc.audioHistory = history
		for index := range doc.promptSteps {
			if origin := doc.promptSteps[index].Transcription; origin != nil && sameAudio(origin.Source, item.Source) {
				doc.promptSteps[index].Transcription = nil
			}
		}
		doc.evaluation = nil
		d.syncDocumentActions(doc)
		return
	}
}

func (d *desktop) markAudioRemoved(item cleanupItem) {
	removed := make(map[string]bool, len(item.Paths)+1)
	removed[strings.ToLower(filepath.Clean(item.Source))] = true
	for _, path := range item.Paths {
		removed[strings.ToLower(filepath.Clean(path))] = true
	}
	contains := func(path string) bool { return removed[strings.ToLower(filepath.Clean(path))] }
	for _, doc := range d.cleanupDocuments() {
		changed := false
		if contains(doc.lastAudio) {
			doc.audioMissing = true
			changed = true
		}
		if doc.live != nil {
			for _, track := range doc.live.Tracks {
				if contains(track.Snapshot.Path) {
					doc.audioMissing = true
					changed = true
					break
				}
			}
		}
		for index := range doc.audioHistory {
			snapshot := cloneAudioSnapshot(doc.audioHistory[index])
			if audioSnapshotHasPath(snapshot, contains) {
				snapshot.AudioMissing = true
				doc.audioHistory[index] = snapshot
				changed = true
			}
		}
		if doc.undo != nil && contains(doc.undo.Source) {
			doc.undo.AudioMissing = true
			changed = true
		}
		if changed {
			doc.evaluation = nil
			d.syncDocumentActions(doc)
		}
	}
	for _, entry := range d.closedTabs {
		if entry.combined == nil {
			continue
		}
		for index := range entry.combined.states {
			tab := &entry.combined.states[index]
			changed := false
			if audioSnapshotHasPath(audioSnapshot{Source: tab.Source, Live: tab.Live}, contains) {
				tab.AudioMissing, changed = true, true
			}
			for audioIndex := range tab.AudioHistory {
				if audioSnapshotHasPath(tab.AudioHistory[audioIndex], contains) {
					tab.AudioHistory[audioIndex].AudioMissing, changed = true, true
				}
			}
			if tab.Undo != nil && audioSnapshotHasPath(audioSnapshot{Source: tab.Undo.Source, Live: tab.Undo.Live}, contains) {
				tab.Undo.AudioMissing, changed = true, true
			}
			if changed {
				tab.Evaluation = nil
			}
		}
	}
}

func audioSnapshotHasPath(snapshot audioSnapshot, contains func(string) bool) bool {
	if contains(snapshot.Source) {
		return true
	}
	if snapshot.Live != nil {
		for _, track := range snapshot.Live.Tracks {
			if contains(track.Snapshot.Path) {
				return true
			}
		}
	}
	return false
}

func (d *desktop) clearTranscriptForCleanup(item cleanupItem) error {
	var failures error
	for _, doc := range d.cleanupDocuments() {
		if item.DocID != 0 && doc.id != item.DocID {
			continue
		}
		tab := d.snapshotDocument(doc)
		changed, currentCleared := d.clearDocumentTranscriptSnapshot(&tab, item)
		if !changed {
			continue
		}
		restorePrompt := d.enterCurrentPrompt(doc)
		doc.lastResult, doc.recordedAppend, doc.undo = tab.Result, tab.Append, tab.Undo
		doc.transcriptionRun, doc.audioHistory, doc.promptSteps = tab.TranscriptionRun, tab.AudioHistory, tab.PromptSteps
		doc.evaluation = nil
		doc.historyApplying = true
		doc.editor.SetText(tab.Text)
		doc.currentPrompt = tab.Text
		doc.historyApplying = false
		if doc.live != nil {
			d.cancelLiveRecording(doc.live)
		}
		doc.live = nil
		d.restoreLiveArchive(doc, tab.Live)
		if currentCleared {
			doc.errorLabel.Hide()
			doc.retryButton.Hide()
			setLabel(doc.transcriptName, "Transcrição", textColor)
		}
		d.updateTranscriptLinks(doc, true)
		restorePrompt()
		d.syncDocumentActions(doc)
		// A later audio can contain the cleared stage as an append baseline.
		// Refresh only internal last-result copies; never rewrite exports.
		if doc.lastResult != nil {
			failures = errors.Join(failures, d.persistDocumentResult(doc, *doc.lastResult))
		}
	}
	for _, entry := range d.closedTabs {
		if entry.combined == nil {
			continue
		}
		for index := range entry.combined.states {
			tab := &entry.combined.states[index]
			if item.DocID == 0 || tab.ID == item.DocID {
				d.clearDocumentTranscriptSnapshot(tab, item)
			}
		}
	}
	return failures
}

func (d *desktop) clearDocumentTranscriptSnapshot(tab *documentSnapshot, item cleanupItem) (bool, bool) {
	current := cloneAudioSnapshot(audioSnapshot{
		Source: tab.Source, Text: tab.Text, Result: tab.Result, Append: tab.Append,
		Live: tab.Live, Undo: tab.Undo, ScreenshotFiles: tab.ScreenshotFiles,
		TranscriptionRun: tab.TranscriptionRun, Retry: tab.Retry, AudioMissing: tab.AudioMissing,
	})
	matches := func(snapshot audioSnapshot) bool {
		if sameAudio(snapshot.Source, item.Source) ||
			item.Current && item.DocID == tab.ID && snapshot.Source == "" && item.Source == "" {
			return true
		}
		return slices.ContainsFunc(d.transcriptCachePaths(snapshot.Result, snapshot.TranscriptionRun), func(path string) bool {
			return slices.ContainsFunc(item.Paths, func(candidate string) bool { return sameAudio(path, candidate) })
		})
	}
	type rewrite struct{ before, after string }
	var rewrites []rewrite
	currentCleared := matches(current)
	if currentCleared {
		before := current.Text
		clearAudioSnapshotTranscript(&current)
		rewrites = append(rewrites, rewrite{before, current.Text})
	}
	history := make([]audioSnapshot, len(tab.AudioHistory))
	changed := currentCleared
	for index, snapshot := range tab.AudioHistory {
		history[index] = cloneAudioSnapshot(snapshot)
		if matches(history[index]) {
			before := history[index].Text
			clearAudioSnapshotTranscript(&history[index])
			rewrites = append(rewrites, rewrite{before, history[index].Text})
			changed = true
		}
	}
	if !changed {
		return false, false
	}
	for _, edit := range rewrites {
		rewriteAudioTranscriptPrefix(&current, edit.before, edit.after)
		for index := range history {
			rewriteAudioTranscriptPrefix(&history[index], edit.before, edit.after)
		}
	}
	if current.Undo != nil && matches(audioSnapshot{
		Source: current.Undo.Source, Result: current.Undo.Result, TranscriptionRun: current.Undo.TranscriptionRun,
	}) {
		current.Undo = nil
	}
	for index := range tab.PromptSteps {
		step := &tab.PromptSteps[index]
		if origin := step.Transcription; origin != nil && matches(audioSnapshot{
			Source: origin.Source, Result: &transcript.Result{JobID: origin.JobID}, TranscriptionRun: origin.RunID,
		}) {
			step.Transcription = nil
			if !step.Sent && !step.Processed {
				step.Prompt, step.Draft = "", nil
			}
		} else if !step.Sent && !step.Processed {
			for _, edit := range rewrites {
				step.Prompt = rewriteTranscriptPrefix(step.Prompt, edit.before, edit.after)
			}
		}
	}
	tab.Text, tab.Result, tab.Append, tab.Live = current.Text, current.Result, current.Append, current.Live
	tab.Undo, tab.TranscriptionRun, tab.Retry = current.Undo, current.TranscriptionRun, current.Retry
	tab.AudioHistory, tab.Evaluation = history, nil
	return true, currentCleared
}

func rewriteTranscriptPrefix(text, before, after string) string {
	if before == "" || before == after {
		return text
	}
	if text == before {
		return after
	}
	if strings.HasPrefix(text, before) {
		rest := strings.TrimPrefix(text, before)
		if strings.HasPrefix(rest, "\n") || strings.HasPrefix(rest, "\r\n") {
			if after == "" {
				return strings.TrimLeft(rest, "\r\n")
			}
			return after + rest
		}
	}
	return text
}

func rewriteAudioTranscriptPrefix(snapshot *audioSnapshot, before, after string) {
	snapshot.Text = rewriteTranscriptPrefix(snapshot.Text, before, after)
	if snapshot.Result != nil {
		snapshot.Result.Text = rewriteTranscriptPrefix(snapshot.Result.Text, before, after)
	}
	if snapshot.Append != nil {
		snapshot.Append.Prefix = rewriteTranscriptPrefix(snapshot.Append.Prefix, before, after)
		snapshot.Append.Content = rewriteTranscriptPrefix(snapshot.Append.Content, before, after)
		snapshot.Append.Combined = rewriteTranscriptPrefix(snapshot.Append.Combined, before, after)
	}
	if snapshot.Live != nil {
		snapshot.Live.Prefix = rewriteTranscriptPrefix(snapshot.Live.Prefix, before, after)
		snapshot.Live.Combined = rewriteTranscriptPrefix(snapshot.Live.Combined, before, after)
	}
	if snapshot.Undo != nil {
		state := audioSnapshot{Text: snapshot.Undo.Text, Result: snapshot.Undo.Result, Append: snapshot.Undo.Append, Live: snapshot.Undo.Live}
		rewriteAudioTranscriptPrefix(&state, before, after)
		snapshot.Undo.Text = state.Text
	}
}

func clearAudioSnapshotTranscript(snapshot *audioSnapshot) {
	if snapshot.Live != nil {
		for index := range snapshot.Live.Entries {
			if !snapshot.Live.Entries[index].Screenshot {
				snapshot.Live.Entries[index].Text = ""
				snapshot.Live.Entries[index].Parts = nil
				snapshot.Live.Entries[index].Error = ""
				snapshot.Live.Entries[index].Pending = false
				snapshot.Live.Entries[index].NeedsReview = false
			}
		}
		for index := range snapshot.Live.PreviousEntries {
			if !snapshot.Live.PreviousEntries[index].Screenshot {
				snapshot.Live.PreviousEntries[index].Text = ""
				snapshot.Live.PreviousEntries[index].Parts = nil
				snapshot.Live.PreviousEntries[index].Error = ""
				snapshot.Live.PreviousEntries[index].Pending = false
				snapshot.Live.PreviousEntries[index].NeedsReview = false
			}
		}
		blocks := live.Blocks(snapshot.Live.Entries, false)
		snapshot.Live.PreviousBlocks = slices.Clone(blocks)
		snapshot.Live.Combined = live.Render(snapshot.Live.Prefix, blocks)
		snapshot.Text = snapshot.Live.Combined
	} else if snapshot.Append != nil {
		snapshot.Append.Content = ""
		snapshot.Text = appendTranscriptText(snapshot.Append.Prefix, screenshotFooter(snapshot.Append.Screenshots))
		snapshot.Append.Combined = snapshot.Text
		snapshot.Append.LinksReady = true
	} else {
		snapshot.Text = ""
	}
	snapshot.Result = nil
	snapshot.TranscriptionRun = ""
	snapshot.Retry = false
	snapshot.Error = ""
	snapshot.Undo = nil
}

func (d *desktop) markScreenshotRemoved(path string) {
	marker := "[Screenshot removida]"
	for _, doc := range d.cleanupDocuments() {
		restorePrompt := d.enterCurrentPrompt(doc)
		changed := false
		replace := func(text *string) {
			updated := replaceScreenshotLine(*text, path, marker)
			changed = changed || updated != *text
			*text = updated
		}
		before := len(doc.screenshotFiles)
		doc.screenshotFiles = slices.DeleteFunc(doc.screenshotFiles, func(candidate string) bool {
			return sameAudio(candidate, path)
		})
		changed = changed || len(doc.screenshotFiles) != before
		text := replaceScreenshotLine(doc.editor.Text, path, marker)
		if text != doc.editor.Text {
			doc.historyApplying = true
			doc.editor.SetText(text)
			doc.currentPrompt = text
			doc.historyApplying = false
			changed = true
		}
		if doc.lastResult != nil {
			result := *doc.lastResult
			replace(&result.Text)
			doc.lastResult = &result
		}
		markRecordedScreenshot(doc.recordedAppend, path, marker, &changed)
		markRecordedScreenshot(doc.jobAppend, path, marker, &changed)
		if doc.live != nil {
			archive := doc.live.archive()
			if markLiveScreenshot(archive, path, marker) {
				doc.live.liveArchive = *archive
				doc.live.blocks = doc.live.textBlocks()
				doc.live.rebuildScreenshotCuts()
				changed = true
			}
		}
		for index := range doc.audioHistory {
			snapshot := cloneAudioSnapshot(doc.audioHistory[index])
			if markAudioSnapshotScreenshot(&snapshot, path, marker) {
				doc.audioHistory[index] = snapshot
				changed = true
			}
		}
		if markDraftScreenshot(doc.promptSteps, path, marker) {
			changed = true
		}
		if changed {
			doc.evaluation = nil
			if doc.live != nil && doc.live.show {
				d.renderLiveText(doc.live)
			}
			d.updateTranscriptLinks(doc, true)
			d.syncDocumentActions(doc)
		}
		restorePrompt()
	}
	for _, entry := range d.closedTabs {
		if entry.combined != nil {
			for index := range entry.combined.states {
				markDocumentSnapshotScreenshot(&entry.combined.states[index], path, marker)
			}
		}
	}
}

func replaceScreenshotLine(text, path, marker string) string {
	lines := strings.Split(text, "\n")
	changed := false
	for index := range lines {
		if strings.TrimSpace(lines[index]) == path {
			lines[index] = marker
			changed = true
		}
	}
	if !changed {
		return text
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

func markRecordedScreenshot(recorded *recordedAppend, path, marker string, changed *bool) {
	if recorded == nil {
		return
	}
	before := len(recorded.Screenshots)
	recorded.Screenshots = slices.DeleteFunc(recorded.Screenshots, func(candidate string) bool {
		return sameAudio(candidate, path)
	})
	*changed = *changed || len(recorded.Screenshots) != before
	for _, text := range []*string{&recorded.Prefix, &recorded.Combined, &recorded.Content} {
		updated := replaceScreenshotLine(*text, path, marker)
		*changed = *changed || updated != *text
		*text = updated
	}
}

func markLiveScreenshot(archive *liveArchive, path, marker string) bool {
	changed := false
	for _, entries := range []*[]live.Entry{&archive.Entries, &archive.PreviousEntries} {
		for index := range *entries {
			entry := &(*entries)[index]
			if entry.Screenshot && sameAudio(entry.Path, path) {
				entry.Path, entry.Pending, entry.Error = marker, false, ""
				changed = true
			}
		}
	}
	for index := range archive.PreviousBlocks {
		updated := replaceScreenshotLine(archive.PreviousBlocks[index].Text, path, marker)
		changed = changed || updated != archive.PreviousBlocks[index].Text
		archive.PreviousBlocks[index].Text = updated
	}
	for _, text := range []*string{&archive.Prefix, &archive.Combined} {
		updated := replaceScreenshotLine(*text, path, marker)
		changed = changed || updated != *text
		*text = updated
	}
	return changed
}

func markAudioSnapshotScreenshot(snapshot *audioSnapshot, path, marker string) bool {
	changed := false
	for _, text := range []*string{&snapshot.Text} {
		updated := replaceScreenshotLine(*text, path, marker)
		changed = changed || updated != *text
		*text = updated
	}
	before := len(snapshot.ScreenshotFiles)
	snapshot.ScreenshotFiles = slices.DeleteFunc(snapshot.ScreenshotFiles, func(candidate string) bool {
		return sameAudio(candidate, path)
	})
	changed = changed || len(snapshot.ScreenshotFiles) != before
	if snapshot.Result != nil {
		updated := replaceScreenshotLine(snapshot.Result.Text, path, marker)
		changed = changed || updated != snapshot.Result.Text
		snapshot.Result.Text = updated
	}
	recordedChanged := false
	markRecordedScreenshot(snapshot.Append, path, marker, &recordedChanged)
	changed = changed || recordedChanged
	if snapshot.Live != nil {
		changed = markLiveScreenshot(snapshot.Live, path, marker) || changed
	}
	if snapshot.Undo != nil {
		updated := replaceScreenshotLine(snapshot.Undo.Text, path, marker)
		changed = changed || updated != snapshot.Undo.Text
		snapshot.Undo.Text = updated
	}
	return changed
}

func markDraftScreenshot(steps []promptStep, path, marker string) bool {
	changed := false
	for index := range steps {
		step := &steps[index]
		if !step.Sent && !step.Processed {
			updated := replaceScreenshotLine(step.Prompt, path, marker)
			changed = changed || updated != step.Prompt
			step.Prompt = updated
		}
		if step.Draft != nil {
			updated := replaceScreenshotLine(*step.Draft, path, marker)
			changed = changed || updated != *step.Draft
			*step.Draft = updated
		}
		if origin := step.Transcription; origin != nil {
			paths := slices.Clone(origin.Screenshots)
			paths = slices.DeleteFunc(paths, func(candidate string) bool { return sameAudio(candidate, path) })
			if len(paths) != len(origin.Screenshots) {
				copy := *origin
				copy.Screenshots = paths
				step.Transcription = &copy
				changed = true
			}
		}
	}
	return changed
}

func markDocumentSnapshotScreenshot(tab *documentSnapshot, path, marker string) bool {
	state := audioSnapshot{
		Text: tab.Text, Result: tab.Result, Append: tab.Append, Live: tab.Live,
		Undo: tab.Undo, ScreenshotFiles: tab.ScreenshotFiles,
	}
	changed := markAudioSnapshotScreenshot(&state, path, marker)
	tab.Text, tab.ScreenshotFiles = state.Text, state.ScreenshotFiles
	tab.Result, tab.Append, tab.Live, tab.Undo = state.Result, state.Append, state.Live, state.Undo
	for index := range tab.AudioHistory {
		changed = markAudioSnapshotScreenshot(&tab.AudioHistory[index], path, marker) || changed
	}
	changed = markDraftScreenshot(tab.PromptSteps, path, marker) || changed
	if changed {
		tab.Evaluation = nil
	}
	return changed
}

func (d *desktop) pruneClosedEvaluations(items []cleanupItem) error {
	path := filepath.Join(d.directory, "avaliacoes-fechadas-v1.json")
	archive, err := d.readEvaluationArchive()
	if err != nil {
		return err
	}
	changed := false
	archive.Entries = slices.DeleteFunc(archive.Entries, func(entry closedEvaluation) bool {
		for _, item := range items {
			if d.evaluationLinkedToCleanup(entry.Context, entry.TabID, item) {
				changed = true
				return true
			}
		}
		return false
	})
	if changed {
		if err := store.WriteJSON(path, archive); err != nil {
			return fmt.Errorf("remover avaliações ligadas aos itens limpos: %w", err)
		}
	}
	return nil
}

func (d *desktop) readEvaluationArchive() (evaluationArchive, error) {
	path := filepath.Join(d.directory, "avaliacoes-fechadas-v1.json")
	archive := evaluationArchive{Version: 1}
	err := store.ReadJSON(path, &archive)
	if errors.Is(err, os.ErrNotExist) {
		return archive, nil
	}
	if err != nil {
		return evaluationArchive{}, fmt.Errorf("ler avaliações internas: %w", err)
	}
	if archive.Version != 0 && archive.Version != 1 {
		return evaluationArchive{}, errors.New("o arquivo de avaliações internas tem uma versão desconhecida")
	}
	archive.Version = 1
	return archive, nil
}

func selectedCleanupItems(form *cleanupForm) []cleanupItem {
	var selected []cleanupItem
	if form == nil {
		return nil
	}
	for _, item := range form.items {
		if form.selected[item.ID] {
			selected = append(selected, item)
		}
	}
	return selected
}
