package desktop

import (
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"ai-talker/internal/recorder"
	"ai-talker/internal/store"
)

var recordingDateName = regexp.MustCompile(`^(?:Gravacao-)?(\d{8}-\d{6})(?:-(?:mic|sis|\d+))*\.wav$`)

func recordingLabel(path string) string {
	name := filepath.Base(path)
	if match := recordingDateName.FindStringSubmatch(name); len(match) == 2 {
		if started, err := time.ParseInLocation("20060102-150405", match[1], time.Local); err == nil {
			return started.Format("15:04 · 02/01")
		}
	}
	return name
}

func (d *desktop) recordingPaths() []string {
	source := d.lastAudio
	var tracks []recorder.Track
	if d.capture != captureIdle && d.captureOrigin != nil {
		source = d.captureOrigin.recordingSource
		if d.recorder != nil {
			tracks = recordingTracks(d.recorder, d.captureOrigin.settings.Capture.SourceMode())
		}
	} else if d.live != nil && sameAudio(source, d.live.Source) {
		tracks = d.live.Tracks
	}
	var paths []string
	for _, track := range tracks {
		if track.Snapshot.Path != "" {
			if absolute, err := filepath.Abs(track.Snapshot.Path); err == nil {
				paths = append(paths, absolute)
			}
		}
	}
	if len(paths) == 0 && source != "" {
		if absolute, err := filepath.Abs(source); err == nil {
			paths = append(paths, absolute)
		}
	}
	return paths
}

func (d *desktop) syncRecordingName() {
	paths := d.recordingPaths()
	name := uiText("Novo áudio")
	d.copyAudioPath.Hide()
	videoPath := ""
	if d.recordedAppend != nil && sameAudio(d.recordedAppend.Source, d.lastAudio) {
		videoPath = d.recordedAppend.VideoPath
	}
	if len(paths) > 0 {
		name = recordingLabel(paths[0])
		d.copyAudioPath.Show()
	}
	if videoPath != "" {
		d.openVideoButton.Show()
	} else {
		d.openVideoButton.Hide()
	}
	setLabel(d.caption, name, mutedColor)
	d.caption.hint.text = strings.Join(paths, "\n")
}

func (d *desktop) copyRecordingPath() {
	paths := d.recordingPaths()
	if len(paths) == 0 {
		return
	}
	source := d.lastAudio
	recording := d.capture != captureIdle
	d.operation(func() error {
		// Imported audio can be on a network drive. Manifest lookup belongs
		// to this explicit action, never the UI layout or progress callbacks.
		if !recording && len(paths) == 1 {
			var manifest recorder.TeamManifest
			if store.ReadJSON(source+".team.json", &manifest) == nil && manifest.Version == 1 && len(manifest.Tracks) == 2 {
				var team []string
				for _, track := range manifest.Tracks {
					if track.Snapshot.Path != "" {
						if absolute, err := filepath.Abs(track.Snapshot.Path); err == nil {
							team = append(team, absolute)
						}
					}
				}
				if len(team) > 0 {
					paths = team
				}
			}
		}
		return d.deps.copy(strings.Join(paths, "\r\n"))
	}, func(err error) {
		if err != nil {
			d.showError("Não foi possível copiar o caminho: "+err.Error(), "")
		}
	})
}
