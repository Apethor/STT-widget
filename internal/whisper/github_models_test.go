package whisper

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ai-talker/internal/config"
)

func syntheticModel(payload []byte) Model {
	digest := sha1.Sum(payload)
	return Model{
		ID: "base-q5_1", Name: "Base Q5", MaxBytes: 1 << 20,
		SHA1: hex.EncodeToString(digest[:]), SHA256: payloadDigest(payload),
		GitHub: "https://github.com/example/models/releases/download/test/base.bin",
	}
}

func TestWhisperModelPrefersVerifiedGitHubAndReusesTheInstalledFile(t *testing.T) {
	t.Parallel()
	payload := []byte("synthetic multilingual Q5 model")
	model := syntheticModel(payload)
	var destinations []string
	d := NewDownloader(t.TempDir())
	d.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		destinations = append(destinations, request.URL.Host)
		return &http.Response{
			StatusCode: http.StatusOK, ContentLength: int64(len(payload)), Request: request,
			Body: io.NopCloser(bytes.NewReader(payload)), Header: http.Header{},
		}, nil
	})}
	path, err := d.installModel(t.Context(), model, nil)
	if err != nil {
		t.Fatal(err)
	}
	reused, err := d.installModel(t.Context(), model, nil)
	saved, readErr := os.ReadFile(path)
	if err != nil || readErr != nil || reused != path || !bytes.Equal(saved, payload) ||
		!slices.Equal(destinations, []string{"github.com"}) {
		t.Fatalf("GitHub model did not install/reuse without Hugging Face: %v / %v", destinations, err)
	}
}

func TestWhisperExplicitSourceChangeReusesIdenticalVerifiedWeights(t *testing.T) {
	t.Parallel()
	payload := []byte("synthetic multilingual Q5 model")
	model := syntheticModel(payload)
	calls := 0
	d := NewDownloader(t.TempDir())
	d.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.URL.Host != "github.com" || calls != 1 {
			t.Fatal("pesos já verificados foram baixados novamente ao mudar a fonte")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(payload)),
			ContentLength: int64(len(payload)), Request: request, Header: http.Header{}}, nil
	})}
	path, err := d.installModelFrom(t.Context(), model, SourceGitHub, nil)
	if err != nil {
		t.Fatal(err)
	}
	reused, err := d.installModelFrom(t.Context(), model, SourceHuggingFace, nil)
	if err != nil || reused != path || calls != 1 {
		t.Fatal("a mudança de fonte descartou pesos idênticos ou sua validação", err)
	}
}

func TestWhisperUnavailableSourceModelCombinationDoesNotContactAnotherHost(t *testing.T) {
	t.Parallel()
	d := NewDownloader(t.TempDir())
	d.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("uma combinação indisponível iniciou download")
		return nil, nil
	})}
	for _, pair := range []struct{ id, source string }{
		{"medium", SourceGitHub}, {"large-v3", SourceGitHub}, {"large-v3-q8_0", SourceHuggingFace},
	} {
		model, ok := ModelByID(pair.id)
		if !ok {
			t.Fatal("modelo fora do catálogo")
		}
		if _, err := d.installModelFrom(t.Context(), model, pair.source, nil); err == nil ||
			!strings.Contains(model.DownloadDescriptionFrom(pair.source), "Escolha a outra fonte") {
			t.Fatal("a combinação indisponível não explicou a alternativa explícita", pair, err)
		}
	}
}

func TestWhisperModelKeepsSelectedSourceAfterFailureAndAllowsExplicitOtherSource(t *testing.T) {
	t.Parallel()
	payload := []byte("synthetic multilingual Q5 model")
	for _, blocked := range []bool{false, true} {
		t.Run(map[bool]string{true: "blocked", false: "checksum mismatch"}[blocked], func(t *testing.T) {
			t.Parallel()
			var destinations []string
			d := NewDownloader(t.TempDir())
			d.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				destinations = append(destinations, request.URL.Host)
				status, body := http.StatusOK, payload
				if request.URL.Host == "github.com" {
					body = []byte("wrong weights")
					if blocked {
						status = http.StatusForbidden
					}
				}
				return &http.Response{
					StatusCode: status, ContentLength: int64(len(body)), Request: request,
					Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{},
				}, nil
			})}
			path, err := d.installModel(t.Context(), syntheticModel(payload), nil)
			if err == nil || path != "" || !slices.Equal(destinations, []string{"github.com"}) {
				t.Fatal("a falha mudou automaticamente a fonte selecionada", destinations, err)
			}
			path, err = d.installModelFrom(t.Context(), syntheticModel(payload), SourceHuggingFace, nil)
			if err != nil {
				t.Fatal(err)
			}
			saved, readErr := os.ReadFile(path)
			if readErr != nil || !bytes.Equal(saved, payload) ||
				!slices.Equal(destinations, []string{"github.com", "huggingface.co"}) {
				t.Fatal("a troca explícita não verificou e instalou somente a fonte escolhida")
			}
		})
	}
}

func TestWhisperModelCancellationDoesNotContinueWithAnotherSource(t *testing.T) {
	t.Parallel()
	payload := []byte("synthetic model")
	d := NewDownloader(t.TempDir())
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := 0
	d.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		cancel()
		return nil, ctx.Err()
	})}
	if _, err := d.installModel(ctx, syntheticModel(payload), nil); !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("canceling a GitHub download started a Hugging Face request: calls=%d / %v", calls, err)
	}
	if _, err := os.Stat(ModelPath(d.directory, "base-q5_1")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("canceled weights were installed")
	}
}

func TestWhisperGitHubCatalogIncludesSmallerMultilingualModelsAndUserQ8(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"tiny-q5_1", "base-q5_1", "tiny", "base", "small-q5_1", "large-v3-q8_0"} {
		model, exists := ModelByID(id)
		settings := config.DefaultWhisper()
		settings.Model = id
		if !exists || !strings.HasPrefix(model.GitHub, "https://github.com/") ||
			len(model.SHA256) != 64 || strings.Contains(model.ID, ".en") ||
			settings.Validate() != nil || !strings.Contains(model.DownloadDescription(), "GitHub") {
			t.Fatalf("model %s lacks a verified multilingual GitHub download", id)
		}
	}
}

func TestWhisperAppUpdateKeepsThePreviousManagedEngineUntilNewInstallation(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	previous := filepath.Join(directory, "whisper", "engine-1.9.2")
	if err := os.MkdirAll(previous, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range engineFiles {
		if err := os.WriteFile(filepath.Join(previous, name), []byte("existing engine"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	binary, _ := Paths(directory, config.DefaultWhisper())
	if binary != filepath.Join(previous, "whisper-cli.exe") {
		t.Fatal("updating the application invalidated an installed local engine")
	}
	options := config.DefaultWhisper()
	options.BinaryPath = filepath.Join(directory, "my-custom-engine.exe")
	if binary, _ := Paths(directory, options); binary != options.BinaryPath {
		t.Fatal("managed update changed an explicitly selected engine")
	}
}
