//go:build whisperintegration && windows

package whisper

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"ai-talker/internal/config"
	"ai-talker/internal/platform"
)

// This test uses a previously downloaded official release archive. It does
// not download weights or use private audio, and proves native CPU startup.
func TestWhisperOfficialEngineInstallationAndRuntime(t *testing.T) {
	path := os.Getenv("AI_TALKER_TEST_WHISPER_ARCHIVE")
	if path == "" {
		t.Skip("informe AI_TALKER_TEST_WHISPER_ARCHIVE com o ZIP oficial " + EngineVersion)
	}
	archive, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	d := NewDownloader(t.TempDir())
	d.client = syntheticDownload(archive, http.StatusOK, "application/zip", int64(len(archive)))
	binary, err := d.InstallEngine(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "--help")
	platform.Hide(cmd)
	output, err := cmd.CombinedOutput()
	if err != nil || !bytes.Contains(output, []byte("--model")) || !bytes.Contains(output, []byte("--language")) {
		t.Fatalf("o motor oficial não iniciou: %v, %s", err, output)
	}
	t.Logf("whisper.cpp %s: ZIP verificado, bibliotecas instaladas e CLI CPU iniciado", EngineVersion)
}

func TestWhisperGitHubQ5ModelsRunNativeSyntheticInference(t *testing.T) {
	archivePath, modelsDirectory := os.Getenv("AI_TALKER_TEST_WHISPER_ARCHIVE"), os.Getenv("AI_TALKER_TEST_WHISPER_MODELS")
	if archivePath == "" || modelsDirectory == "" {
		t.Skip("informe o ZIP oficial e a pasta de modelos sintéticos previamente baixados")
	}
	archive, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	d := NewDownloader(directory)
	d.client = syntheticDownload(archive, http.StatusOK, "application/zip", int64(len(archive)))
	if _, err := d.InstallEngine(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"tiny-q5_1", "base-q5_1", "small-q5_1"} {
		t.Run(id, func(t *testing.T) {
			model, _ := ModelByID(id)
			modelPath := filepath.Join(modelsDirectory, "ggml-"+id+".bin")
			valid, err := verifyFile(t.Context(), modelPath, model.SHA256, false)
			if err != nil || !valid {
				t.Fatalf("GitHub weights checksum did not match: %v", err)
			}
			settings := config.Default()
			settings.Provider, settings.Glossary = config.ProviderWhisper, ""
			settings.Whisper.Model, settings.Whisper.ModelPath = id, modelPath
			result, err := Check(t.Context(), directory, settings)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%s: voz sintética reconhecida em %.2fs: %s", model.Name, result.ElapsedSeconds, result.Text)
		})
	}
}
