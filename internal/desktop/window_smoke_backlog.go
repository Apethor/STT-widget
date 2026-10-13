//go:build windows && windowsmoke

package desktop

import (
	"context"
	"sync/atomic"

	"ai-talker/internal/cloud"
	"ai-talker/internal/config"
	"ai-talker/internal/recorder"
	"ai-talker/internal/version"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

type smokeNamedControl struct {
	Text     string
	Rect     smokeRectangle
	MinWidth float32
}

type smokeTab struct {
	ID, Group uint64
	Name      string
	Rect      smokeRectangle
	Close     smokeRectangle
}

type smokeGroup struct {
	ID    uint64
	Name  string
	Label smokeRectangle
	Row   smokeRectangle
	More  smokeRectangle
}

type smokeEditorZoom struct {
	Button, Slider, Thumb smokeRectangle
	Value                 int
	Open                  bool
}

type backlogSmokeSettings struct {
	Page                                                   string
	SearchQuery                                            string
	Tree, Search, SearchList                               smokeRectangle
	TreeLabels                                             []smokeNamedControl
	Results                                                []string
	Model, Reasoning, MergeModel, MergeReasoning           smokeRectangle
	CloudModel                                             smokeRectangle
	CloudChoices                                           []string
	APIProvider, APIEndpoint, APIName, APICatalog          smokeRectangle
	APIChoices                                             []string
	CleanupCategory, CleanupPeriod, CleanupAll             smokeRectangle
	CleanupSummary, About                                  string
	CopyIdentity                                           smokeRectangle
	DialogSize                                             fyne.Size
	AWSProfile, AWSRegion, BucketName, BucketResult        string
	BucketUse, BucketField, BucketTest                     smokeRectangle
	BucketEnabled, BucketFieldDisabled, BucketTestDisabled bool
	DeviceRefresh, Microphone, Playback, VideoPreset       smokeRectangle
	MicrophoneChoices, PlaybackChoices, VideoChoices       []string
	SelectedVideo                                          string
	Loading                                                bool
}

type backlogSmokeState struct {
	Version, Revision, Layout, Error                         string
	Selected                                                 uint64
	CreateGroup                                              smokeRectangle
	UndoClose, Join                                          smokeRectangle
	UndoVisible                                              bool
	UndoCount                                                int
	UndoDeadlineUTC                                          string
	Groups                                                   []smokeGroup
	Tabs                                                     []smokeTab
	TranscriptZoom, ProcessZoom                              smokeEditorZoom
	SpeedButton, EvaluationButton, Soundboard                smokeRectangle
	SpeedVisible, EvaluationVisible                          bool
	Speed, PlayerSpeed                                       float64
	EvaluationScore                                          *int
	ModalButtons                                             []smokeNamedControl
	Settings                                                 *backlogSmokeSettings
	Video                                                    smokeRectangle
	VideoArmed                                               bool
	SoundboardOpen                                           bool
	SoundboardButtons                                        []smokeNamedControl
	SoundboardVolume                                         int
	SoundboardSlider                                         smokeRectangle
	SoundPlaying                                             bool
	WindowTitle                                              string
	HeaderTexts                                              []string
	RecorderCaption, RecorderMessage, RecorderSize           smokeRectangle
	RecorderCollapse                                         smokeRectangle
	CaptureDeviceRefresh, CaptureMicrophone, CapturePlayback smokeRectangle
	DeviceEnumerationCount                                   int32
	TranscriptCopy, ProcessCopy                              smokeRectangle
	TranscriptSource, TranscriptEditor                       smokeRectangle
}

type backlogSmokeCommand struct {
	Layout          string `json:"layout"`
	SettingsPage    string `json:"settings_page"`
	CloseZoom       bool   `json:"close_zoom"`
	DraftAWSProfile string `json:"draft_aws_profile"`
	DraftAWSRegion  string `json:"draft_aws_region"`
	DraftBucket     string `json:"draft_bucket"`
}

var smokeDeviceEnumerationCount atomic.Int32

// Only the explicitly built native smoke executable uses these contracts.
// The bucket control is exercised without creating AWS requests.
func configureRelease011Smoke(deps *dependencies) {
	deps.verifyBucket = func(_ context.Context, settings config.Settings) (cloud.BucketCheck, error) {
		return cloud.BucketCheck{
			Bucket: settings.Cloud.S3Bucket, Region: settings.Region,
			Account: "000000000000", Encryption: "synthetic", Cleaned: true,
		}, nil
	}
	microphones, outputs := deps.microphones, deps.playbackDevices
	deps.microphones = func() ([]recorder.Device, error) {
		smokeDeviceEnumerationCount.Add(1)
		return microphones()
	}
	deps.playbackDevices = func() ([]recorder.Device, error) {
		smokeDeviceEnumerationCount.Add(1)
		return outputs()
	}
}

func (d *desktop) windowBacklogState() *backlogSmokeState {
	state := &backlogSmokeState{
		Version: version.Version, Revision: version.Revision, Layout: d.settings.Appearance.Layout,
		Selected: d.document.id, CreateGroup: d.smokeRectangle(d.tabs.renderer.createGroup),
		UndoClose: d.smokeRectangle(d.undoCloseButton), UndoVisible: d.undoCloseButton.Visible(),
		UndoCount: len(d.closedTabs), UndoDeadlineUTC: d.windowSmokeUndoDeadline(),
		Join:        d.smokeRectangle(d.joinButton),
		SpeedButton: d.smokeRectangle(d.playbackSpeedButton), SpeedVisible: d.playbackSpeedButton.Visible(),
		Speed:             d.playbackSpeed(d.lastAudio),
		EvaluationButton:  d.smokeRectangle(d.document.evaluationButton),
		EvaluationVisible: d.document.evaluationButton.Visible(),
		Soundboard:        d.smokeRectangle(d.soundboardButton), Video: d.smokeRectangle(d.videoButton),
		VideoArmed:      d.videoArmed,
		WindowTitle:     d.window.Title(),
		RecorderCaption: d.smokeRectangle(d.caption), RecorderMessage: d.smokeRectangle(d.recordState),
		RecorderSize:           d.smokeRectangle(d.sizeButton),
		DeviceEnumerationCount: smokeDeviceEnumerationCount.Load(),
		TranscriptCopy:         d.smokeRectangle(d.document.copyButton),
		ProcessCopy:            d.smokeRectangle(d.document.processCopyButton),
		TranscriptSource:       d.smokeRectangle(d.document.sourceLabel),
		TranscriptEditor:       d.smokeRectangle(d.document.editor),
	}
	for _, object := range smokeCanvasObjects(d.header) {
		if text, ok := object.(*themedText); ok {
			state.HeaderTexts = append(state.HeaderTexts, text.Text)
		}
	}
	for _, object := range smokeCanvasObjects(d.recordPanel) {
		if button, ok := object.(*actionButton); ok && button.hint.text == "Recolher Gravação" {
			state.RecorderCollapse = d.smokeRectangle(button)
		}
	}
	if form := d.deviceForm; form != nil && form.popup.Visible() {
		state.CaptureDeviceRefresh = d.smokeRectangle(form.refresh)
		state.CaptureMicrophone = d.smokeRectangle(form.microphone)
		state.CapturePlayback = d.smokeRectangle(form.playback)
	}
	if d.errorLabel.Visible() {
		state.Error = d.errorLabel.Text
	}
	state.TranscriptZoom = d.smokeEditorZoom(d.document, true)
	state.ProcessZoom = d.smokeEditorZoom(d.document, false)
	for _, row := range d.tabs.renderer.rows {
		state.Groups = append(state.Groups, smokeGroup{
			ID: row.id, Name: d.groupName(row.id),
			Label: d.smokeRectangle(row.label), Row: d.smokeRectangle(row.box),
			More: d.smokeRectangle(row.more),
		})
	}
	for _, header := range d.tabs.renderer.headers {
		for _, doc := range d.documents {
			if doc.item == header.item {
				state.Tabs = append(state.Tabs, smokeTab{
					ID: doc.id, Group: doc.group, Name: header.item.Text, Rect: d.smokeRectangle(header.title),
					Close: d.smokeRectangle(header.close),
				})
			}
		}
	}
	if doc := d.document; doc.evaluation != nil {
		score := doc.evaluation.Score
		state.EvaluationScore = &score
	}
	if d.playback != nil {
		if player, ok := d.playback.player.(*windowSmokePlayer); ok {
			state.PlayerSpeed = player.playbackSpeed()
		}
	}
	for _, overlay := range d.window.Canvas().Overlays().List() {
		state.ModalButtons = append(state.ModalButtons, d.smokeNamedButtons(overlay)...)
	}
	if form := d.settingsForm; form != nil {
		settings := &backlogSmokeSettings{
			Page: form.sections.selectedID, Tree: d.smokeRectangle(form.sections.tree),
			SearchQuery: form.sections.search.Text,
			Search:      d.smokeRectangle(form.sections.search), SearchList: d.smokeRectangle(form.sections.searchList),
			Model: d.smokeRectangle(form.llm.model), Reasoning: d.smokeRectangle(form.llm.reasoning),
			MergeModel: d.smokeRectangle(form.mergeModel), MergeReasoning: d.smokeRectangle(form.mergeReasoning),
			CloudModel: d.smokeRectangle(form.quality), CloudChoices: form.quality.Options,
			CleanupCategory: d.smokeRectangle(form.cleanup.category),
			CleanupPeriod:   d.smokeRectangle(form.cleanup.period), CleanupAll: d.smokeRectangle(form.cleanup.all),
			CleanupSummary: form.cleanup.summary.Text,
			DialogSize:     form.dialogSize, AWSProfile: form.profile.Text, AWSRegion: form.region.Text,
			BucketName: form.cloudBucket.Text, BucketResult: form.cloudBucketResult.Text,
			BucketUse: d.smokeRectangle(form.cloudUseBucket), BucketField: d.smokeRectangle(form.cloudBucket),
			BucketTest: d.smokeRectangle(form.cloudTestBucket), BucketEnabled: form.cloudUseBucket.Checked,
			BucketFieldDisabled: form.cloudBucket.Disabled(), BucketTestDisabled: form.cloudTestBucket.Disabled(),
			DeviceRefresh: d.smokeRectangle(form.deviceRefresh), Microphone: d.smokeRectangle(form.microphone),
			Playback: d.smokeRectangle(form.playback), MicrophoneChoices: form.microphone.Options,
			PlaybackChoices: form.playback.Options, VideoPreset: d.smokeRectangle(form.videoPreset),
			VideoChoices: form.videoPreset.Options, SelectedVideo: form.videoPreset.Selected,
			Loading: form.loading || form.devicesLoading,
		}
		for _, object := range smokeCanvasObjects(form.sections.tree) {
			if label, ok := object.(*widget.Label); ok && label.Text != "" {
				settings.TreeLabels = append(settings.TreeLabels, smokeNamedControl{
					Text: label.Text, Rect: d.smokeRectangle(label), MinWidth: label.MinSize().Width,
				})
			}
		}
		for _, index := range form.sections.searchResult {
			settings.Results = append(settings.Results, form.sections.searchItems[index].title)
		}
		if manager := form.audioAPI.manager; manager != nil {
			settings.APIProvider = d.smokeRectangle(manager.provider)
			settings.APIEndpoint = d.smokeRectangle(manager.endpoint)
			settings.APIName = d.smokeRectangle(manager.name)
			settings.APICatalog = d.smokeRectangle(manager.managerSelect)
			settings.APIChoices = manager.provider.Options
		}
		if settings.Page == "about" {
			settings.About = applicationIdentification()
			for _, control := range d.smokeNamedButtons(form.sections.pages["about"]) {
				if control.Text == "Copiar identificação" {
					settings.CopyIdentity = control.Rect
				}
			}
		}
		state.Settings = settings
	}
	if d.soundboardWindow != nil {
		state.SoundboardOpen = true
		state.SoundboardButtons = d.smokeNamedButtons(d.soundboardWindow.Content())
		state.SoundboardSlider = d.smokeRectangle(d.soundboardVolume)
		state.SoundboardVolume = d.soundboard.Volume()
		state.SoundPlaying = d.soundPlayback != nil
	}
	return state
}

func (d *desktop) smokeEditorZoom(doc *document, transcript bool) smokeEditorZoom {
	button, form := doc.processZoomButton, doc.processZoomForm
	if transcript {
		button, form = doc.transcriptZoomButton, doc.transcriptZoomForm
	}
	state := smokeEditorZoom{Button: d.smokeRectangle(button), Value: d.editorZoomValue(doc, transcript)}
	if form != nil && form.popup.Visible() {
		state.Open = true
		state.Slider = d.smokeRectangle(form.slider)
		state.Thumb = d.smokeRectangle(test.WidgetRenderer(form.slider).Objects()[2])
	}
	return state
}

func (d *desktop) smokeNamedButtons(object fyne.CanvasObject) []smokeNamedControl {
	var controls []smokeNamedControl
	for _, child := range smokeCanvasObjects(object) {
		if button, ok := child.(*widget.Button); ok && button.Text != "" && !button.Disabled() {
			controls = append(controls, smokeNamedControl{Text: button.Text, Rect: d.smokeRectangle(button)})
		}
	}
	return controls
}

// Inspect the renderer already used by the native canvas. Creating another
// renderer would return detached tree rows and could change live geometry.
func smokeCanvasObjects(object fyne.CanvasObject) []fyne.CanvasObject {
	if object == nil {
		return nil
	}
	objects := []fyne.CanvasObject{object}
	var children []fyne.CanvasObject
	switch current := object.(type) {
	case *fyne.Container:
		children = current.Objects
	case fyne.Widget:
		children = test.WidgetRenderer(current).Objects()
	}
	for _, child := range children {
		objects = append(objects, smokeCanvasObjects(child)...)
	}
	return objects
}

func (d *desktop) applyBacklogSmokeCommand(command backlogSmokeCommand) {
	switch command.Layout {
	case config.LayoutColumn, config.LayoutInline, config.LayoutStacked:
		updated := cloneSettings(d.settings)
		updated.Appearance.Layout = command.Layout
		d.persistPreferences(updated, func(err error) {
			if err != nil {
				d.showError(err.Error(), "")
			}
		})
	}
	if command.SettingsPage != "" && d.settingsForm != nil {
		d.settingsForm.sections.selectID(command.SettingsPage)
	}
	if form := d.settingsForm; form != nil {
		if command.DraftAWSProfile != "" {
			form.profile.SetText(command.DraftAWSProfile)
		}
		if command.DraftAWSRegion != "" {
			form.region.SetText(command.DraftAWSRegion)
		}
		if command.DraftBucket != "" {
			form.cloudBucket.SetText(command.DraftBucket)
		}
	}
	if command.CloseZoom {
		for _, doc := range d.documents {
			for _, form := range []*editorZoomForm{doc.transcriptZoomForm, doc.processZoomForm} {
				if form != nil {
					form.popup.Hide()
				}
			}
		}
	}
}
