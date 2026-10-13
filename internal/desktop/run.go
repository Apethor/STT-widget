package desktop

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"ai-talker/internal/api"
	"ai-talker/internal/cloud"
	"ai-talker/internal/config"
	"ai-talker/internal/platform"
	"ai-talker/internal/playback"
	"ai-talker/internal/processing"
	"ai-talker/internal/realtime"
	"ai-talker/internal/recorder"
	"ai-talker/internal/secrets"
	"ai-talker/internal/soundboard"
	"ai-talker/internal/version"
	"ai-talker/internal/video"
	"ai-talker/internal/whisper"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/dialog"
)

// Run owns the desktop window and its goroutines until GUI exit. Call it from
// main's goroutine. A second launch for the same data directory activates the
// existing window and returns success without creating another application.
func Run(
	ctx context.Context,
	settings config.Settings,
	directory string,
	service Runner,
	initialError string,
) (err error) {
	if ctx == nil {
		return errors.New("informe o contexto de execução do aplicativo")
	}
	if directory == "" {
		return errors.New("informe a pasta de dados do aplicativo")
	}
	directory, err = filepath.Abs(directory)
	if err != nil {
		return fmt.Errorf("não foi possível localizar a pasta de dados: %w", err)
	}
	instance, err := acquireInstance(directory)
	if err != nil || instance == nil {
		return err
	}
	defer func() { err = errors.Join(err, instance.close()) }()
	if service == nil {
		return errors.New("informe o serviço de transcrição")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	app.SetMetadata(fyne.AppMetadata{
		ID:         "dev.ai-talker",
		Name:       "STT-widget",
		Version:    version.Version,
		Migrations: map[string]bool{"fyneDo": true},
	})
	application := app.NewWithID("dev.ai-talker")
	d := newDesktop(ctx, application, settings, directory, service, nativeDependencies())
	d.instance = instance
	application.Lifecycle().SetOnStarted(d.guard(func() { d.start(initialError) }))
	d.window.Show()
	application.Run()
	d.stopped.Store(true)
	d.cancel()
	d.workers.Wait()
	return nil
}

func nativeDependencies() dependencies {
	return dependencies{
		dispatch: fyne.Do,
		newRecorder: func(device int, directory string) recording {
			return recorder.NewShared(device, "", directory)
		},
		newConfiguredRecorder: func(settings config.Settings, directory string) recording {
			switch settings.Capture.SourceMode() {
			case config.CaptureTeam:
				return recorder.NewTeam(settings.Device(), settings.Capture.MicrophoneEndpoint, settings.Capture.PlaybackEndpoint, directory)
			case config.CaptureSystem:
				return recorder.NewSystem(settings.Capture.PlaybackEndpoint, directory)
			default:
				return recorder.NewShared(settings.Device(), settings.Capture.MicrophoneEndpoint, directory)
			}
		},
		newPlayer: func(directory string) audioPlayer { return playback.New(directory) },
		newConversation: func(ctx context.Context, settings config.Settings, options realtime.Options, callback func(realtime.Event)) (voiceConnection, error) {
			return cloud.NewConversation(ctx, settings, options, callback)
		},
		newSoundEngine: soundboard.NewEngine,
		verify:         cloud.Verify,
		verifyBucket:   cloud.TestBucket,
		process: func(ctx context.Context, settings config.Settings, request processing.Request) (processing.Result, error) {
			if request.Options.Provider == config.ProviderAPI {
				return api.Process(ctx, request, secrets.Native{})
			}
			return cloud.Process(ctx, settings, request)
		},
		vault: secrets.Native{},
		downloadWhisperEngine: func(ctx context.Context, directory string, progress func(whisper.DownloadProgress)) (string, error) {
			return whisper.NewDownloader(directory).InstallEngine(ctx, progress)
		},
		downloadWhisperModel: func(ctx context.Context, directory, model, source string, progress func(whisper.DownloadProgress)) (string, error) {
			return whisper.NewDownloader(directory).InstallModelFrom(ctx, model, source, progress)
		},
		testWhisper:       whisper.Check,
		copy:              platform.Copy,
		openFolder:        platform.OpenFolder,
		openURL:           platform.OpenURL,
		openImage:         platform.OpenImage,
		chooseFile:        platform.ChooseFile,
		setPinned:         platform.SetPinned,
		activate:          platform.Activate,
		minimize:          platform.Minimize,
		selectScreenshot:  platform.SelectScreenshot,
		selectVideoRegion: platform.SelectScreenRegion,
		newVideoRecorder: func(directory, audioPath string, region platform.ScreenRegion) (screenVideoCapture, error) {
			return video.NewRecorder(directory, audioPath, region)
		},
		openFile:        platform.OpenFile,
		ownWindow:       platform.OwnWindow,
		darkTitlebar:    platform.DarkTitlebar,
		windowFrame:     platform.IntegratedTitlebar,
		microphones:     recorder.SharedMicrophones,
		playbackDevices: recorder.PlaybackDevices,
		profiles:        availableProfiles,
		now:             time.Now,
		tickInterval:    100 * time.Millisecond,
		confirmClose: func(window fyne.Window, callback func(bool)) {
			confirmation := dialog.NewConfirm(
				"Gravação em andamento",
				"Salvar a gravação e fechar? Você poderá transcrever o arquivo depois.",
				callback,
				window,
			)
			confirmation.SetConfirmText("Salvar e fechar")
			confirmation.SetDismissText("Continuar gravando")
			confirmation.Show()
		},
	}
}
