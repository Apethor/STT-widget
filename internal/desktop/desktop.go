// Package desktop implements the Portuguese Fyne desktop interface.
package desktop

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ai-talker/internal/branding"
	"ai-talker/internal/cloud"
	"ai-talker/internal/config"
	"ai-talker/internal/platform"
	"ai-talker/internal/processing"
	"ai-talker/internal/realtime"
	"ai-talker/internal/recorder"
	"ai-talker/internal/secrets"
	"ai-talker/internal/soundboard"
	"ai-talker/internal/store"
	"ai-talker/internal/transcript"
	"ai-talker/internal/whisper"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	fynedesktop "fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// Runner is the desktop's consumer-side contract with the transcription core.
// The foreground request always leaves PersistLatest false: only an accepted
// result on the current UI generation may replace last-transcript.json.
type Runner interface {
	Transcribe(context.Context, string, config.Settings, transcript.Request) (transcript.Result, error)
}

type recording interface {
	Start(context.Context) (string, error)
	Stop() (string, error)
	Pause()
	Resume()
	Snapshot() recorder.Snapshot
}

type screenVideoCapture interface {
	Start(context.Context) error
	Pause(context.Context) error
	Resume(context.Context) error
	Finish(context.Context, []recorder.Track) (string, error)
	Abort() error
}

// dependencies are private seams for headless tests. Dispatch must schedule
// callbacks on one UI thread; the normal driver uses fyne.Do.
type dependencies struct {
	dispatch              func(func())
	newRecorder           func(int, string) recording
	newConfiguredRecorder func(config.Settings, string) recording
	newPlayer             func(string) audioPlayer
	newSoundEngine        func() *soundboard.Engine
	newConversation       func(context.Context, config.Settings, realtime.Options, func(realtime.Event)) (voiceConnection, error)
	newResponsePlayer     func(string, int, int) responsePlayer
	verify                func(context.Context, config.Settings) error
	verifyBucket          func(context.Context, config.Settings) (cloud.BucketCheck, error)
	process               func(context.Context, config.Settings, processing.Request) (processing.Result, error)
	copy                  func(string) error
	openFolder            func(string) error
	openURL               func(string) error
	openImage             func(string) error
	chooseFile            func(context.Context, platform.FileDialogOptions) (string, error)
	setPinned             func(bool) error
	activate              func() error
	minimize              func() error
	selectScreenshot      func(context.Context) (image.Image, error)
	selectVideoRegion     func(context.Context) (platform.ScreenRegion, error)
	newVideoRecorder      func(string, string, platform.ScreenRegion) (screenVideoCapture, error)
	openFile              func(string) error
	ownWindow             func(string) error
	darkTitlebar          func(bool) error
	windowFrame           func(bool, color.NRGBA, platform.WindowHeader) error
	microphones           func() ([]recorder.Device, error)
	playbackDevices       func() ([]recorder.Device, error)
	profiles              func() ([]string, error)
	confirmClose          func(fyne.Window, func(bool))
	confirmTabs           func(fyne.Window, func(bool))
	confirmDiscard        func(fyne.Window, func(bool))
	downloadWhisperEngine func(context.Context, string, func(whisper.DownloadProgress)) (string, error)
	downloadWhisperModel  func(context.Context, string, string, string, func(whisper.DownloadProgress)) (string, error)
	testWhisper           func(context.Context, string, config.Settings) (whisper.CheckResult, error)
	now                   func() time.Time
	vault                 secrets.Vault
	writeWorkspace        func(string, workspaceSnapshot) error
	tickInterval          time.Duration
}

type captureState uint8

const (
	captureIdle captureState = iota
	captureStarting
	captureRunning
	captureStopping
)

type captureAction uint8

const (
	keepCapture captureAction = iota
	transcribeCapture
	newCapture
	closeCapture
	failedCapture
	discardCapture
)

// All mutable fields below belong to the Fyne UI thread except ctx, deps,
// workers, stopped, tickQueued and the explicitly atomic preference version.
// In particular, worker goroutines never read widgets or job/capture state.
type desktop struct {
	// The embedded document is the selected tab. Workers always capture their
	// own document explicitly; selecting a tab never changes a worker's owner.
	*document
	application fyne.App
	window      fyne.Window
	ctx         context.Context
	cancel      context.CancelFunc
	directory   string
	runner      Runner
	deps        dependencies
	instance    *guiInstance
	workers     sync.WaitGroup
	stopped     atomic.Bool
	tickQueued  atomic.Bool

	settings        config.Settings
	localization    *interfaceLocalization
	preferenceMu    sync.Mutex
	preferenceEpoch atomic.Uint64

	closeAfterJob       bool
	closing             bool
	hidden              bool
	started             bool
	authEpoch           uint64
	authCancel          context.CancelFunc
	documents           []*document
	tabs                *documentTabs
	nextDocument        uint64
	groups              []uint64
	groupNames          map[uint64]string
	groupAutomaticNames map[uint64]string
	groupNameWidths     map[uint64]float32
	groupWidthForm      *groupWidthForm
	nextGroup           uint64
	selectingTabs       bool
	workspaceInvalid    bool
	processSlots        chan struct{}
	processingForm      *processingForm
	mergeBusy           bool
	mergeGeneration     uint64
	mergeCancel         context.CancelFunc
	mergeGroup          uint64
	joinButton          *actionButton
	mergeButton         *actionButton
	closedTab           *closedDocument
	closedTabs          []*closedDocument
	undoCloseButton     *actionButton
	tabsDialog          bool

	capture               captureState
	voiceSession          *voiceSession
	responsePlayback      *responsePlayback
	captureEpoch          uint64
	captureCancel         context.CancelFunc
	recorder              recording
	afterCapture          captureAction
	captureError          string
	captureDocument       *document
	playback              *audioPlayback
	playbackSlot          chan struct{}
	soundPlaybackSlot     chan struct{}
	settingsForm          *settingsForm
	transcribeSetup       *transcribeSetupForm
	deviceForm            *captureDeviceForm
	unavailableMicrophone bool
	unavailablePlayback   bool
	outputVolumeForm      *playbackVolumeForm
	playbackRates         map[string]float64
	closeDialog           bool

	recordButton          *widget.Button
	pauseButton           *actionButton
	stopPlaybackButton    *actionButton
	outputVolumeButton    *actionButton
	playbackSpeedButton   *actionButton
	discardButton         *actionButton
	discardDialog         bool
	captureOrigin         *captureOrigin
	newButton             *actionButton
	importButton          *actionButton
	audioHistoryButton    *actionButton
	pinButton             *actionButton
	sizeButton            *actionButton
	deviceButton          *actionButton
	streamButton          *actionButton
	captureSelect         *widget.Select
	captureVolumes        *captureVolumeControls
	syncingCapture        bool
	settingsButton        *actionButton
	minimizeButton        *actionButton
	windowCloseButton     *actionButton
	folderButton          *actionButton
	screenshotButton      *actionButton
	videoButton           *actionButton
	soundboardButton      *actionButton
	openVideoButton       *actionButton
	screenshotBusy        bool
	screenshotCancel      context.CancelFunc
	screenshotEditors     map[*screenshotEditor]struct{}
	videoRegion           *platform.ScreenRegion
	videoArmed            bool
	videoSelecting        bool
	videoSession          screenVideoCapture
	videoCommandSession   screenVideoCapture
	videoCommandTail      <-chan struct{}
	soundboard            *soundboard.Board
	soundboardLoading     bool
	soundboardPreparing   bool
	soundEngine           *soundboard.Engine
	soundboardFrameCancel context.CancelFunc
	soundboardWindow      fyne.Window
	soundboardVolume      *widget.Slider
	soundboardStatus      *widget.Label
	soundPlayback         *soundEffectPlayback
	caption               *themedText
	copyAudioPath         *actionButton
	brandIcon             *canvas.Image
	timer                 *themedText
	recordState           *widget.Label
	awsLabel              *themedText
	awsIndicator          *connectionDot
	status                *widget.Label
	progress              *progressLine
	wave                  *waveform
	palette               *talkerTheme
	themeButton           *actionButton
	hints                 *hintLayer
	recordPanel           *themedPanel
	recorderLayout        *recordingLayout
	recorderDivider       *recordingSplitSurface
	recordingResize       bool
	header                fyne.CanvasObject
	headerTools           *fyne.Container
	footer                fyne.CanvasObject
	content               *fyne.Container
	rowSplit              *container.Split
	columnScroll          *container.Scroll
	columnHandle          *heightHandle
	columnHeight          float32
}

func newDesktop(
	ctx context.Context,
	application fyne.App,
	settings config.Settings,
	directory string,
	runner Runner,
	deps dependencies,
) *desktop {
	lifetime, cancel := context.WithCancel(ctx)
	if deps.now == nil {
		deps.now = time.Now
	}
	if deps.vault == nil {
		deps.vault = secrets.Native{}
	}
	d := &desktop{
		application:       application,
		ctx:               lifetime,
		cancel:            cancel,
		directory:         directory,
		runner:            runner,
		deps:              deps,
		settings:          cloneSettings(settings),
		localization:      newInterfaceLocalization(settings.InterfaceLocale()),
		processSlots:      make(chan struct{}, 2),
		playbackSlot:      make(chan struct{}, 1),
		soundPlaybackSlot: make(chan struct{}, 1),
		playbackRates:     make(map[string]float64),
		groups:            []uint64{1}, nextGroup: 1,
		groupNames:          make(map[uint64]string),
		groupAutomaticNames: make(map[uint64]string),
		groupNameWidths:     make(map[uint64]float32),
		screenshotEditors:   make(map[*screenshotEditor]struct{}),
	}
	d.syncConcurrency(settings)
	d.palette = newTheme(settings.Appearance.Theme)
	application.Settings().SetTheme(d.palette)
	application.SetIcon(fyne.NewStaticResource("ai-talker.png", branding.PNG))
	d.window = newDesktopWindow(application)
	d.window.SetIcon(application.Icon())
	d.window.SetMaster()
	d.window.SetPadded(false)
	d.hints = newHintLayer(d.window.Canvas())
	d.build()
	d.window.Resize(d.initialWindowSize())
	d.resizeRecordingPanel(settings.Appearance.RecordingSize)
	d.window.CenterOnScreen()
	d.window.SetCloseIntercept(d.guard(d.requestClose))
	d.window.SetOnClosed(d.guard(d.finish))
	d.shortcuts()
	d.syncControls()
	d.localizeInterface()
	return d
}

func (d *desktop) build() {
	title := label("STT-widget", 20, textColor)
	title.TextStyle.Bold = true
	title.truncate = true
	icon := canvas.NewImageFromResource(headerIcon(d.settings.Appearance.Theme))
	d.brandIcon = icon
	icon.FillMode = canvas.ImageFillContain
	icon.SetMinSize(fyne.NewSize(24, 24))
	d.pinButton = d.iconButton("Fixar janela", pinIcon, d.togglePin)
	d.settingsButton = d.iconButton("Configurações", theme.SettingsIcon(), d.openSettings)
	d.minimizeButton = d.iconButton("Minimizar janela", theme.WindowMinimizeIcon(), d.minimizeWindow)
	d.windowCloseButton = d.iconButton("Fechar aplicativo", theme.WindowCloseIcon(), d.requestClose)
	d.themeButton = d.iconButton("Mudar tema", theme.ColorPaletteIcon(), d.chooseTheme)
	d.themeButton.Alignment = widget.ButtonAlignLeading
	d.themeButton.dotColor = rgb(0x79deb5)
	d.syncThemeButton()
	d.awsLabel = label("AWS Bedrock", 11, mutedColor)
	d.awsLabel.Alignment = fyne.TextAlignTrailing
	d.awsIndicator = d.newConnectionDot()
	d.headerTools = container.NewHBox(
		d.themeButton, d.pinButton, d.settingsButton, d.minimizeButton, d.windowCloseButton,
	)
	d.header = container.NewThemeOverride(container.NewBorder(
		nil, nil, nil, d.headerTools,
		container.NewBorder(nil, nil, icon, nil, title),
	), headerTheme{Theme: d.palette})

	d.caption = label("NOVO ÁUDIO", 11, mutedColor)
	d.caption.TextStyle.Bold = true
	d.caption.truncate = true
	// Keep every recorder shortcut accessible at 320 pixels with Copy visible.
	d.caption.minimumWidth = 12
	d.caption.hint = &toolHint{layer: d.hints}
	d.copyAudioPath = d.iconButton("Copiar caminho da gravação", theme.ContentCopyIcon(), d.copyRecordingPath)
	d.copyAudioPath.compact = true
	d.copyAudioPath.Hide()
	d.openVideoButton = d.iconButton("Abrir o vídeo desta gravação", videoIcon, d.openRecordedVideo)
	d.openVideoButton.compact = true
	d.openVideoButton.Hide()
	d.newButton = d.iconButton("Novo áudio", theme.ContentAddIcon(), d.newAudio)
	d.importButton = d.iconButton("Importar áudio", theme.UploadIcon(), d.audioFileAction)
	d.audioHistoryButton = d.iconButton("Histórico de áudios desta aba", theme.HistoryIcon(), d.openAudioHistory)
	d.sizeButton = d.iconButton("Reduzir gravação para tamanho Médio", theme.ViewRestoreIcon(), d.toggleRecordingSize)
	d.deviceButton = d.iconButton("Fontes e dispositivos de gravação", inputDevicesIcon, d.openCaptureDevices)
	d.streamButton = d.iconButton("Modos de gravação", streamIcon, d.chooseAudioMode)
	d.captureSelect = widget.NewSelect([]string{microphoneChoice, systemChoice, teamChoice}, d.changeCaptureMode)
	d.syncCaptureOptions()
	d.syncRecordingSizeButton()
	d.timer = label("00:00", 36, textColor)
	d.timer.Alignment = fyne.TextAlignCenter
	d.timer.TextStyle.Monospace = true
	d.recordState = widget.NewLabel("Pronto para começar")
	d.recordState.Importance = widget.SuccessImportance
	d.recordState.Alignment = fyne.TextAlignCenter
	d.recordState.Wrapping = fyne.TextWrapWord
	d.wave = newWaveform()
	d.wave.onSeek = d.seekPlayback
	d.captureVolumes = d.newCaptureVolumes()
	d.syncCaptureVolumes()
	d.recordButton = widget.NewButton("●  Gravar áudio", d.guard(d.recordAction))
	d.recordButton.Importance = widget.HighImportance
	d.pauseButton = d.iconButton("Pausar", theme.MediaPauseIcon(), d.pauseAction)
	d.stopPlaybackButton = d.iconButton("Parar reprodução · próximo Play começa do início", theme.MediaStopIcon(), d.stopPlaybackAction)
	d.stopPlaybackButton.Hide()
	d.outputVolumeButton = d.iconButton("Volume da reprodução · clicar para ajustar", theme.VolumeUpIcon(), d.openPlaybackVolume)
	d.playbackSpeedButton = d.iconButton("Velocidade de reprodução", theme.MediaFastForwardIcon(), d.openPlaybackSpeed)
	d.discardButton = d.iconButton("Descartar gravação", theme.CancelIcon(), d.requestDiscardRecording)
	d.discardButton.Hide()
	d.recordPanel = panel(d.recordingContent())

	d.progress = newProgressLine()
	d.status = widget.NewLabel("O áudio só é enviado ao concluir.")
	d.status.Wrapping = fyne.TextWrapWord
	d.folderButton = d.iconButton("Abrir pasta · gravações, screenshots ou vídeos", theme.FolderOpenIcon(), d.chooseFolder)
	d.folderButton.Importance = widget.LowImportance
	d.joinButton = d.iconButton("Juntar transcrições na primeira aba", theme.ContentPasteIcon(), d.joinTranscripts)
	d.mergeButton = d.iconButton("Mesclar transcrições por LLM na primeira aba", mergeIcon, d.mergeTranscripts)
	d.screenshotButton = d.iconButton("Capturar área da tela · Esc cancela", cameraIcon, d.captureScreenshot)
	d.screenshotButton.compact = true
	d.videoButton = d.iconButton("Selecionar área de vídeo para a próxima gravação", videoIcon, d.armVideoCapture)
	d.videoButton.compact = true
	d.soundboardButton = d.iconButton("Abrir a botoneira de sons", soundboardIcon, d.openSoundboard)
	d.soundboardButton.compact = true
	d.footer = container.NewVBox(
		d.progress,
		container.NewThemeOverride(d.status, textTheme{Theme: d.application.Settings().Theme(), size: 11}),
		container.NewBorder(nil, nil, container.NewHBox(d.joinButton, d.mergeButton),
			container.NewHBox(d.folderButton, d.awsIndicator),
			container.NewCenter(container.NewHBox(d.soundboardButton, d.videoButton, d.screenshotButton))),
	)
	d.tabs = newDocumentTabs()
	d.undoCloseButton = d.iconButton("Desfazer fechamento da aba", theme.ContentUndoIcon(), d.undoCloseDocument)
	d.undoCloseButton.Hide()
	d.tabs.undoClose = d.undoCloseButton
	d.tabs.onSecondary = d.tabContextMenu
	d.tabs.onGroupSecondary = d.groupContextMenu
	d.tabs.groupIDs = func() []uint64 { return d.groups }
	d.tabs.groupFor = d.groupForItem
	d.tabs.groupName = d.groupName
	d.tabs.groupNameLimit = d.groupNameWidth
	d.tabs.groupNameHandle = d.newGroupNameHandle
	d.tabs.groupMenu = d.groupTabsMenuFor
	d.tabs.createGroup = func() { d.addGroup() }
	d.tabs.moveTab = d.moveDocumentTab
	d.tabs.moveGroup = d.moveGroup
	d.tabs.createInGroup = func(group uint64) *container.TabItem {
		if len(d.documents) >= maxDocuments {
			d.status.SetText("Feche uma aba antes de abrir mais.")
			return nil
		}
		return d.createDocumentInGroup(group).item
	}
	d.document = d.createDocument()
	d.tabs.Append(d.document.item)
	d.tabs.CreateTab = func() *container.TabItem {
		if len(d.documents) >= maxDocuments {
			d.status.SetText("Feche uma aba antes de abrir mais.")
			return nil
		}
		return d.createDocument().item
	}
	d.tabs.OnSelected = func(item *container.TabItem) {
		d.guard(func() {
			for _, doc := range d.documents {
				if doc.item == item {
					d.selectDocument(doc)
					return
				}
			}
		})()
	}
	d.tabs.CloseIntercept = func(item *container.TabItem) {
		d.guard(func() { d.requestCloseDocument(item) })()
	}
	d.arrangePanels()
}

func (d *desktop) shortcuts() {
	actions := map[fyne.KeyName]func(){
		fyne.KeyR:     d.recordAction,
		fyne.KeySpace: d.pauseAction,
		fyne.KeyP:     d.togglePin,
		fyne.KeyS:     d.captureScreenshot,
	}
	for key, action := range actions {
		shortcut := &fynedesktop.CustomShortcut{
			KeyName:  key,
			Modifier: fyne.KeyModifierControl | fyne.KeyModifierShift,
		}
		d.window.Canvas().AddShortcut(shortcut, func(fyne.Shortcut) { d.guard(action)() })
	}
}

func (d *desktop) start(initialError string) {
	if d.started || d.closing {
		return
	}
	d.started = true
	if !d.loadWorkspace() {
		d.loadPrevious()
	}
	d.prepareSoundboard()
	if initialError != "" {
		d.showError(initialError, "")
	}
	d.checkAWS()
	d.applyNativeWindow()
	if d.deps.tickInterval > 0 {
		d.workers.Go(func() {
			ticker := time.NewTicker(d.deps.tickInterval)
			defer ticker.Stop()
			for {
				select {
				case <-d.ctx.Done():
					return
				case <-ticker.C:
					if d.tickQueued.CompareAndSwap(false, true) {
						d.post(func() {
							defer d.tickQueued.Store(false)
							d.tick()
						})
					}
				}
			}
		})
	}
	d.workers.Go(func() {
		<-d.ctx.Done()
		if !d.stopped.Load() {
			d.deps.dispatch(d.guard(d.finish))
		}
	})
}

// post is the only bridge from a worker to widgets. Generation checks happen
// inside the scheduled callback, never before dispatch on a worker goroutine.
func (d *desktop) post(action func()) {
	if d.stopped.Load() {
		return
	}
	d.deps.dispatch(d.guard(func() {
		if !d.closing {
			action()
		}
	}))
}

func (d *desktop) postJob(doc *document, generation uint64, action func()) {
	d.post(func() {
		if !doc.closed && generation == doc.generation && doc.busy {
			action()
		}
	})
}

func (d *desktop) guard(action func()) func() {
	return func() {
		if d.closing {
			return
		}
		defer func() {
			if recovered := recover(); recovered != nil {
				slog.Error("falha em uma ação da interface", "panic", recovered)
				d.invalidateJob()
				d.reveal()
				d.showError(fmt.Sprintf("Não foi possível concluir essa ação: %v", recovered), "")
			}
		}()
		action()
		d.localizeInterface()
	}
}

func (d *desktop) operation(work func() error, complete func(error)) {
	d.workers.Go(func() {
		err := callOperation(work)
		d.post(func() { complete(err) })
	})
}

func callOperation(work func() error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("não foi possível concluir essa ação: %v", recovered)
		}
	}()
	return work()
}

func (d *desktop) syncTextActions() {
	d.syncDocumentActions(d.document)
}

func (d *desktop) syncControls() {
	d.recordButton.Enable()
	d.recordButton.Importance = widget.HighImportance
	d.recordButton.SetText("●  Gravar áudio")
	if d.videoArmed {
		d.recordButton.SetText("●  Gravar áudio e vídeo")
	}
	switch {
	case d.capture == captureStarting:
		d.recordButton.SetText("Abrindo gravação…")
		d.recordButton.Disable()
	case d.capture == captureStopping:
		d.recordButton.SetText("Salvando áudio…")
		d.recordButton.Disable()
	case d.capture == captureRunning:
		d.recordButton.Importance = widget.DangerImportance
		d.recordButton.SetText("■  Parar e transcrever")
	case d.busy:
		d.recordButton.Importance = widget.MediumImportance
		d.recordButton.SetText("Cancelar transcrição")
	}
	if d.videoSelecting {
		d.recordButton.Disable()
	}
	d.recordButton.Refresh()
	d.syncRecordingName()
	d.syncVideoButton()
	d.syncPlaybackControls()
	if d.capture == captureRunning {
		d.discardButton.Show()
	} else {
		d.discardButton.Hide()
	}
	setEnabled(d.discardButton, d.capture == captureRunning && !d.discardDialog)
	if d.discardDialog {
		d.recordButton.Disable()
	}
	setEnabled(d.importButton, d.capture == captureIdle)
	d.settingsButton.Enable()
	setEnabled(d.newButton, !d.discardDialog)
	setEnabled(d.retryButton, !d.busy && d.capture == captureIdle && d.lastAudio != "")
	d.syncPin()
	d.syncTextActions()
	d.syncCaptureOptions()
	d.syncCombineControls()
	d.syncAudioHistoryButton()
	d.syncRecorderSurface()
	d.syncVoiceControls()
}

func (d *desktop) startJob(source string) {
	d.startDocumentJob(d.document, source)
}

func (d *desktop) startDocumentJob(doc *document, source string) {
	if !sameAudio(doc.lastAudio, source) && !d.preserveAudioHistory(doc) {
		return
	}
	doc.transcriptionRun = ""
	d.startDocumentTranscription(doc, source, false)
}

func (d *desktop) startRecordedJob(doc *document, source string) {
	doc.transcriptionRun = ""
	var shots []string
	videoPath := ""
	if doc.recordedAppend != nil && doc.recordedAppend.Source == source {
		shots = doc.recordedAppend.Screenshots
		videoPath = doc.recordedAppend.VideoPath
	}
	doc.recordedAppend = &recordedAppend{
		Source: source, Prefix: doc.editor.Text, Combined: doc.editor.Text,
		Screenshots: shots, VideoPath: videoPath,
	}
	d.startDocumentTranscription(doc, source, true)
}

func (d *desktop) startImportedJob(doc *document, source string) {
	d.returnToCurrentPrompt(doc)
	if d.closing || doc.closed || d.capture != captureIdle {
		return
	}
	if !d.preserveAudioHistory(doc) {
		return
	}
	d.stopDocumentPlayback(doc)
	// Imported audio follows the same append/retry contract as a recording.
	// The previous stream is already represented in the preserved editor.
	if doc.live != nil {
		d.cancelLiveRecording(doc.live)
		doc.live = nil
	}
	doc.recordedAppend = &recordedAppend{Source: source, Prefix: doc.editor.Text, Combined: doc.editor.Text}
	doc.transcriptionRun = ""
	doc.view.timer = "--:--"
	d.startDocumentTranscription(doc, source, true)
}

func (d *desktop) retryDocument(doc *document) {
	if doc.browsingPrompts() {
		return
	}
	if doc.live != nil && doc.live.Source == doc.lastAudio {
		d.retryLiveRecording(doc)
		return
	}
	appendRecording := doc.recordedAppend != nil && doc.recordedAppend.Source == doc.lastAudio
	d.startDocumentTranscription(doc, doc.lastAudio, appendRecording)
}

func (d *desktop) startDocumentTranscription(doc *document, source string, appendRecording bool) {
	if d.closing || doc.closed || d.capture != captureIdle {
		return
	}
	d.invalidateDocumentJob(doc)
	if !sameAudio(doc.lastAudio, source) {
		doc.audioMissing = false
	}
	doc.lastAudio = source
	if !d.ensureTranscriptionSetup(doc, d.settings, func() {
		d.startDocumentTranscription(doc, source, appendRecording)
	}) {
		return
	}
	if !d.prepareDocumentTranscript(doc, filepath.Base(source), appendRecording) {
		return
	}
	if appendRecording && doc.recordedAppend != nil {
		snapshot := *doc.recordedAppend
		doc.jobAppend = &snapshot
	}
	doc.busy = true
	doc.jobStarted = d.deps.now()
	doc.lastProgressAt = doc.jobStarted
	doc.jobProgress = transcript.Progress{}
	jobContext, cancel := context.WithCancel(d.ctx)
	doc.jobCancel = cancel
	generation := doc.generation
	settings := cloneSettings(d.settings)
	doc.jobProvider = settings.TranscriptionProvider()
	doc.view.recordText = "Verificando se há fala no áudio."
	doc.view.status = "Verificando o áudio…"
	if doc.transcriptionRun != "" {
		doc.view.recordText = "Retranscrevendo o áudio carregado."
		doc.view.status = "Reconhecendo novamente com o modelo selecionado."
	}
	doc.view.progress = 0.02
	d.renderDocument(doc)
	d.syncControls()
	request := transcript.Request{
		OutputDir:       filepath.Join(d.directory, "transcricoes"),
		CacheDir:        d.transcriptionCacheDirectory(doc),
		Workers:         settings.Parallelism(),
		PersistLatest:   false,
		ContinueOnError: true,
		OnProgress: func(progress transcript.Progress) {
			d.postJob(doc, generation, func() { d.showDocumentProgress(doc, progress) })
		},
	}
	d.launchDocument(doc, generation, func() {
		defer cancel()
		result, err := d.runner.Transcribe(jobContext, source, settings, request)
		d.postJob(doc, generation, func() {
			d.completeDocumentJob(doc, result, err)
		})
	})
}

func (d *desktop) invalidateJob() {
	d.invalidateDocumentJob(d.document)
	d.closeAfterJob = false
}

func (d *desktop) cancelJob() {
	d.invalidateJob()
	d.showDocumentCancellation(d.document)
}

func (d *desktop) showDocumentCancellation(doc *document) {
	doc.view.progress = 0
	doc.view.recordText = "Transcrição cancelada."
	doc.view.status = "O áudio foi preservado. Tentar novamente usa o motor selecionado."
	if doc.lastAudio != "" {
		doc.retryButton.Show()
	}
	d.renderDocument(doc)
	d.syncControls()
}

func (d *desktop) showDocumentProgress(doc *document, progress transcript.Progress) {
	doc.lastProgressAt = d.deps.now()
	doc.jobProgress = progress
	doc.view.progress = boundedLevel(progress.Fraction)
	doc.view.status = progress.Message
	if progress.Stage == "wait" {
		doc.view.recordText = "Aguardando a tentativa anterior salvar os trechos."
	}
	if progress.Stage == "transcribe" || progress.Stage == "retry" {
		doc.view.recordText = "Transcrevendo seu áudio completo."
		if doc.jobProvider == config.ProviderWhisper {
			doc.view.recordText = "Transcrevendo neste computador."
		}
	}
	if progress.Total > 1 {
		doc.view.recordText = fmt.Sprintf("Trechos: %d de %d concluídos", progress.Completed, progress.Total)
	}
	d.renderDocument(doc)
}

func (d *desktop) completeDocumentJob(doc *document, result transcript.Result, err error) {
	defer d.enterCurrentPrompt(doc)()
	doc.busy = false
	doc.jobCancel = nil
	if errors.Is(err, context.Canceled) {
		d.invalidateDocumentJob(doc)
		d.showDocumentCancellation(doc)
		if d.hidden {
			d.reveal()
		}
		return
	}
	if err != nil {
		partial := ""
		var failure *transcript.Error
		if errors.As(err, &failure) {
			partial = failure.PartialText
			if failure.Result.Source != "" && doc.transcriptionRun == "" {
				doc.lastResult = &failure.Result
				d.syncTranscriptionDetails(doc)
			}
		}
		if doc.transcriptionRun != "" {
			// A failed comparison keeps the previous complete text visible.
			// The new attempt's completed parts remain in its own cache.
			partial = ""
		}
		var appended *recordedAppend
		if doc.jobAppend != nil && strings.TrimSpace(partial) != "" {
			prefix := recordingPrefix(doc)
			content := appendTranscriptText(prefix, partial)
			shots := doc.jobAppend.Screenshots
			partial = appendTranscriptText(content, screenshotFooter(shots))
			appended = &recordedAppend{
				Source: doc.lastAudio, Prefix: prefix, Combined: partial,
				Content: content, Screenshots: shots, LinksReady: true,
				VideoPath: doc.jobAppend.VideoPath,
			}
		}
		d.showDocumentError(doc, err.Error(), partial)
		doc.view.status = "Transcrição incompleta · o áudio e os trechos aceitos foram preservados."
		if appended != nil && doc.editor.Text == appended.Combined {
			doc.recordedAppend = appended
			d.updateTranscriptLinks(doc, true)
		}
		if strings.TrimSpace(partial) != "" {
			d.recordTranscriptionPrompt(doc)
		}
		doc.retryButton.Show()
		d.syncControls()
		d.finishWhenReady()
		return
	}
	if !d.showJobResult(doc, result) {
		d.reveal()
		return
	}
	d.finishWhenReady()
}

func (d *desktop) prepareDocumentTranscript(doc *document, source string, preserveText bool) bool {
	if err := d.saveDocumentDrafts(doc); err != nil {
		d.showDocumentError(doc, err.Error(), "")
		return false
	}
	if !preserveText {
		doc.live = nil
		if err := d.writeDraft(strings.TrimSpace(doc.processEditor.Text), "", &doc.processDraftText, "Processado"); err != nil {
			d.showDocumentError(doc, err.Error(), "")
			return false
		}
		if err := d.removeDocumentLatest(doc); err != nil {
			d.showDocumentError(doc, err.Error(), "")
			return false
		}
		d.invalidateProcessing(doc)
		doc.processEditor.SetText("")
		doc.processResult = nil
		doc.undo = nil
		doc.lastResult = nil
		doc.recordedAppend = nil
		doc.editor.SetText("")
	}
	setLabel(doc.transcriptName, "Transcrição", textColor)
	doc.sourceLabel.SetText("Português + inglês · sem traduzir sua fala")
	doc.errorLabel.Hide()
	doc.retryButton.Hide()
	d.syncDocumentActions(doc)
	d.syncControls()
	return true
}

func (d *desktop) showResult(result transcript.Result, persist bool) bool {
	return d.showDocumentResult(d.document, result, persist)
}

func (d *desktop) showDocumentResult(doc *document, result transcript.Result, persist bool) bool {
	return d.displayDocumentResult(doc, result, persist, result.Text, false)
}

func (d *desktop) showJobResult(doc *document, result transcript.Result) bool {
	if doc.jobAppend == nil {
		return d.showDocumentResult(doc, result, true)
	}
	prefix := recordingPrefix(doc)
	shots := doc.jobAppend.Screenshots
	for _, path := range shots {
		prefix = removeScreenshotLine(prefix, path)
	}
	content := appendTranscriptText(prefix, result.Text)
	text := appendTranscriptText(content, screenshotFooter(shots))
	if !d.displayDocumentResult(doc, result, true, text, true) {
		return false
	}
	doc.recordedAppend = &recordedAppend{
		Source: result.Source, Prefix: prefix, Combined: text, Content: content,
		Screenshots: shots, LinksReady: true, VideoPath: doc.jobAppend.VideoPath,
	}
	d.updateTranscriptLinks(doc, true)
	doc.jobAppend = nil
	return true
}

func recordingPrefix(doc *document) string {
	if doc.jobAppend != nil && doc.editor.Text == doc.jobAppend.Combined {
		return doc.jobAppend.Prefix
	}
	return doc.editor.Text
}

func appendTranscriptText(previous, next string) string {
	if strings.TrimSpace(next) == "" {
		return previous
	}
	if previous == "" {
		return next
	}
	separator := "\n\n"
	if strings.HasSuffix(previous, "\n\n") {
		separator = ""
	} else if strings.HasSuffix(previous, "\n") {
		separator = "\n"
	}
	return previous + separator + next
}

func (d *desktop) displayDocumentResult(
	doc *document, result transcript.Result, persist bool, text string, preserveUndo bool,
) bool {
	if err := d.saveDocumentDrafts(doc); err != nil {
		d.showDocumentError(doc, err.Error(), "")
		return false
	}
	snapshot := result
	snapshot.Text = text
	doc.lastResult = &snapshot
	doc.lastAudio = result.Source
	if !preserveUndo {
		doc.undo = nil
	}
	doc.editor.SetText(text)
	d.recordTranscriptionPrompt(doc)
	d.updateTranscriptLinks(doc, true)
	setLabel(doc.transcriptName, "Transcrição", textColor)
	doc.sourceLabel.SetText("Português + inglês · sem traduzir sua fala")
	doc.view.timer = clockText(result.DurationSeconds)
	doc.errorLabel.Hide()
	doc.retryButton.Hide()
	doc.view.importance = widget.MediumImportance
	doc.view.recordText = "Nenhuma fala detectada."
	doc.view.status = "Grave outro áudio quando quiser."
	if strings.TrimSpace(result.Text) != "" {
		doc.view.importance = widget.SuccessImportance
		doc.view.recordText = "Texto pronto. Pode editar e copiar."
		doc.view.status = fmt.Sprintf(
			"%s de áudio · %.0fs de processamento",
			clockText(result.DurationSeconds),
			result.ElapsedSeconds,
		)
		if len(result.Parts) > 0 && result.ReusedParts == len(result.Parts) {
			doc.view.status = "Transcrição salva reaproveitada."
		}
		if result.NeedsReview {
			doc.retryButton.Show()
			doc.view.recordText = "Transcrição pronta para conferir."
			doc.view.status = "Há trechos com fala sem texto reconhecido. Você pode tentar novamente."
		}
	} else if detectedSpeech(result) {
		doc.retryButton.Show()
		doc.view.recordText = "Nenhuma fala reconhecida."
		doc.view.status = "Você pode conferir o áudio e tentar novamente."
	}
	if result.NeedsReview && strings.TrimSpace(result.Text) != "" {
		doc.view.importance = widget.WarningImportance
		doc.view.recordText = "Confira a transcrição antes de usar."
		doc.view.status = "Há partes com resposta vazia ou texto incompatível com a duração."
		doc.errorLabel.SetText(resultReviewNotice(result))
		doc.errorLabel.Show()
		doc.retryButton.Show()
	}
	if result.NeedsReview && strings.TrimSpace(result.Text) == "" {
		doc.errorLabel.SetText(resultReviewNotice(result))
		doc.errorLabel.Show()
	}
	d.syncTranscriptionDetails(doc)
	d.syncDocumentActions(doc)
	d.syncControls()
	doc.view.progress = 1
	d.renderDocument(doc)
	if persist {
		// This runs only after the UI-thread generation check in postJob. A
		// cancelled worker can cache parts but cannot write this snapshot.
		if err := d.persistDocumentResult(doc, snapshot); err != nil {
			d.showDocumentError(doc, fmt.Sprintf("Não foi possível salvar a última transcrição: %v", err), "")
			return false
		}
	}
	return true
}

func (d *desktop) showError(message, partial string) {
	d.showDocumentError(d.document, message, partial)
}

func (d *desktop) showDocumentError(doc *document, message, partial string) {
	if strings.TrimSpace(partial) != "" {
		if err := d.saveDocumentDrafts(doc); err != nil {
			message = message + " · " + err.Error()
		} else {
			doc.editor.SetText(partial)
			setLabel(doc.transcriptName, "Transcrição parcial", textColor)
		}
	}
	doc.errorLabel.SetText(shortMessage(message))
	doc.errorLabel.Show()
	doc.view.importance = widget.DangerImportance
	doc.view.recordText = "Não foi possível concluir."
	doc.view.status = "Você pode limpar, gravar outro áudio ou tentar novamente."
	d.renderDocument(doc)
	d.syncDocumentActions(doc)
	d.syncControls()
}

func (d *desktop) retry() {
	if d.lastAudio != "" && !d.busy && d.capture == captureIdle {
		d.retryDocument(d.document)
	}
}

func (d *desktop) clearText(announce bool) bool {
	if d.document.browsingPrompts() {
		return false
	}
	if err := d.saveDraft(); err != nil {
		d.showError(err.Error(), "")
		return false
	}
	if !announce {
		if err := d.writeDraft(
			strings.TrimSpace(d.processEditor.Text), "", &d.processDraftText, "Processado",
		); err != nil {
			d.showError(err.Error(), "")
			return false
		}
	}
	d.invalidateJob()
	if err := d.removeDocumentLatest(d.document); err != nil {
		d.showError(err.Error(), "")
		return false
	}
	if d.captureDocument == d.document && d.capture == captureStopping && d.afterCapture == transcribeCapture {
		d.afterCapture = keepCapture
	}
	if announce && d.editor.Text != "" {
		d.undo = &clearUndo{
			Text: d.editor.Text, Result: d.lastResult, Source: d.lastAudio, Append: d.recordedAppend,
			TranscriptionRun: d.transcriptionRun, AudioMissing: d.audioMissing,
		}
		if d.live != nil {
			d.undo.Live = d.live.archive()
		}
	} else if !announce {
		d.undo = nil
	}
	d.invalidateProcessing(d.document)
	d.document.evaluation = nil
	if !announce {
		d.processEditor.SetText("")
		d.processResult = nil
	}
	d.lastResult = nil
	d.recordedAppend = nil
	d.clearLiveText(d.document)
	d.editor.SetText("")
	setLabel(d.transcriptName, "Transcrição", textColor)
	d.errorLabel.Hide()
	d.retryButton.Hide()
	d.progress.SetValue(0)
	if d.capture == captureIdle {
		d.recordState.Importance = widget.MediumImportance
		d.recordState.SetText("Pronto para outro áudio")
	}
	if announce {
		d.status.SetText("Texto limpo.")
	}
	d.syncControls()
	return true
}

func (d *desktop) newAudio() {
	d.returnToCurrentPrompt(d.document)
	if d.discardDialog {
		return
	}
	if !d.preserveAudioHistory(d.document) {
		return
	}
	if !d.clearText(false) {
		return
	}
	if d.capture != captureIdle {
		d.stopCapture(newCapture)
		return
	}
	d.resetAudio()
}

func (d *desktop) resetAudio() {
	d.stopDocumentPlayback(d.document)
	d.lastAudio = ""
	d.audioMissing = false
	d.transcriptionRun = ""
	d.lastResult = nil
	d.live = nil
	d.wave.reset()
	setLabel(d.timer, "00:00", textColor)
	d.recordState.Importance = widget.SuccessImportance
	d.recordState.SetText("Pronto para começar")
	d.sourceLabel.SetText("Português + inglês · sem traduzir sua fala")
	d.status.SetText("Pronto para um novo áudio.")
	d.progress.SetValue(0)
	d.retryButton.Hide()
	d.syncControls()
}

func (d *desktop) saveDraft() error {
	return d.saveDocumentDrafts(d.document)
}

func (d *desktop) saveDocumentDrafts(doc *document) error {
	prompt, response := d.currentTexts(doc)
	current := strings.TrimSpace(prompt)
	original := ""
	if doc.lastResult != nil {
		original = strings.TrimSpace(doc.lastResult.Text)
	}
	if err := d.writeDraft(current, original, &doc.draftText, "Texto"); err != nil {
		return err
	}
	original = ""
	if doc.processResult != nil {
		original = strings.TrimSpace(doc.processResult.Text)
	}
	return d.writeDraft(strings.TrimSpace(response), original, &doc.processDraftText, "Processado")
}

func (d *desktop) writeDraft(current, original string, previous *string, prefix string) error {
	if current == "" || current == original || current == *previous {
		return nil
	}
	name := prefix + "-" + d.deps.now().Format("20060102-150405.000000000") + ".txt"
	path := filepath.Join(d.directory, "rascunhos", name)
	// O_EXCL reserves a unique name even if a fast sequence shares one clock
	// tick. Never truncate an earlier draft with a repeated timestamp.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("não foi possível criar a pasta de rascunhos: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(path), strings.TrimSuffix(name, ".txt")+"-*.txt")
	if err != nil {
		return fmt.Errorf("não foi possível preservar o rascunho: %w", err)
	}
	path = file.Name()
	if err := file.Close(); err != nil {
		return fmt.Errorf("não foi possível preparar o rascunho: %w", errors.Join(err, os.Remove(path)))
	}
	if err := store.Write(path, []byte(current+"\n")); err != nil {
		return fmt.Errorf("não foi possível preservar o rascunho: %w", errors.Join(err, os.Remove(path)))
	}
	*previous = current
	return nil
}

func (d *desktop) loadPrevious() {
	var result transcript.Result
	if err := store.ReadJSON(d.latestPath(), &result); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			d.showError("Não foi possível ler a transcrição anterior. O arquivo foi preservado.", "")
		}
		return
	}
	if d.showResult(result, false) && strings.TrimSpace(result.Text) != "" {
		setLabel(d.transcriptName, "Transcrição anterior", textColor)
	}
}

func (d *desktop) latestPath() string { return filepath.Join(d.directory, "last-transcript.json") }

func (d *desktop) removeLatest() error {
	if info, err := os.Lstat(d.latestPath()); err == nil && info.IsDir() {
		return errors.New("não foi possível limpar a última transcrição: o caminho é uma pasta")
	}
	err := os.Remove(d.latestPath())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("não foi possível limpar a última transcrição: %w", err)
	}
	return nil
}

func detectedSpeech(result transcript.Result) bool {
	if result.NeedsReview {
		return true
	}
	for _, part := range result.Parts {
		if part.DetectedSpeech {
			return true
		}
	}
	return false
}

func shortMessage(message string) string {
	runes := []rune(strings.Join(strings.Fields(message), " "))
	if len(runes) > 240 {
		return string(runes[:239]) + "…"
	}
	return string(runes)
}

func clockText(seconds float64) string {
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 {
		seconds = 0
	}
	total := int64(min(seconds, float64(math.MaxInt32)))
	hours := total / 3600
	minutes := total / 60 % 60
	remainder := total % 60
	if hours > 0 {
		return fmt.Sprintf("%02d:%02d:%02d", hours, minutes, remainder)
	}
	return fmt.Sprintf("%02d:%02d", minutes, remainder)
}

func cloneSettings(settings config.Settings) config.Settings {
	if settings.Conversation.Volume != nil {
		value := *settings.Conversation.Volume
		settings.Conversation.Volume = &value
	}
	if settings.Conversation.Autoplay != nil {
		value := *settings.Conversation.Autoplay
		settings.Conversation.Autoplay = &value
	}
	if settings.Cloud.UseS3 != nil {
		enabled := *settings.Cloud.UseS3
		settings.Cloud.UseS3 = &enabled
	}
	settings.TextProfiles = slices.Clone(settings.TextProfiles)
	settings.APIConnections = slices.Clone(settings.APIConnections)
	settings.Instructions = slices.Clone(settings.Instructions)
	if settings.Microphone != nil {
		device := *settings.Microphone
		settings.Microphone = &device
	}
	if settings.Capture.MicrophoneVolume != nil {
		value := *settings.Capture.MicrophoneVolume
		settings.Capture.MicrophoneVolume = &value
	}
	if settings.Capture.PlaybackVolume != nil {
		value := *settings.Capture.PlaybackVolume
		settings.Capture.PlaybackVolume = &value
	}
	if settings.OutputVolume != nil {
		value := *settings.OutputVolume
		settings.OutputVolume = &value
	}
	return settings
}
