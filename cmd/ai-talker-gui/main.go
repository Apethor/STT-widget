package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"

	"ai-talker/internal/api"
	"ai-talker/internal/audio"
	"ai-talker/internal/cloud"
	"ai-talker/internal/config"
	"ai-talker/internal/desktop"
	"ai-talker/internal/platform"
	"ai-talker/internal/secrets"
	"ai-talker/internal/transcript"
	"ai-talker/internal/whisper"
)

func main() {
	if err := run(); err != nil {
		slog.Error("falha ao abrir o STT-widget", "error", err)
		if noticeErr := platform.ShowNotice("STT-widget", err.Error()); noticeErr != nil {
			slog.Error("falha ao mostrar o aviso de abertura", "error", noticeErr)
		}
		os.Exit(1)
	}
}

func run() (runErr error) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	directory, err := config.DataDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	// The GUI binary has no console; retain technical startup failures locally.
	logFile, err := os.OpenFile(filepath.Join(directory, "ai-talker-go.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err == nil {
		defer func() {
			if runErr != nil {
				slog.Error("falha ao abrir o STT-widget", "error", runErr)
			}
			_ = logFile.Close()
		}()
		slog.SetDefault(slog.New(slog.NewTextHandler(logFile, nil)))
	}
	settings, loadErr := config.Load(directory)
	initialError := ""
	if loadErr != nil {
		settings = config.Default()
		settings.Profile = config.DefaultProfile()
		initialError = loadErr.Error()
	}
	profile := flag.String("profile", "", "Perfil AWS.")
	region := flag.String("region", "", "Região AWS.")
	quality := flag.String("quality", "", "quality ou fast.")
	provider := flag.String("provider", "", "bedrock (AWS Cloud) ou whisper.")
	model := flag.String("model", "", "Modelo Cloud para gravação completa.")
	streamModel := flag.String("stream-model", "", "Modelo Cloud para Stream.")
	language := flag.String("transcribe-language", "", "Idioma do Amazon Transcribe.")
	bucket := flag.String("transcribe-bucket", "", "Bucket S3 do Amazon Transcribe.")
	flag.Parse()
	explicit := make(map[string]bool)
	flag.Visit(func(option *flag.Flag) { explicit[option.Name] = true })
	if *profile != "" {
		settings.Profile = *profile
	}
	if *region != "" {
		settings.Region = *region
	}
	if *quality != "" {
		settings.Quality = *quality
		settings.Cloud.ModelID = ""
	}
	if explicit["model"] {
		settings.Cloud.ModelID = *model
		settings.Provider = config.ProviderBedrock
	}
	if explicit["provider"] {
		settings.Provider = *provider
	}
	if explicit["stream-model"] {
		settings.Cloud.StreamModelID = *streamModel
	}
	if explicit["transcribe-language"] {
		settings.Cloud.Language = *language
	}
	if explicit["transcribe-bucket"] {
		settings.Cloud.S3Bucket = *bucket
	}
	if err := settings.Validate(); err != nil {
		return err
	}
	preparer, prepareErr := audio.New()
	if prepareErr != nil {
		if initialError != "" {
			initialError += "\n"
		}
		initialError += prepareErr.Error()
		return desktop.Run(ctx, settings, directory, failedRunner{cause: prepareErr}, initialError)
	}
	service, err := transcript.New(ctx, transcript.Options{
		DataDir: directory, Audio: preparer, CloudFactory: cloud.New,
		LocalFactory: whisper.Factory(directory), LocalFingerprint: whisper.Fingerprint(directory),
		APIFactory:  api.Factory(secrets.Native{}),
		MaxRequests: settings.Parallelism(),
	})
	if err != nil {
		return err
	}
	defer service.Close()
	return desktop.Run(ctx, settings, directory, service, initialError)
}

type failedRunner struct{ cause error }

func (r failedRunner) Transcribe(
	context.Context,
	string,
	config.Settings,
	transcript.Request,
) (transcript.Result, error) {
	return transcript.Result{}, errors.Join(errors.New("as ferramentas de áudio não estão disponíveis"), r.cause)
}
