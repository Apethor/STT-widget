package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"

	"ai-talker/internal/api"
	"ai-talker/internal/audio"
	"ai-talker/internal/cli"
	"ai-talker/internal/cloud"
	"ai-talker/internal/config"
	"ai-talker/internal/platform"
	"ai-talker/internal/secrets"
	"ai-talker/internal/transcript"
	"ai-talker/internal/version"
	"ai-talker/internal/whisper"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	directory, err := config.DataDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	var runner cli.Runner
	preparer, prepareErr := audio.New()
	if prepareErr != nil {
		runner = failedRunner{cause: prepareErr}
	} else {
		service, err := transcript.New(ctx, transcript.Options{
			DataDir: directory, Audio: preparer, CloudFactory: cloud.New,
			LocalFactory: whisper.Factory(directory), LocalFingerprint: whisper.Fingerprint(directory),
			APIFactory: api.Factory(secrets.Native{}),
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		defer service.Close()
		runner = service
	}
	app := cli.App{
		DataDir: directory, Runner: runner, Copy: platform.Copy, Launch: launch,
		Verify: cloud.Verify, Version: version.String(),
	}
	return app.Run(ctx, os.Args[1:])
}

type failedRunner struct{ cause error }

func (r failedRunner) Transcribe(
	context.Context,
	string,
	config.Settings,
	transcript.Request,
) (transcript.Result, error) {
	return transcript.Result{}, r.cause
}

func launch(ctx context.Context, settings config.Settings) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	gui := filepath.Join(filepath.Dir(executable), "STT-widget.exe")
	if _, err := os.Stat(gui); err != nil {
		return errors.New("compile a interface com .\\scripts\\build-go.ps1 e abra bin\\STT-widget.exe")
	}
	arguments := []string{
		"--region", settings.Region, "--quality", settings.Quality, "--provider", settings.TranscriptionProvider(),
		"--model", settings.CloudModelID(), "--stream-model", settings.StreamModelID(),
		"--transcribe-language", settings.Cloud.TranscribeLanguage(),
		"--transcribe-bucket", settings.Cloud.S3Bucket,
	}
	if settings.Profile != "" {
		arguments = append(arguments, "--profile", settings.Profile)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	cmd := exec.Command(gui, arguments...)
	platform.Hide(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	// The GUI owns its lifetime after a successful launch. Releasing the
	// process handle avoids leaving a waiter in the short-lived CLI process.
	return cmd.Process.Release()
}
