package whisper

import (
	"archive/zip"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ai-talker/internal/platform"
)

type DownloadProgress struct {
	Message string
	Bytes   int64
	Total   int64
}

type Downloader struct {
	directory string
	client    *http.Client
}

func NewDownloader(directory string) *Downloader {
	return &Downloader{directory: directory, client: &http.Client{
		Timeout: time.Hour,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 10 || !downloadHostAllowed(request.URL) {
				return errors.New("o download redirecionou para um endereço não reconhecido")
			}
			return nil
		},
	}}
}

func downloadHostAllowed(address *url.URL) bool {
	if address.Scheme != "https" || address.User != nil || (address.Port() != "" && address.Port() != "443") {
		return false
	}
	host := strings.ToLower(address.Hostname())
	return host == "github.com" || host == "release-assets.githubusercontent.com" ||
		host == "objects.githubusercontent.com" || host == "huggingface.co" ||
		strings.HasSuffix(host, ".huggingface.co") || host == "hf.co" || strings.HasSuffix(host, ".hf.co")
}

func (d *Downloader) InstallEngine(ctx context.Context, progress func(DownloadProgress)) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(d.directory, "whisper"), 0o700); err != nil {
		return "", fmt.Errorf("preparar a pasta Whisper: %w", err)
	}
	lock, err := platform.TryLock(filepath.Join(d.directory, "whisper", "install.lock"))
	if err != nil {
		return "", errors.New("há outra instalação Whisper em andamento; aguarde sua conclusão")
	}
	defer lock.Close()
	target := filepath.Dir(BinaryPath(d.directory))
	if engineInstalled(target) {
		emitDownload(progress, "Motor Whisper já instalado.", 1, 1)
		return BinaryPath(d.directory), nil
	}
	archive, err := d.fetch(ctx, filepath.Join(d.directory, "whisper"), engineURL, engineBytes, engineSHA256, false, progress)
	if err != nil {
		return "", err
	}
	defer os.Remove(archive)
	stage, err := os.MkdirTemp(filepath.Dir(target), ".engine-")
	if err != nil {
		return "", fmt.Errorf("preparar o motor Whisper: %w", err)
	}
	defer os.RemoveAll(stage)
	emitDownload(progress, "Preparando o motor Whisper…", 0, 0)
	if err := unpackEngine(ctx, archive, stage); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(stage, "LICENSE.txt"), engineLicense, 0o600); err != nil {
		return "", fmt.Errorf("preservar a licença do Whisper: %w", err)
	}
	if _, err := os.Stat(target); err == nil {
		backup := target + ".anterior-" + time.Now().UTC().Format("20060102-150405.000000000")
		if err := renameArtifact(ctx, target, backup); err != nil {
			return "", fmt.Errorf("preservar o motor anterior: %w", err)
		}
		if err := renameArtifact(ctx, stage, target); err != nil {
			return "", errors.Join(fmt.Errorf("concluir a instalação Whisper: %w", err), restoreArtifact(backup, target))
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("verificar a instalação Whisper: %w", err)
	} else if err := renameArtifact(ctx, stage, target); err != nil {
		return "", fmt.Errorf("concluir a instalação Whisper: %w", err)
	}
	emitDownload(progress, "Motor Whisper instalado.", 1, 1)
	return BinaryPath(d.directory), nil
}

func (d *Downloader) InstallModel(ctx context.Context, id string, progress func(DownloadProgress)) (string, error) {
	return d.InstallModelFrom(ctx, id, SourceGitHub, progress)
}

// InstallModelFrom verifies and installs a model from the selected source.
// Failure or cancellation preserves existing weights and never changes hosts.
func (d *Downloader) InstallModelFrom(ctx context.Context, id, source string, progress func(DownloadProgress)) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	model, ok := ModelByID(id)
	if !ok {
		return "", errors.New("selecione um modelo da lista para baixar")
	}
	return d.installModelFrom(ctx, model, source, progress)
}

func (d *Downloader) installModel(ctx context.Context, model Model, progress func(DownloadProgress)) (string, error) {
	return d.installModelFrom(ctx, model, SourceGitHub, progress)
}

func (d *Downloader) installModelFrom(ctx context.Context, model Model, source string, progress func(DownloadProgress)) (string, error) {
	address, selectedDigest, selectedLegacy, err := model.DownloadFrom(source)
	if err != nil {
		return "", err
	}
	id := model.ID
	directory := filepath.Dir(ModelPath(d.directory, id))
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", fmt.Errorf("preparar a pasta de modelos Whisper: %w", err)
	}
	lock, err := platform.TryLock(filepath.Join(directory, ".download.lock"))
	if err != nil {
		return "", errors.New("há outro download de modelo em andamento; aguarde sua conclusão")
	}
	defer lock.Close()
	target := ModelPath(d.directory, id)
	digest, legacy := model.SHA1, true
	if digest == "" {
		digest, legacy = model.SHA256, false
	}
	if valid, err := verifyFile(ctx, target, digest, legacy); err != nil {
		return "", err
	} else if valid {
		emitDownload(progress, "Modelo "+model.Name+" já instalado.", 1, 1)
		return target, nil
	}
	host := "GitHub"
	if source == SourceHuggingFace {
		host = "Hugging Face"
	}
	emitDownload(progress, "Baixando "+model.Name+" pelo "+host+"…", 0, 0)
	temporary, err := d.fetch(ctx, directory, address, model.MaxBytes, selectedDigest, selectedLegacy, progress)
	if err != nil {
		return "", err
	}
	defer os.Remove(temporary)
	if _, err := os.Stat(target); err == nil {
		backup := target + ".anterior-" + time.Now().UTC().Format("20060102-150405.000000000")
		if err := renameArtifact(ctx, target, backup); err != nil {
			return "", fmt.Errorf("preservar o modelo anterior: %w", err)
		}
		if err := renameArtifact(ctx, temporary, target); err != nil {
			return "", errors.Join(fmt.Errorf("concluir o download do modelo: %w", err), restoreArtifact(backup, target))
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("verificar o destino do modelo: %w", err)
	} else if err := renameArtifact(ctx, temporary, target); err != nil {
		return "", fmt.Errorf("concluir o download do modelo: %w", err)
	}
	emitDownload(progress, "Modelo "+model.Name+" instalado.", 1, 1)
	return target, nil
}

func (d *Downloader) fetch(
	ctx context.Context, directory, address string, limit int64, digest string, legacySHA1 bool,
	progress func(DownloadProgress),
) (path string, err error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil || !downloadHostAllowed(request.URL) {
		return "", errors.New("o endereço de download é inválido")
	}
	request.Header.Set("User-Agent", "AI-Talker-Whisper/1")
	response, err := d.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", errors.New("Não foi possível baixar o arquivo. Confira a conexão e as restrições de acesso ao GitHub ou Hugging Face.")
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusUnauthorized {
		return "", errors.New("A rede ou o servidor bloqueou o download. Libere o acesso ao site oficial na sua rede ou selecione um arquivo local existente.")
	}
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("o servidor de download respondeu HTTP %d; tente novamente", response.StatusCode)
	}
	if strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/html") {
		return "", errors.New("O download retornou uma página em vez do arquivo. Confira o bloqueio da rede ou selecione um arquivo local existente.")
	}
	if response.ContentLength > limit {
		return "", errors.New("o arquivo de download excede o tamanho esperado")
	}
	file, err := os.CreateTemp(directory, ".download-")
	if err != nil {
		return "", fmt.Errorf("preparar o download: %w", err)
	}
	path = file.Name()
	temporaryPath := path
	complete := false
	defer func() {
		err = errors.Join(err, file.Close())
		if !complete || err != nil {
			os.Remove(temporaryPath)
		}
	}()
	checksum := artifactHash(legacySHA1)
	buffer := make([]byte, 256*1024)
	reader := io.LimitReader(response.Body, limit+1)
	lastProgress, received := time.Time{}, int64(0)
	for {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		read, readErr := reader.Read(buffer)
		if read > 0 {
			received += int64(read)
			if received > limit {
				return "", errors.New("o arquivo de download excede o tamanho esperado")
			}
			if _, err := file.Write(buffer[:read]); err != nil {
				return "", fmt.Errorf("salvar o download: %w", err)
			}
			checksum.Write(buffer[:read])
			if time.Since(lastProgress) >= 100*time.Millisecond {
				lastProgress = time.Now()
				emitDownload(progress, "Baixando…", received, response.ContentLength)
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", errors.New("o download foi interrompido; tente novamente")
		}
	}
	if response.ContentLength >= 0 && received != response.ContentLength {
		return "", errors.New("o download terminou incompleto; tente novamente")
	}
	if hex.EncodeToString(checksum.Sum(nil)) != digest {
		return "", errors.New("o arquivo baixado não corresponde ao checksum esperado; a instalação anterior foi preservada")
	}
	if err := file.Sync(); err != nil {
		return "", fmt.Errorf("concluir o arquivo baixado: %w", err)
	}
	complete = true
	return path, nil
}

var engineFiles = []string{"whisper-cli.exe", "whisper.dll", "ggml.dll", "ggml-base.dll", "ggml-cpu-x64.dll"}

func engineInstalled(directory string) bool {
	for _, name := range engineFiles {
		info, err := os.Stat(filepath.Join(directory, name))
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			return false
		}
	}
	return true
}

func artifactHash(legacySHA1 bool) hash.Hash {
	if legacySHA1 {
		// Upstream publishes SHA-1 for GGML file integrity. HTTPS validates the
		// server; executable archives use the upstream SHA-256 instead.
		return sha1.New()
	}
	return sha256.New()
}

func verifyFile(ctx context.Context, path, digest string, legacySHA1 bool) (bool, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("verificar o modelo instalado: %w", err)
	}
	defer file.Close()
	hash := artifactHash(legacySHA1)
	buffer := make([]byte, 256*1024)
	for {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		read, err := file.Read(buffer)
		hash.Write(buffer[:read])
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return false, fmt.Errorf("verificar o modelo instalado: %w", err)
		}
	}
	return hex.EncodeToString(hash.Sum(nil)) == digest, nil
}

func emitDownload(progress func(DownloadProgress), message string, received, total int64) {
	if progress != nil {
		progress(DownloadProgress{Message: message, Bytes: received, Total: total})
	}
}

func unpackEngine(ctx context.Context, archive, directory string) error {
	reader, err := zip.OpenReader(archive)
	if err != nil {
		return errors.New("o pacote Whisper baixado não é um ZIP válido")
	}
	defer reader.Close()
	seen := make(map[string]bool)
	var total uint64
	for _, entry := range reader.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		clean := strings.ReplaceAll(entry.Name, "\\", "/")
		if strings.HasPrefix(clean, "/") || strings.Contains(clean, ":") || strings.Contains(clean, "../") ||
			entry.Mode()&os.ModeSymlink != 0 {
			return errors.New("o pacote Whisper contém um caminho inválido")
		}
		name := filepath.Base(clean)
		allowed := name == "whisper-cli.exe" || name == "whisper.dll" || (strings.HasPrefix(name, "ggml") && strings.HasSuffix(name, ".dll"))
		if !allowed || entry.FileInfo().IsDir() {
			continue
		}
		key := strings.ToLower(name)
		total += entry.UncompressedSize64
		if seen[key] || total > 100<<20 {
			return errors.New("o pacote Whisper contém arquivos duplicados ou excede o tamanho esperado")
		}
		seen[key] = true
		input, err := entry.Open()
		if err != nil {
			return fmt.Errorf("ler o motor Whisper: %w", err)
		}
		output, err := os.OpenFile(filepath.Join(directory, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
		if err != nil {
			input.Close()
			return fmt.Errorf("preparar o motor Whisper: %w", err)
		}
		copied, copyErr := copyContext(ctx, output, io.LimitReader(input, int64(entry.UncompressedSize64)+1))
		closeErr := errors.Join(input.Close(), output.Close())
		if copyErr != nil || closeErr != nil || uint64(copied) != entry.UncompressedSize64 {
			return fmt.Errorf("extrair o motor Whisper: %w", errors.Join(copyErr, closeErr, io.ErrUnexpectedEOF))
		}
	}
	for _, name := range engineFiles {
		if !seen[name] {
			return errors.New("o pacote não contém o motor Whisper completo")
		}
	}
	return nil
}

func copyContext(ctx context.Context, output io.Writer, input io.Reader) (int64, error) {
	buffer := make([]byte, 256*1024)
	var total int64
	for {
		if ctx.Err() != nil {
			return total, ctx.Err()
		}
		read, err := input.Read(buffer)
		if read > 0 {
			written, writeErr := output.Write(buffer[:read])
			total += int64(written)
			if writeErr != nil {
				return total, writeErr
			}
			if written != read {
				return total, io.ErrShortWrite
			}
		}
		if errors.Is(err, io.EOF) {
			return total, nil
		}
		if err != nil {
			return total, err
		}
	}
}
