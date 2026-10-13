//go:build windows && windowsmoke

package desktop

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"time"

	"ai-talker/internal/config"
	"ai-talker/internal/platform"
	"ai-talker/internal/recorder"
	"ai-talker/internal/screenshot"
	"ai-talker/internal/store"
	"ai-talker/internal/transcript"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// This harness exists only in an explicitly built smoke executable. Geometry
// and captures come from the native canvas; no hooks are shipped in the app.
type smokeRectangle struct{ X, Y, Width, Height float32 }

type windowSmokeState struct {
	StopPlayback, OutputVolumeButton                smokeRectangle
	StopVisible, OutputVolumeVisible                bool
	OutputVolumeOpen                                bool
	OutputVolumePopup                               *smokeRectangle
	OutputVolumeSlider                              smokeRectangle
	OutputVolumeThumb                               smokeRectangle
	OutputVolume, PlayerOutputVolume                int
	CaptureVolumeButton                             smokeRectangle
	CaptureVolumesOpen                              bool
	CaptureVolumePopup                              *smokeRectangle
	MicrophoneThumb, PlaybackThumb                  smokeRectangle
	CloudButton                                     smokeRectangle
	QualityButton                                   smokeRectangle
	TranscriptionProvider                           string
	MenuItems                                       []smokeRectangle
	MenuLabels                                      []string
	MenuChildren                                    int
	PlayVisible                                     bool
	ProcessCollapse                                 smokeRectangle
	ProcessCollapsed                                bool
	TranscriptCollapse                              smokeRectangle
	TranscriptCollapsed, InvertPanels               bool
	TranscriptPanel, ProcessingPanel                smokeRectangle
	PromptBack, PromptForward                       smokeRectangle
	PromptCount, PromptCursor                       int
	PromptReadonly                                  bool
	CloudMenu, CloudSubmenu                         *smokeRectangle
	CloudCompleteItem, CloudStreamItem              smokeRectangle
	CloudSubmenuMode                                string
	CloudModel, CloudStreamModel                    string
	CloudBatchOptions, CloudStreamOptions           int
	Theme, HeaderIconName                           string
	GroupName, GroupLimit                           float32
	GroupLabel, GroupHandle                         smokeRectangle
	GroupScrollThumb                                *smokeRectangle
	GroupWidthPopup                                 *smokeRectangle
	GroupWidthSlider, GroupWidthThumb               smokeRectangle
	AudioHistoryButton                              smokeRectangle
	AudioHistoryCount                               int
	AudioHistoryEnabled, AudioLoading               bool
	AudioHistoryItems                               []smokeRectangle
	Frame                                           uint64
	Scale                                           float32
	Header, Settings, Minimize, Close               smokeRectangle
	Bottom, RecorderDivider, Divider                smokeRectangle
	RecorderClock, RecorderWave                     smokeRectangle
	RecorderMode, RecorderSizeHint                  string
	RecorderFont                                    float32
	RecorderWide                                    bool
	RecorderCollapsed                               bool
	RecorderCollapse, RecorderRestore, RecorderStop smokeRectangle
	Folder, AWS                                     smokeRectangle
	Camera, RecordPanel, Record, Pause              smokeRectangle
	Discard, CopyAudioPath                          smokeRectangle
	CopyAudioPathVisible                            bool
	ColumnViewport                                  smokeRectangle
	CanvasWidth, CanvasHeight, Height               float32
	ScrollY, RecorderHeight, TextHeight             float32
	SettingsOpen                                    bool
	Overlays                                        int
	ScreenshotBusy                                  bool
	Screenshot                                      *screenshotSmokeState
	ResultView                                      smokeRectangle
	ResultLink                                      *smokeRectangle
	ReadingLinks                                    bool
	ResultLinkCount                                 int
	ProcessedText                                   string
	Recording                                       bool
	Transcribing                                    bool
	TranscriptReading                               bool
	TranscriptText                                  string
	TranscriptLink                                  *smokeRectangle
	TranscriptViewport                              smokeRectangle
	TranscriptCopy                                  smokeRectangle
	Retranscribe                                    smokeRectangle
	RetranscribeEnabled                             bool
	TranscriptionRun, AudioSource                   string
	TranscriptionModel                              string
	AudioResultReady                                bool
	Device, Stream, Process, Follow                 smokeRectangle
	NewAudio, ImportAudio                           smokeRectangle
	StreamText                                      string
	TeamIcon                                        bool
	CaptureSelect                                   smokeRectangle
	DevicePopup                                     *smokeRectangle
	DevicesOpen, DevicesReady, StreamEnabled        bool
	ConversationTurns                               int
	ProcessBusy                                     bool
	DeviceMode                                      string
	RecordSeconds                                   float64
	StreamPending                                   int
	MicrophoneVolume, PlaybackVolume                float64
	MicrophoneSlider, PlaybackSlider                smokeRectangle
	MicrophoneVisible, PlaybackVisible              bool
	PlayEnabled, PlaybackReady, Playing             bool
	PlaybackPaused, CapturePaused                   bool
	DiscardConfirmation                             bool
	PlayIcon, PlayHint, TimerText                   string
	PlaybackSeconds                                 float64
	Backlog                                         *backlogSmokeState
	Release012                                      *release012SmokeState
	Soundboard                                      *soundboardSmokeState
}

type screenshotSmokeState struct {
	Title, Path                         string
	Scale                               float32
	Surface, Save, Trash, Brush, Eraser smokeRectangle
	Text, Undo, Redo, CopyPath          smokeRectangle
	Busy                                bool
	Revision, SavedRevision             uint64
	Zoom, ZoomButton, Viewport, Image   smokeRectangle
	ZoomItems                           []smokeRectangle
	ZoomLabels                          []string
	ZoomPercent                         float64
	ZoomFit                             bool
	CanvasWidth, CanvasHeight           float32
}

func RunWindowSmoke(ctx context.Context, directory string) error {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	undoReopen := windowSmokeUndoReopen(directory)
	settings := config.Default()
	if undoReopen {
		loaded, err := config.Load(directory)
		if err != nil {
			return err
		}
		settings = loaded
	} else {
		settings.Appearance.Theme = config.ThemeDark
		settings.Appearance.Layout = config.LayoutColumn
	}
	stage := func(name string) { _ = os.WriteFile(filepath.Join(directory, "smoke-stage.txt"), []byte(name), 0o600) }
	stage("initializing")
	deps := nativeDependencies()
	deps.verify = func(context.Context, config.Settings) error { return nil } // Synthetic profile; no AWS calls.
	deps.microphones = func() ([]recorder.Device, error) { return nil, nil }
	deps.profiles = func() ([]string, error) { return nil, nil }
	streamInput := new(windowSmokeInput)
	deps.newConfiguredRecorder = func(_ config.Settings, recordings string) recording {
		return recorder.NewWithInput(-1, recordings, streamInput)
	}
	// Native UI checks exercise the controls without playing audible fixtures.
	deps.newPlayer = func(string) audioPlayer { return new(windowSmokePlayer) }
	deps.playbackDevices = func() ([]recorder.Device, error) {
		return []recorder.Device{{ID: 100000, Name: "Saída sintética", EndpointID: "smoke-output"}}, nil
	}
	deps.microphones = func() ([]recorder.Device, error) {
		return []recorder.Device{{ID: 0, Name: "Microfone sintético", EndpointID: "smoke-mic"}}, nil
	}
	configureRelease011Smoke(&deps)
	configureRelease012Smoke(&deps)
	deps.process = windowSmokeProcess(directory)
	deps.selectScreenshot = func(ctx context.Context) (image.Image, error) {
		_ = store.WriteJSON(filepath.Join(directory, "selection-stage.json"), map[string]any{"started": true})
		pixels, err := platform.SelectScreenshot(ctx)
		message := ""
		if err != nil {
			message = err.Error()
		}
		_ = store.WriteJSON(filepath.Join(directory, "selection-stage.json"), map[string]any{"finished": true, "error": message})
		return pixels, err
	}
	deps.openURL = func(address string) error {
		return store.WriteJSON(filepath.Join(directory, "opened-url.json"), map[string]string{"url": address})
	}
	deps.openImage = func(path string) error {
		return store.WriteJSON(filepath.Join(directory, "opened-image.json"), map[string]string{"path": path})
	}
	deps.copy = func(text string) error {
		return store.WriteJSON(filepath.Join(directory, "copied-text.json"), map[string]string{"text": text})
	}
	app.SetMetadata(fyne.AppMetadata{
		ID: "dev.ai-talker.window-smoke", Name: "STT-widget",
		Migrations: map[string]bool{"fyneDo": true},
	})
	application := app.NewWithID("dev.ai-talker.window-smoke")
	runner := windowSmokeRunner{}
	d := newDesktop(ctx, application, settings, directory, runner, deps)
	stage("desktop-created")
	if !undoReopen {
		d.editor.SetText("Transcrição sintética para verificar a janela, o mouse e as divisórias.")
		d.processEditor.SetText("Texto processado sintético.")
	}
	first := d.document
	var stateFrame uint64
	var pauseClicks uint64
	pauseAction := d.pauseButton.OnTapped
	d.pauseButton.OnTapped = func() {
		pauseClicks++
		paused := false
		if d.recorder != nil {
			paused = d.recorder.Snapshot().Paused
		}
		_ = store.WriteJSON(filepath.Join(directory, "pause-clicks.json"), map[string]any{
			"count": pauseClicks, "frame": stateFrame, "capture_paused_before": paused,
			"capture_running": d.capture == captureRunning, "playing": d.playback != nil,
		})
		pauseAction()
	}
	application.Lifecycle().SetOnStarted(d.guard(func() {
		stage("started")
		if !undoReopen {
			stage("adding-group")
			if second := d.addGroup(); second != nil {
				stage("setting-second-transcription")
				second.editor.SetText("Transcrição sintética do segundo grupo.")
			}
			stage("selecting-first-tab")
			d.tabs.Select(first.item)
		}
		d.start("")
		stage("desktop-started")
		d.workers.Go(func() {
			ticker := time.NewTicker(150 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-d.ctx.Done():
					return
				case <-ticker.C:
					d.post(func() {
						defer func() {
							if failure := recover(); failure != nil {
								trace := append([]byte(fmt.Sprint(failure, "\n")), debug.Stack()...)
								_ = os.WriteFile(filepath.Join(d.directory, "state-panic.txt"), trace, 0o600)
								panic(failure)
							}
						}()
						stage("writing-state")
						stateFrame++
						d.runSoundboardSmokeActions()
						d.writeWindowSmokeState(streamInput, stateFrame)
						stage("state-written")
					})
				}
			}
		})
	}))
	d.window.Show()
	stage("window-shown")
	application.Run()
	d.stopped.Store(true)
	d.cancel()
	d.workers.Wait()
	return nil
}

type windowSmokeRunner struct{}

func (windowSmokeRunner) Transcribe(ctx context.Context, source string, settings config.Settings, request transcript.Request) (transcript.Result, error) {
	if request.CacheDir != "" {
		timer := time.NewTimer(450 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return transcript.Result{}, ctx.Err()
		case <-timer.C:
		}
		return transcript.Result{
			Source: source, Text: "Nova transcrição sintética · " + settings.ModelID(),
			ModelID: settings.ModelID(), DurationSeconds: 3,
		}, nil
	}
	if text, ok, err := windowSmokeStreamResult(ctx, source, request.OnPartial); ok {
		return transcript.Result{Source: source, Text: text}, err
	}
	return transcript.Result{Source: source, Text: "Transcrição sintética da gravação.", DurationSeconds: 3}, nil
}

func (d *desktop) smokeRectangle(object fyne.CanvasObject) smokeRectangle {
	if object == nil || reflect.ValueOf(object).Kind() == reflect.Pointer && reflect.ValueOf(object).IsNil() {
		return smokeRectangle{}
	}
	position := d.application.Driver().AbsolutePositionForObject(object)
	size := object.Size()
	return smokeRectangle{position.X, position.Y, size.Width, size.Height}
}

func (d *desktop) smokeHorizontalThumb(object fyne.CanvasObject) *smokeRectangle {
	if object == nil || !object.Visible() {
		return nil
	}
	kind := reflect.TypeOf(object)
	if kind.Kind() == reflect.Pointer && kind.Elem().Name() == "scrollBar" &&
		object.Size().Width > object.Size().Height {
		thumb := d.smokeRectangle(object)
		return &thumb
	}
	var children []fyne.CanvasObject
	if box, ok := object.(*fyne.Container); ok {
		children = box.Objects
	} else if control, ok := object.(fyne.Widget); ok {
		children = test.WidgetRenderer(control).Objects()
	}
	for _, child := range children {
		if thumb := d.smokeHorizontalThumb(child); thumb != nil {
			return thumb
		}
	}
	return nil
}

func (d *desktop) writeWindowSmokeState(streamInput *windowSmokeInput, frame uint64) {
	textHeight := d.document.transcriptPanel.Size().Height
	if d.editors != nil {
		textHeight = d.editors.Leading.Size().Height
	}
	var columnHeight, columnScrollY float32
	if d.columnScroll != nil {
		columnScrollY = d.columnScroll.Offset.Y
		if d.columnScroll.Content != nil {
			columnHeight = d.columnScroll.Content.Size().Height
		}
	}
	state := windowSmokeState{
		Backlog:            d.windowBacklogState(),
		Release012:         d.windowRelease012State(),
		Soundboard:         d.windowSmokeSoundboard(),
		StopPlayback:       d.smokeRectangle(d.stopPlaybackButton),
		OutputVolumeButton: d.smokeRectangle(d.outputVolumeButton),
		StopVisible:        d.stopPlaybackButton.Visible(), OutputVolumeVisible: d.outputVolumeButton.Visible(),
		OutputVolume:          config.CaptureVolume(d.settings.OutputVolume),
		CaptureVolumeButton:   d.smokeRectangle(d.captureVolumes.button),
		CloudButton:           d.smokeRectangle(d.providerButton),
		QualityButton:         d.smokeRectangle(d.qualityBars),
		TranscriptionProvider: d.settings.TranscriptionProvider(),
		PlayVisible:           d.pauseButton.Visible(),
		ProcessCollapse:       d.smokeRectangle(d.document.processCollapse),
		ProcessCollapsed:      d.document.processCollapsed,
		TranscriptCollapse:    d.smokeRectangle(d.document.transcriptCollapse),
		TranscriptCollapsed:   d.document.transcriptCollapsed,
		InvertPanels:          d.settings.Appearance.InvertPanels,
		TranscriptPanel:       d.smokeRectangle(d.document.transcriptPanel),
		ProcessingPanel:       d.smokeRectangle(d.document.processPanel),
		PromptBack:            d.smokeRectangle(d.document.promptBack), PromptForward: d.smokeRectangle(d.document.promptForward),
		PromptCount: len(d.document.promptSteps), PromptCursor: d.document.promptCursor,
		PromptReadonly: d.document.browsingPrompts(),
		Theme:          d.settings.Appearance.Theme, HeaderIconName: d.brandIcon.Resource.Name(),
		AudioHistoryButton:  d.smokeRectangle(d.audioHistoryButton),
		AudioHistoryCount:   len(d.audioSnapshots(d.document)),
		AudioHistoryEnabled: !d.audioHistoryButton.Disabled(), AudioLoading: d.audioLoading,
		CloudModel: d.settings.CloudModelID(), CloudStreamModel: d.settings.StreamModelID(),
		CloudBatchOptions: len(cloudChoices(false)), CloudStreamOptions: len(cloudChoices(true)),
		Frame: frame,
		Scale: d.window.Canvas().Scale(), Header: d.smokeRectangle(d.header),
		Settings: d.smokeRectangle(d.settingsButton), Minimize: d.smokeRectangle(d.minimizeButton),
		Close: d.smokeRectangle(d.windowCloseButton), Bottom: d.smokeRectangle(d.columnHandle),
		Folder: d.smokeRectangle(d.folderButton), AWS: d.smokeRectangle(d.awsIndicator),
		CanvasWidth: d.window.Canvas().Size().Width, CanvasHeight: d.window.Canvas().Size().Height,
		Height: columnHeight, ScrollY: columnScrollY,
		RecorderHeight: d.recordPanel.Size().Height, TextHeight: textHeight,
		RecorderClock: d.smokeRectangle(d.timer), RecorderWave: d.smokeRectangle(d.wave),
		RecorderMode: d.recorderLayout.mode, RecorderSizeHint: d.sizeButton.hint.text,
		RecorderFont: d.timer.TextSize,
		RecorderWide: d.recorderLayout.wideEnough(d.recorderLayout.lastWidth),
		SettingsOpen: d.settingsForm != nil,
		Overlays:     len(d.window.Canvas().Overlays().List()),
		Camera:       d.smokeRectangle(d.screenshotButton), RecordPanel: d.smokeRectangle(d.recordPanel),
		Record: d.smokeRectangle(d.recordButton), Pause: d.smokeRectangle(d.pauseButton),
		Discard: d.smokeRectangle(d.discardButton), CopyAudioPath: d.smokeRectangle(d.copyAudioPath),
		CopyAudioPathVisible: d.copyAudioPath.Visible(),
		ColumnViewport:       d.smokeRectangle(d.columnScroll),
		ScreenshotBusy:       d.screenshotBusy,
		ResultView:           d.smokeRectangle(d.document.processViewButton),
		ReadingLinks:         d.document.processReading, ProcessedText: d.document.processEditor.Text,
		ResultLinkCount: d.document.processLinkCount,
		Recording:       d.capture == captureRunning, Transcribing: d.document.busy,
		TranscriptReading: d.document.transcriptReading, TranscriptText: d.document.editor.Text,
		TranscriptCopy:      d.smokeRectangle(d.document.copyButton),
		Retranscribe:        d.smokeRectangle(d.document.retranscribeButton),
		RetranscribeEnabled: !d.document.retranscribeButton.Disabled(),
		TranscriptionRun:    d.document.transcriptionRun, AudioSource: d.document.lastAudio,
		TranscriptViewport: d.smokeRectangle(d.document.transcriptReadScroll),
		Device:             d.smokeRectangle(d.deviceButton), Stream: d.smokeRectangle(d.streamButton),
		NewAudio: d.smokeRectangle(d.newButton), ImportAudio: d.smokeRectangle(d.importButton),
		StreamText: d.streamButton.Text, TeamIcon: d.deviceButton.Icon == teamInputIcon,
		Process: d.smokeRectangle(d.document.processButton), Follow: d.smokeRectangle(d.document.processFollowButton),
		DevicesOpen:       d.captureDevicesOpen(),
		StreamEnabled:     d.settings.Capture.Stream && d.settings.TranscriptionProvider() == config.ProviderBedrock,
		DeviceMode:        d.settings.Capture.SourceMode(),
		ConversationTurns: len(d.document.processHistory), ProcessBusy: d.document.processBusy,
		MicrophoneVolume: d.captureVolumes.microphone.Value, PlaybackVolume: d.captureVolumes.playback.Value,
		MicrophoneSlider:  d.smokeRectangle(d.captureVolumes.microphone),
		PlaybackSlider:    d.smokeRectangle(d.captureVolumes.playback),
		MicrophoneVisible: d.captureVolumes.micRow.Visible(), PlaybackVisible: d.captureVolumes.sysRow.Visible(),
		PlayEnabled: !d.pauseButton.Disabled(), PlayHint: d.pauseButton.hint.text, TimerText: d.timer.Text,
		DiscardConfirmation: d.discardDialog,
	}
	state.RecorderCollapsed = d.document.panelSizes.RecorderCollapsed
	if surface := d.recorderLayout.surface; surface != nil {
		state.RecorderRestore = d.smokeRectangle(surface.restore)
		state.RecorderStop = d.smokeRectangle(surface.stop)
	}
	if form := d.outputVolumeForm; form != nil && form.popup.Visible() {
		state.OutputVolumeOpen = true
		popup := d.smokeRectangle(form.popup.Content)
		state.OutputVolumePopup = &popup
		state.OutputVolumeSlider = d.smokeRectangle(form.slider)
		state.OutputVolumeThumb = d.smokeRectangle(test.WidgetRenderer(form.slider).Objects()[2])
	}
	if controls := d.captureVolumes; controls.popup != nil && controls.popup.Visible() {
		state.CaptureVolumesOpen = true
		popup := d.smokeRectangle(controls.popup.Content)
		state.CaptureVolumePopup = &popup
		state.MicrophoneThumb = d.smokeRectangle(test.WidgetRenderer(controls.microphone).Objects()[2])
		state.PlaybackThumb = d.smokeRectangle(test.WidgetRenderer(controls.playback).Objects()[2])
	}
	if rows := d.tabs.renderer.rows; len(rows) > 0 {
		row := rows[0]
		state.GroupLabel, state.GroupHandle = d.smokeRectangle(row.label), d.smokeRectangle(row.resize)
		state.GroupName, state.GroupLimit = row.label.MinSize().Width, d.groupNameWidth(row.id)
		state.GroupScrollThumb = d.smokeHorizontalThumb(row.scroll)
	}
	if form := d.groupWidthForm; form != nil && form.popup.Visible() {
		popup := d.smokeRectangle(form.popup.Content)
		state.GroupWidthPopup = &popup
		state.GroupWidthSlider = d.smokeRectangle(form.slider)
		state.GroupWidthThumb = d.smokeRectangle(test.WidgetRenderer(form.slider).Objects()[2])
	}
	if menu, ok := d.window.Canvas().Focused().(*widget.PopUpMenu); ok && menu.Visible() {
		root := d.smokeRectangle(menu)
		state.CloudMenu = &root
		state.MenuItems, state.MenuLabels = d.smokeMenuItems(menu)
		for _, item := range menu.Items {
			if parent, ok := item.(interface{ Child() *widget.Menu }); ok && parent.Child() != nil {
				state.MenuChildren++
			}
		}
		// The history menu contains only its numbered audio entries.
		if len(menu.Items) == state.AudioHistoryCount && state.AudioHistoryCount > 0 {
			for _, item := range menu.Items {
				state.AudioHistoryItems = append(state.AudioHistoryItems, d.smokeRectangle(item))
			}
		}
	}
	switch d.pauseButton.Icon {
	case theme.MediaPlayIcon():
		state.PlayIcon = "play"
	case theme.MediaPauseIcon():
		state.PlayIcon = "pause"
	default:
		state.PlayIcon = "preparing"
	}
	if d.playback != nil {
		snapshot := d.playback.player.Snapshot()
		state.PlaybackReady, state.Playing = snapshot.Ready, snapshot.Active
		state.PlaybackPaused, state.PlaybackSeconds = snapshot.Paused, snapshot.Position
		if player, ok := d.playback.player.(*windowSmokePlayer); ok {
			state.PlayerOutputVolume = player.outputVolume()
		}
	}
	if d.document.lastResult != nil {
		state.TranscriptionModel = d.document.lastResult.ModelID
		state.AudioResultReady = sameAudio(d.document.lastResult.Source, d.document.lastAudio)
	}
	if d.captureDevicesOpen() {
		state.DevicesReady = !d.deviceForm.loading && !d.deviceForm.saving
		popup := d.smokeRectangle(d.deviceForm.popup.Content)
		state.DevicePopup = &popup
		state.CaptureSelect = d.smokeRectangle(d.captureSelect)
	}
	if d.recorder != nil {
		state.RecordSeconds = d.recorder.Snapshot().Duration
		state.CapturePaused = d.recorder.Snapshot().Paused
	}
	if d.document.live != nil {
		state.StreamPending = len(d.document.live.running)
	}
	if d.document.processReading {
		state.ResultLink = d.smokeLink(d.document.processPreview, 0)
	}
	if d.document.transcriptReading {
		state.TranscriptLink = d.smokeLink(d.document.transcriptPreview, 0)
	}
	for editor := range d.screenshotEditors {
		state.Screenshot = &screenshotSmokeState{
			Title: editor.window.Title(), Path: editor.path, Scale: editor.window.Canvas().Scale(),
			Surface: d.smokeRectangle(editor.surface), Save: d.smokeRectangle(editor.save),
			Trash: d.smokeRectangle(editor.trash), Brush: d.smokeRectangle(editor.brush),
			Eraser: d.smokeRectangle(editor.eraser), Text: d.smokeRectangle(editor.text),
			Undo: d.smokeRectangle(editor.undo), Redo: d.smokeRectangle(editor.redo), CopyPath: d.smokeRectangle(editor.copyPath),
			Busy: editor.busy, Revision: editor.document.Revision(), SavedRevision: editor.savedRevision,
			Zoom: d.smokeRectangle(editor.zoom), ZoomButton: d.smokeRectangle(editor.zoomButton),
			Viewport: d.smokeRectangle(editor.viewport), Image: d.smokeRectangle(editor.surface.image),
			ZoomPercent: editor.zoomPercent, ZoomFit: editor.zoomFit,
			CanvasWidth: editor.window.Canvas().Size().Width, CanvasHeight: editor.window.Canvas().Size().Height,
		}
		if menu, ok := editor.window.Canvas().Focused().(*widget.PopUpMenu); ok && menu.Visible() {
			state.Screenshot.ZoomItems, state.Screenshot.ZoomLabels = d.smokeMenuItems(menu)
		}
	}
	if d.editors != nil {
		state.Divider = d.smokeRectangle(test.WidgetRenderer(d.editors).Objects()[1])
	}
	if d.rowSplit != nil {
		state.RecorderDivider = d.smokeRectangle(test.WidgetRenderer(d.rowSplit).Objects()[1])
	}
	if d.recorderDivider != nil {
		state.RecorderDivider = d.smokeRectangle(d.recorderDivider.grip)
	}
	if err := store.WriteJSON(filepath.Join(d.directory, "window-state.json"), state); err != nil {
		d.status.SetText("Falha ao salvar geometria sintética.")
	}
	var command struct {
		CloseOutputVolume   bool                             `json:"close_output_volume"`
		CloseCaptureVolumes bool                             `json:"close_capture_volumes"`
		CloseGroupWidth     bool                             `json:"close_group_width"`
		Theme               string                           `json:"theme"`
		GroupName           string                           `json:"group_name"`
		NewTabs             int                              `json:"new_tabs"`
		CloudModel          string                           `json:"cloud_model"`
		CloudStreamModel    string                           `json:"cloud_stream_model"`
		Provider            string                           `json:"provider"`
		ToggleProcessing    bool                             `json:"toggle_processing"`
		ToggleTranscription bool                             `json:"toggle_transcription"`
		ToggleRecorder      bool                             `json:"toggle_recorder"`
		InvertPanels        *bool                            `json:"invert_panels"`
		RecorderSize        string                           `json:"recorder_size"`
		Capture             string                           `json:"capture"`
		CloseSettings       bool                             `json:"close_settings"`
		ScrollBottom        bool                             `json:"scroll_bottom"`
		ScrollTop           bool                             `json:"scroll_top"`
		Open                string                           `json:"open"`
		CloseMenu           bool                             `json:"close_menu"`
		Hover               bool                             `json:"hover"`
		CloseDialog         bool                             `json:"close_dialog"`
		SettingsSection     *int                             `json:"settings_section"`
		Resize              *struct{ Width, Height float32 } `json:"resize"`
		Screenshot          string                           `json:"screenshot"`
		ScreenshotZoom      string                           `json:"screenshot_zoom"`
		ScreenshotResize    *struct{ Width, Height float32 } `json:"screenshot_resize"`
		Backlog             backlogSmokeCommand              `json:"backlog"`
		Stream              *bool                            `json:"stream"`
		StreamSeconds       *float64                         `json:"stream_seconds"`
		Prompt              *string                          `json:"prompt"`
		CloseDevices        bool                             `json:"close_devices"`
		ResetText           bool                             `json:"reset_text"`
		Quality             string                           `json:"quality"`
		CaptureMode         string                           `json:"capture_mode"`
		ScrollTranscript    bool                             `json:"scroll_transcript"`
		Release012Prepare   bool                             `json:"release012_prepare"`
	}
	path := filepath.Join(d.directory, "window-command.json")
	if err := store.ReadJSON(path, &command); err != nil {
		return
	}
	if err := os.Remove(path); err != nil {
		return
	}
	d.applyBacklogSmokeCommand(command.Backlog)
	if command.Release012Prepare {
		d.prepareRelease012Smoke()
	}
	if command.Provider == config.ProviderBedrock || command.Provider == config.ProviderWhisper {
		d.setTranscriptionProvider(command.Provider)
	}
	if command.Stream != nil && d.settings.Capture.Stream != *command.Stream {
		d.toggleStream()
	}
	if command.CaptureMode != "" {
		d.changeCaptureMode(command.CaptureMode)
	}
	if command.ToggleProcessing {
		d.toggleProcessingPanel(d.document)
	}
	if command.ToggleTranscription {
		d.toggleTranscriptionPanel(d.document)
	}
	if command.ToggleRecorder {
		d.toggleRecorderPanel()
	}
	if command.InvertPanels != nil {
		updated := cloneSettings(d.settings)
		updated.Appearance.InvertPanels = *command.InvertPanels
		d.persistPreferences(updated, func(err error) {
			if err != nil {
				d.showError(err.Error(), "")
			}
		})
	}
	if command.RecorderSize == config.RecordingSmall || command.RecorderSize == config.RecordingMedium || command.RecorderSize == config.RecordingLarge {
		d.resizeRecordingPanel(command.RecorderSize)
	}
	if command.StreamSeconds != nil {
		streamInput.feed(*command.StreamSeconds)
	}
	if command.Prompt != nil {
		d.editor.SetText(*command.Prompt)
	}
	if command.ResetText {
		d.clearText(true)
	}
	if command.Quality == "fast" || command.Quality == "quality" {
		d.settings.Quality = command.Quality
		d.syncDocumentProvider(d.document)
	}
	if command.CloseDevices && d.deviceForm != nil {
		d.deviceForm.popup.Hide()
	}
	if command.CloseOutputVolume && d.outputVolumeForm != nil {
		d.outputVolumeForm.popup.Hide()
	}
	if command.CloseCaptureVolumes && d.captureVolumes.popup != nil {
		d.captureVolumes.popup.Hide()
	}
	if command.CloseGroupWidth && d.groupWidthForm != nil {
		d.groupWidthForm.popup.Hide()
	}
	if command.Theme != "" {
		d.setTheme(command.Theme)
	}
	if command.GroupName != "" {
		if err := d.renameGroup(d.groups[0], command.GroupName); err != nil {
			d.showError(err.Error(), "")
		}
	}
	if command.NewTabs > 0 {
		first := d.documents[0]
		d.tabs.Select(first.item)
		for range min(command.NewTabs, 8) {
			d.addDocument()
		}
		d.tabs.Select(first.item)
	}
	if command.CloudModel != "" {
		d.setCloudModel(command.CloudModel, false)
	}
	if command.CloudStreamModel != "" {
		d.setCloudModel(command.CloudStreamModel, true)
	}
	if command.Screenshot == "capture" {
		d.captureScreenshot()
	}
	if command.Screenshot == "links" {
		d.document.processEditor.SetText("Leia [Documentação oficial](https://example.com/docs?q=go#intro).\n\nTexto sintético com links.")
		d.updateResultLinks(d.document, true)
	}
	if command.Screenshot == "record" {
		d.recordAction()
	}
	for editor := range d.screenshotEditors {
		if command.ScreenshotZoom != "" {
			editor.zoom.SetSelected(command.ScreenshotZoom)
		}
		if command.ScreenshotResize != nil {
			editor.window.Resize(fyne.NewSize(command.ScreenshotResize.Width, command.ScreenshotResize.Height))
		}
		switch command.Screenshot {
		case "close":
			editor.window.Close()
		case "text":
			// The native smoke exercises insertion with accents and multiline
			// text. The regular UI uses a keyboard entry dialog.
			if err := editor.document.AddText(
				screenshot.Point{X: 12, Y: 30}, "Olá!\nTeste", screenshot.FontBold, 20,
				color.NRGBA{R: 255, G: 220, A: 255},
			); err != nil {
				d.status.SetText(err.Error())
			}
			editor.refresh()
		case "preview":
			painter := reflect.ValueOf(editor.window.Canvas()).MethodByName("Painter").Call(nil)[0].Interface()
			capture := painter.(interface{ Capture(fyne.Canvas) image.Image })
			pixels := capture.Capture(editor.window.Canvas())
			file, err := os.Create(filepath.Join(d.directory, "screenshot-editor.png"))
			if err == nil {
				err = png.Encode(file, pixels)
				err = errors.Join(err, file.Close())
			}
			if err != nil {
				d.status.SetText(err.Error())
			}
		}
		break
	}
	if command.CloseSettings && d.settingsForm != nil {
		d.settingsForm.dialog.Hide()
	}
	if command.ScrollBottom {
		d.columnScroll.ScrollToBottom()
	}
	if command.ScrollTop {
		d.columnScroll.ScrollToTop()
	}
	if command.ScrollTranscript {
		scroll := d.document.transcriptReadScroll
		if link := d.smokeLink(d.document.transcriptPreview, 0); link != nil {
			viewport := d.smokeRectangle(scroll)
			offset := scroll.Offset
			offset.Y += link.Y + link.Height/2 - viewport.Y - viewport.Height/2
			scroll.ScrollToOffset(offset)
		}
	}
	if command.Hover {
		d.settingsButton.hint.show(d.settingsButton)
	}
	if command.CloseMenu {
		if menu, ok := d.window.Canvas().Focused().(*widget.PopUpMenu); ok {
			menu.Dismiss()
		}
	}
	if command.CloseDialog {
		if overlays := d.window.Canvas().Overlays().List(); len(overlays) > 0 {
			for _, object := range test.LaidOutObjects(overlays[len(overlays)-1]) {
				if button, ok := object.(*widget.Button); ok && (button.Text == "Cancelar" || button.Text == "Fechar") {
					test.Tap(button)
					break
				}
			}
		}
	}
	if command.SettingsSection != nil && d.settingsForm != nil {
		d.settingsForm.sections.SelectIndex(*command.SettingsSection)
	}
	if command.Resize != nil {
		d.window.Resize(fyne.NewSize(command.Resize.Width, command.Resize.Height))
	}
	if command.Open != "" {
		d.hints.hide()
	}
	switch command.Open {
	case "groups", "row":
		d.tabs.renderer.showGroupTabs(d.tabs.renderer.rows[0])
	case "settings":
		d.openSettings()
	case "group-context":
		label := d.tabs.renderer.rows[0].label
		label.TappedSecondary(&fyne.PointEvent{AbsolutePosition: d.smokeRectanglePosition(label)})
	case "rename":
		d.openRenameGroup(d.documents[0].group)
	case "close-tab":
		d.requestCloseDocument(d.documents[0].item)
	case "close-group":
		d.requestCloseGroup(d.documents[0].group)
	case "group-width":
		d.openGroupNameWidth(d.groups[0])
	}
	switch command.Capture {
	case "initial", "expanded", "collapsed", "settings", "minimum", "maximized",
		"group-context", "row-menu", "rename", "close-tab", "close-group", "transcript-links", "stream-links", "conversation",
		"volume-team", "volume-adjusted", "retranscription", "play-loaded", "play-paused", "device-shortcut-left",
		"recorder-medium", "recorder-large", "cloud-complete-left", "cloud-stream-left", "volume-reset",
		"theme-light", "theme-dark", "theme-green", "group-width", "group-scroll-hover",
		"audio-history-old", "audio-history-new", "engine-menu", "model-complete",
		"model-stream", "model-local", "play-position", "processing-collapsed", "transcription-collapsed",
		"both-collapsed", "panels-inverted", "wide-recorder",
		"backlog-groups", "backlog-zoom", "backlog-speed", "backlog-settings", "backlog-llm",
		"backlog-about", "backlog-cleanup", "backlog-evaluation",
		"release011-title-controls", "release011-groups", "release011-recorder-collapsed",
		"release011-recorder-collapsed-stacked", "release011-recorder-collapsed-inline",
		"release011-recorder-collapsed-column",
		"release011-recorder-small-narrow", "release011-recorder-small-wide",
		"release011-recorder-medium-narrow", "release011-recorder-medium-wide",
		"release011-recorder-large-narrow", "release011-recorder-large-wide",
		"release011-settings-narrow", "release011-settings-wide", "release011-s3",
		"release011-audio-devices", "release011-device-refresh", "release011-video-presets",
		"release011-slow-playback", "release011-folders",
		"release012-ready-pt", "release012-ready-en",
		"release012-settings-pt", "release012-settings-en",
		"release012-pt-narrow", "release012-pt-wide",
		"release012-en-narrow", "release012-en-wide",
		"release012-mode-menu", "release012-models",
		"release012-live-two-turns", "release012-live-history", "release012-return-process",
		"backlog-settings-narrow-480", "backlog-settings-narrow-320":
		file, err := os.Create(filepath.Join(d.directory, command.Capture+".png"))
		if err == nil {
			// Calling Canvas.Capture nests the same GL context in this Fyne
			// release. Access its exported Painter method only in this test
			// executable and let the painter own the single capture context.
			painter := reflect.ValueOf(d.window.Canvas()).MethodByName("Painter").Call(nil)[0].Interface()
			capture := painter.(interface{ Capture(fyne.Canvas) image.Image })
			err = errors.Join(png.Encode(file, capture.Capture(d.window.Canvas())), file.Close())
		}
		if err != nil {
			d.status.SetText("Falha ao capturar o canvas nativo.")
		}
	}
}

func (d *desktop) smokeMenuItems(menu *widget.PopUpMenu) ([]smokeRectangle, []string) {
	var rectangles []smokeRectangle
	var labels []string
	for _, item := range menu.Items {
		value := reflect.ValueOf(item)
		if value.Kind() != reflect.Pointer {
			continue
		}
		field := value.Elem().FieldByName("Item")
		if field.IsValid() && field.CanInterface() {
			if description, ok := field.Interface().(*fyne.MenuItem); ok {
				rectangles = append(rectangles, d.smokeRectangle(item))
				labels = append(labels, description.Label)
			}
		}
	}
	return rectangles, labels
}

func (d *desktop) smokeLink(object fyne.CanvasObject, depth int) *smokeRectangle {
	if depth > 20 {
		return nil
	}
	if link, ok := object.(*widget.Hyperlink); ok {
		rect := d.smokeRectangle(object)
		// Block/list links fill their row, but only the label receives clicks.
		rect.Width = min(rect.Width, link.MinSize().Width)
		return &rect
	}
	var children []fyne.CanvasObject
	if container, ok := object.(*fyne.Container); ok {
		children = container.Objects
	} else if widget, ok := object.(fyne.Widget); ok {
		children = test.WidgetRenderer(widget).Objects()
	}
	for _, child := range children {
		if result := d.smokeLink(child, depth+1); result != nil {
			return result
		}
	}
	return nil
}

func (d *desktop) smokeRectanglePosition(object fyne.CanvasObject) fyne.Position {
	return d.application.Driver().AbsolutePositionForObject(object).Add(fyne.NewPos(8, 8))
}
