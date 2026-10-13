package playback

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"ai-talker/internal/audio"
	"ai-talker/internal/platform"
	"ai-talker/internal/recorder"
	"ai-talker/internal/store"
)

type preparedAudio struct{ path string }

func (audio preparedAudio) remove() error {
	if audio.path == "" {
		return nil
	}
	if err := os.Remove(audio.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remover o áudio temporário de reprodução: %w", err)
	}
	return nil
}

type input struct {
	path   string
	offset float64
}

type renderOptions struct {
	Speed float64
	Gain  float64
}

func recordingInputs(source string) ([]input, error) {
	absolute, err := filepath.Abs(source)
	if err != nil || source == "" {
		return nil, errors.New("informe um arquivo de áudio para reproduzir")
	}
	details, err := os.Stat(absolute)
	if err != nil {
		return nil, fmt.Errorf("abrir o áudio carregado: %w", err)
	}
	if !details.Mode().IsRegular() {
		return nil, errors.New("o áudio carregado não é um arquivo")
	}
	var manifest recorder.TeamManifest
	if err := store.ReadJSON(absolute+".team.json", &manifest); errors.Is(err, os.ErrNotExist) {
		return []input{{path: absolute}}, nil
	} else if err != nil {
		return nil, fmt.Errorf("ler as fontes da gravação em Equipe: %w", err)
	}
	if manifest.Version != 1 || len(manifest.Tracks) != 2 {
		return nil, errors.New("as fontes da gravação em Equipe são inválidas")
	}
	var inputs []input
	seen := make(map[string]bool, 2)
	for _, track := range manifest.Tracks {
		if track.ID != "microphone" && track.ID != "system" || seen[track.ID] ||
			!(track.Offset >= 0 && track.Offset <= 24*60*60) {
			return nil, errors.New("as posições das fontes de Equipe são inválidas")
		}
		seen[track.ID] = true
		if track.Snapshot.Path == "" {
			continue
		}
		path, err := filepath.Abs(track.Snapshot.Path)
		if err != nil || !strings.EqualFold(filepath.Dir(path), filepath.Dir(absolute)) {
			return nil, errors.New("uma fonte de Equipe está fora da pasta da gravação")
		}
		if track.ID == "microphone" && !strings.EqualFold(path, absolute) {
			return nil, errors.New("a fonte de microfone não corresponde à gravação carregada")
		}
		snapshot, err := recorder.Inspect(path)
		if err != nil {
			return nil, fmt.Errorf("ler a fonte %s da gravação: %w", track.ID, err)
		}
		if snapshot.WrittenSamples > 0 {
			inputs = append(inputs, input{path: path, offset: track.Offset})
		}
	}
	if len(inputs) == 0 {
		return nil, errors.New("a gravação não contém amostras de áudio")
	}
	return inputs, nil
}

func prepareAudio(ctx context.Context, source, directory string) (prepared preparedAudio, err error) {
	return prepareAudioWithOptions(ctx, source, directory, renderOptions{Speed: 1, Gain: 1})
}

func prepareAudioWithOptions(ctx context.Context, source, directory string, options renderOptions) (prepared preparedAudio, err error) {
	if err := ctx.Err(); err != nil {
		return prepared, err
	}
	if !validPlaybackSpeed(options.Speed) || !(options.Gain >= 0 && options.Gain <= 2) {
		return prepared, errors.New("as opções de velocidade ou ganho da reprodução são inválidas")
	}
	inputs, err := recordingInputs(source)
	if err != nil {
		return prepared, err
	}
	folder := filepath.Join(directory, "playback")
	if err := os.MkdirAll(folder, 0o700); err != nil {
		return prepared, fmt.Errorf("preparar a pasta de reprodução: %w", err)
	}
	file, err := createPlaybackFile(folder)
	if err != nil {
		return prepared, fmt.Errorf("preparar o áudio para ouvir: %w", err)
	}
	prepared.path = file.Name()
	defer func() {
		if err != nil {
			err = errors.Join(err, prepared.remove())
		}
	}()
	// A private copy prevents the native player from locking the original
	// while transcription reads it. Recorder WAVs need no decoding.
	if len(inputs) == 1 && inputs[0].offset == 0 && options.Speed == 1 && options.Gain <= 1 {
		if snapshot, inspectErr := recorder.Inspect(inputs[0].path); inspectErr == nil && snapshot.WrittenSamples > 0 {
			copyErr := copyAudioWithGain(ctx, file, inputs[0].path, options.Gain)
			err = errors.Join(copyErr, file.Close())
			return prepared, err
		}
	}
	if err := file.Close(); err != nil {
		return prepared, err
	}
	processor, err := audio.New()
	if err != nil {
		return prepared, err
	}
	err = renderAudioWithOptions(ctx, processor.FFmpeg, inputs, prepared.path, options)
	return prepared, err
}

func copyAudioWithGain(ctx context.Context, destination io.Writer, source string, gain float64) (err error) {
	if gain == 1 {
		return copyAudio(ctx, destination, source)
	}
	file, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	// Inspect has already restricted this path to the recorder's mono s16
	// WAV header. Only the private copy's PCM is attenuated; timing is exact.
	header := make([]byte, 44)
	if _, err := io.ReadFull(file, header); err != nil {
		return err
	}
	if written, err := destination.Write(header); err != nil {
		return err
	} else if written != len(header) {
		return io.ErrShortWrite
	}
	remaining := int64(binary.LittleEndian.Uint32(header[40:44]))
	buffer := make([]byte, 64*1024)
	for remaining > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		payload := buffer[:min(int64(len(buffer)), remaining)]
		if _, err := io.ReadFull(file, payload); err != nil {
			return err
		}
		for offset := 0; offset < len(payload); offset += 2 {
			sample := int16(binary.LittleEndian.Uint16(payload[offset:]))
			scaled := int16(math.Round(float64(sample) * gain))
			binary.LittleEndian.PutUint16(payload[offset:], uint16(scaled))
		}
		written, err := destination.Write(payload)
		if err != nil {
			return err
		}
		if written != len(payload) {
			return io.ErrShortWrite
		}
		remaining -= int64(len(payload))
	}
	// Valid PCM WAVs can carry metadata chunks after their samples. Preserve
	// those bytes too so the copied RIFF length remains correct.
	_, err = io.CopyBuffer(destination, &contextReader{ctx: ctx, reader: file}, buffer)
	return err
}

func copyAudio(ctx context.Context, destination io.Writer, source string) (err error) {
	file, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	reader := contextReader{ctx: ctx, reader: file}
	_, err = io.CopyBuffer(destination, &reader, make([]byte, 64*1024))
	return err
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}

func renderAudio(ctx context.Context, ffmpeg string, inputs []input, destination string) error {
	return renderAudioWithOptions(ctx, ffmpeg, inputs, destination, renderOptions{Speed: 1, Gain: 1})
}

func renderAudioWithOptions(ctx context.Context, ffmpeg string, inputs []input, destination string, options renderOptions) error {
	if !validPlaybackSpeed(options.Speed) || !(options.Gain >= 0 && options.Gain <= 2) {
		return errors.New("as opções de velocidade ou ganho da reprodução são inválidas")
	}
	args := []string{"-v", "error", "-nostdin", "-y"}
	for _, input := range inputs {
		args = append(args, "-protocol_whitelist", "file,pipe", "-i", input.path)
	}
	var filters []string
	var mixed strings.Builder
	for index, input := range inputs {
		name := "a" + strconv.Itoa(index)
		delay := int64(math.Round(input.offset * 48000))
		filters = append(filters, fmt.Sprintf(
			"[%d:a:0]aresample=48000,aformat=sample_fmts=fltp:channel_layouts=stereo,adelay=%dS:all=1[%s]",
			index, delay, name,
		))
		fmt.Fprintf(&mixed, "[%s]", name)
	}
	if len(inputs) > 1 {
		filters = append(filters, fmt.Sprintf("%samix=inputs=%d:duration=longest:dropout_transition=0:normalize=1[out]",
			mixed.String(), len(inputs)))
	} else {
		filters = append(filters, "[a0]anull[out]")
	}
	output := "[out]"
	if options.Speed != 1 || options.Gain != 1 {
		var effects []string
		remaining := options.Speed
		for remaining > 2 {
			effects = append(effects, "atempo=2")
			remaining /= 2
		}
		if remaining != 1 {
			effects = append(effects, "atempo="+strconv.FormatFloat(remaining, 'f', 2, 64))
		}
		if options.Gain != 1 {
			effects = append(effects, "volume="+strconv.FormatFloat(options.Gain, 'f', 2, 64))
			if options.Gain > 1 {
				effects = append(effects, "alimiter=limit=0.98:attack=5:release=50:level=false")
			}
		}
		output = "[processed]"
		filters = append(filters, "[out]"+strings.Join(effects, ",")+output)
	}
	args = append(args, "-filter_complex", strings.Join(filters, ";"), "-map", output,
		"-vn", "-c:a", "pcm_s16le", "-ar", "48000", "-ac", "2", "-f", "wav", destination)
	command := exec.CommandContext(ctx, ffmpeg, args...)
	platform.Hide(command)
	command.WaitDelay = 3 * time.Second
	var diagnostic diagnosticBuffer
	command.Stderr = &diagnostic
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("preparar o áudio para ouvir: %s: %w", strings.TrimSpace(diagnostic.String()), err)
	}
	return nil
}

type diagnosticBuffer struct{ bytes.Buffer }

func (b *diagnosticBuffer) Write(payload []byte) (int, error) {
	length := len(payload)
	if available := 16*1024 - b.Len(); available > 0 {
		b.Buffer.Write(payload[:min(available, length)])
	}
	return length, nil
}
