package whisper

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func syntheticDownload(payload []byte, status int, mediaType string, length int64) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: status, ContentLength: length, Header: http.Header{"Content-Type": []string{mediaType}},
			Body: io.NopCloser(bytes.NewReader(payload)), Request: request,
		}, nil
	})}
}

func payloadDigest(payload []byte) string {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func TestWhisperDownloadVerifiesChecksumAndPublishesProgress(t *testing.T) {
	payload := []byte("synthetic official artifact")
	directory := t.TempDir()
	downloader := NewDownloader(directory)
	downloader.client = syntheticDownload(payload, 200, "application/octet-stream", int64(len(payload)))
	var progress DownloadProgress
	path, err := downloader.fetch(t.Context(), directory, engineURL, 1024, payloadDigest(payload), false, func(p DownloadProgress) {
		progress = p
	})
	if err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(actual, payload) || progress.Bytes != int64(len(payload)) || progress.Total != int64(len(payload)) {
		t.Fatalf("download não preservou o arquivo verificado ou seu progresso: %v, %+v", err, progress)
	}
}

func TestWhisperDownloadFailuresRemoveOnlyTemporaryFiles(t *testing.T) {
	payload := []byte("artifact")
	for _, tc := range []struct {
		name   string
		status int
		media  string
		length int64
		limit  int64
		digest string
	}{
		{"rede bloqueada", 403, "text/html", 8, 1024, payloadDigest(payload)},
		{"página de bloqueio", 200, "text/html", 8, 1024, payloadDigest(payload)},
		{"servidor indisponível", 503, "text/plain", 8, 1024, payloadDigest(payload)},
		{"checksum incorreto", 200, "application/octet-stream", 8, 1024, strings.Repeat("0", 64)},
		{"tamanho declarado excessivo", 200, "application/octet-stream", 2048, 1024, payloadDigest(payload)},
		{"stream excessivo", 200, "application/octet-stream", -1, 4, payloadDigest(payload)},
		{"stream incompleto", 200, "application/octet-stream", 9, 1024, payloadDigest(payload)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			directory := t.TempDir()
			original := filepath.Join(directory, "modelo-existente.bin")
			if err := os.WriteFile(original, []byte("preservar"), 0o600); err != nil {
				t.Fatal(err)
			}
			d := NewDownloader(directory)
			d.client = syntheticDownload(payload, tc.status, tc.media, tc.length)
			if _, err := d.fetch(t.Context(), directory, engineURL, tc.limit, tc.digest, false, nil); err == nil {
				t.Fatal("um download inválido foi instalado")
			}
			files, err := os.ReadDir(directory)
			if err != nil || len(files) != 1 || files[0].Name() != filepath.Base(original) {
				t.Fatalf("a falha removeu um arquivo anterior ou deixou um temporário: %v, %v", files, err)
			}
		})
	}
}

func TestWhisperDownloadCancelRemovesPartialFile(t *testing.T) {
	directory := t.TempDir()
	d := NewDownloader(directory)
	payload := bytes.Repeat([]byte("a"), 600*1024)
	d.client = syntheticDownload(payload, 200, "application/octet-stream", int64(len(payload)))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	_, err := d.fetch(ctx, directory, engineURL, int64(len(payload)), payloadDigest(payload), false, func(DownloadProgress) {
		cancel()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("download ignorou o cancelamento: %v", err)
	}
	files, err := os.ReadDir(directory)
	if err != nil || len(files) != 0 {
		t.Fatalf("cancelar deixou um download parcial: %v, %v", files, err)
	}
}

func TestWhisperDownloadAllowsOnlyOfficialHTTPSDestinations(t *testing.T) {
	for _, address := range []string{
		"https://github.com/a", "https://release-assets.githubusercontent.com/a",
		"https://huggingface.co/a", "https://cdn-lfs-us-1.hf.co/a",
	} {
		parsed, _ := url.Parse(address)
		if !downloadHostAllowed(parsed) {
			t.Fatalf("um destino oficial foi rejeitado: %s", address)
		}
	}
	for _, address := range []string{
		"http://github.com/a", "https://huggingface.co.evil.example/a",
		"https://user:password@github.com/a", "https://github.com:444/a", "https://127.0.0.1/a",
	} {
		parsed, _ := url.Parse(address)
		if downloadHostAllowed(parsed) {
			t.Fatalf("um destino não reconhecido foi aceito: %s", address)
		}
	}
}

func writeEngineZip(t *testing.T, names []string, symlink bool) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "engine.zip")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for _, name := range names {
		header := &zip.FileHeader{Name: name}
		if symlink {
			header.SetMode(os.ModeSymlink | 0o777)
		}
		part, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte("synthetic engine file")); err != nil {
			t.Fatal(err)
		}
	}
	if err := errors.Join(writer.Close(), file.Close()); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestWhisperEngineExtractionKeepsOnlyRequiredExecutableAndLibraries(t *testing.T) {
	names := []string{"Release/unrelated.exe", "Release/readme.txt"}
	for _, name := range engineFiles {
		names = append(names, "Release/"+name)
	}
	directory := t.TempDir()
	if err := unpackEngine(t.Context(), writeEngineZip(t, names, false), directory); err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(directory)
	if err != nil || len(files) != len(engineFiles) || !engineInstalled(directory) {
		t.Fatalf("a instalação incluiu executáveis auxiliares ou perdeu bibliotecas: %v, %v", files, err)
	}
}

func TestWhisperEngineRejectsTraversalDuplicatesSymlinksAndIncompletePackage(t *testing.T) {
	for _, tc := range []struct {
		name    string
		files   []string
		symlink bool
	}{
		{"caminho fora da pasta", []string{"../whisper-cli.exe"}, false},
		{"caminho absoluto", []string{"/whisper-cli.exe"}, false},
		{"caminho Windows", []string{`C:\whisper-cli.exe`}, false},
		{"link", []string{"Release/whisper-cli.exe"}, true},
		{"duplicado", []string{"Release/ggml.dll", "Other/ggml.dll"}, false},
		{"incompleto", []string{"Release/whisper-cli.exe", "Release/whisper.dll", "Release/ggml.dll"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := unpackEngine(t.Context(), writeEngineZip(t, tc.files, tc.symlink), t.TempDir()); err == nil {
				t.Fatal("um pacote inválido foi aceito")
			}
		})
	}
}

func TestWhisperBlockedModelDownloadPreservesExistingModel(t *testing.T) {
	directory := t.TempDir()
	target := ModelPath(directory, "base")
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("modelo existente"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := NewDownloader(directory)
	d.client = syntheticDownload([]byte("rede bloqueada"), 403, "text/html", 14)
	if _, err := d.InstallModel(t.Context(), "base", nil); err == nil || !strings.Contains(err.Error(), "bloqueou") {
		t.Fatalf("o bloqueio de rede não explicou a falha: %v", err)
	}
	actual, err := os.ReadFile(target)
	if err != nil || string(actual) != "modelo existente" {
		t.Fatal("uma falha de download removeu o modelo anterior")
	}
	if _, err := d.InstallModel(t.Context(), "../outro", nil); err == nil {
		t.Fatal("um modelo fora do catálogo foi baixado")
	}
}
