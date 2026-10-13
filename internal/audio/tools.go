package audio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"ai-talker/internal/platform"
)

type Processor struct {
	FFmpeg  string
	FFprobe string
}

// Keep the useful wideband speech range while reducing rumble and energy
// close to Nyquist. Resample first so the filters also accept 8 kHz sources.
const speechFilter = "aresample=16000,highpass=f=80,lowpass=f=7000"

func New() (*Processor, error) {
	ffmpeg, err := findTool("ffmpeg", "AI_TALKER_FFMPEG")
	if err != nil {
		return nil, err
	}
	ffprobe, err := findTool("ffprobe", "AI_TALKER_FFPROBE")
	if err != nil {
		return nil, err
	}
	return &Processor{FFmpeg: ffmpeg, FFprobe: ffprobe}, nil
}

func findTool(name, environment string) (string, error) {
	if configured := os.Getenv(environment); configured != "" {
		path, err := exec.LookPath(configured)
		if err != nil {
			return "", fmt.Errorf("não foi possível localizar %s definido em %s: %w", name, environment, err)
		}
		return path, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("localizar a aplicação: %w", err)
	}
	for _, candidate := range []string{
		filepath.Join(filepath.Dir(executable), "tools", name+".exe"),
		filepath.Join(filepath.Dir(executable), "tools", name),
	} {
		if details, err := os.Stat(candidate); err == nil && !details.IsDir() {
			return candidate, nil
		}
	}
	if path, err := exec.LookPath(name); err == nil {
		return path, nil
	}
	return "", fmt.Errorf("%s não foi encontrado; use o pacote Windows completo ou defina %s", name, environment)
}

func (p *Processor) Duration(ctx context.Context, path string) (float64, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, p.FFprobe,
		"-v", "error", "-protocol_whitelist", "file,pipe", "-select_streams", "a:0",
		"-show_entries", "stream=duration:format=duration", "-of", "json", path,
	)
	platform.Hide(cmd)
	var stdout, stderr limitedBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = 3 * time.Second
	if err := cmd.Run(); err != nil {
		if probeCtx.Err() != nil {
			return 0, probeCtx.Err()
		}
		return 0, fmt.Errorf("ler a duração do áudio: %s: %w", strings.TrimSpace(stderr.String()), err)
	}
	var probe struct {
		Streams []struct {
			Duration string `json:"duration"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &probe); err != nil {
		return 0, fmt.Errorf("ler os detalhes do áudio: %w", err)
	}
	if len(probe.Streams) == 0 {
		return 0, errors.New("o arquivo não contém uma faixa de áudio")
	}
	duration, err := strconv.ParseFloat(probe.Streams[0].Duration, 64)
	if err == nil && duration > 0 {
		return duration, nil
	}
	duration, err = strconv.ParseFloat(probe.Format.Duration, 64)
	if err != nil || duration <= 0 {
		// Some valid streams do not declare a duration. Decoding gives the final
		// duration; this value is used only to estimate progress.
		return 0, nil
	}
	return duration, nil
}

func (p *Processor) Encode(ctx context.Context, pcm []byte) ([]byte, error) {
	encodeCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cmd := exec.CommandContext(encodeCtx, p.FFmpeg,
		"-v", "error", "-nostdin", "-f", "s16le", "-ar", strconv.Itoa(SampleRate),
		"-ac", "1", "-i", "pipe:0", "-codec:a", "libmp3lame",
		"-b:a", strconv.Itoa(BitRate), "-f", "mp3", "pipe:1",
	)
	platform.Hide(cmd)
	cmd.Stdin = bytes.NewReader(pcm)
	var stderr limitedBuffer
	var encoded bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = &encoded
	cmd.WaitDelay = 3 * time.Second
	if err := cmd.Run(); err != nil {
		if encodeCtx.Err() != nil {
			return nil, encodeCtx.Err()
		}
		return nil, fmt.Errorf("comprimir o áudio: %s: %w", strings.TrimSpace(stderr.String()), err)
	}
	if encoded.Len() > MaxAudioBytes {
		return nil, errors.New("a parte de áudio excede o limite seguro de envio; reduza a duração das partes")
	}
	return encoded.Bytes(), nil
}

type ChunkOptions struct {
	ChunkSeconds   int
	OverlapSeconds float64
	Format         string
	KeepPCM        bool
	// ContextSamples precede new speech in the decoded mono 16 kHz PCM.
	// They remain in encoded audio but cannot trigger the speech detector.
	ContextSamples uint64
	OnDecode       func(float64)
}

func (p *Processor) Split(ctx context.Context, path string, options ChunkOptions, yield func(Chunk) error) error {
	decodeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(decodeCtx, p.FFmpeg,
		"-v", "error", "-nostdin", "-protocol_whitelist", "file,pipe", "-i", path, "-map", "0:a:0",
		"-vn", "-ac", "1", "-af", speechFilter,
		"-ar", strconv.Itoa(SampleRate), "-f", "s16le", "pipe:1",
	)
	platform.Hide(cmd)
	var stderr limitedBuffer
	cmd.Stderr = &stderr
	cmd.WaitDelay = 3 * time.Second
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("preparar a leitura do áudio: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("abrir o decodificador de áudio: %w", err)
	}
	encode := p.Encode
	if options.Format == FormatWAV {
		encode = EncodeWAV
	}
	splitErr := SplitPCM(ctx, stdout, options, encode, yield)
	if splitErr != nil {
		cancel()
	}
	waitErr := cmd.Wait()
	if splitErr != nil {
		return splitErr
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if waitErr != nil {
		return fmt.Errorf("decodificar o áudio: %s: %w", strings.TrimSpace(stderr.String()), waitErr)
	}
	return nil
}

// limitedBuffer drains the child's output even after the diagnostic limit.
type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(payload []byte) (int, error) {
	size := len(payload)
	if available := 64*1024 - b.Len(); available > 0 {
		b.Buffer.Write(payload[:min(size, available)])
	}
	return size, nil
}

type Encoder func(context.Context, []byte) ([]byte, error)

// SplitPCM keeps at most one PCM part in memory and never emits an overlap-only
// final part. Part boundaries are derived from sample counts, not probe metadata.
func SplitPCM(
	ctx context.Context,
	source io.Reader,
	options ChunkOptions,
	encode Encoder,
	yield func(Chunk) error,
) error {
	if options.ChunkSeconds < 1 || options.ChunkSeconds > 300 {
		return errors.New("a duração da parte deve estar entre 1 e 300 segundos")
	}
	if options.Format != "" && options.Format != FormatMP3 && options.Format != FormatWAV {
		return errors.New("selecione MP3 ou WAV para preparar o áudio")
	}
	if !(options.OverlapSeconds >= 0 && options.OverlapSeconds < float64(options.ChunkSeconds)) {
		return errors.New("a duração da parte deve superar a sobreposição")
	}
	targetBytes := options.ChunkSeconds * SampleRate * SampleBytes
	overlapBytes := int(options.OverlapSeconds*SampleRate) * SampleBytes
	buffer := make([]byte, targetBytes)
	var kept, index int
	var startSamples, decodedSamples int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		read, err := io.ReadFull(source, buffer[kept:])
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
			return fmt.Errorf("ler as amostras de áudio: %w", err)
		}
		if read == 0 {
			return nil
		}
		count := kept + read
		if count%SampleBytes != 0 {
			return errors.New("o áudio PCM termina com uma amostra incompleta")
		}
		decodedSamples += int64(read / SampleBytes)
		if options.OnDecode != nil {
			options.OnDecode(float64(decodedSamples) / SampleRate)
		}
		payload := buffer[:count]
		contextSamples := uint64(0)
		if options.ContextSamples > uint64(startSamples) {
			contextSamples = min(options.ContextSamples-uint64(startSamples), uint64(count/SampleBytes))
		}
		activity, err := DetectSpeech(payload[int(contextSamples)*SampleBytes:])
		if err != nil {
			return err
		}
		chunk := Chunk{
			Index: index, Start: float64(startSamples) / SampleRate,
			Duration:  float64(count) / (SampleRate * SampleBytes),
			HasSpeech: activity.HasSpeech(), SpeechSeconds: activity.VoicedSeconds,
			Format: options.Format,
		}
		if chunk.Format == "" {
			chunk.Format = FormatMP3
		}
		if chunk.HasSpeech {
			if options.KeepPCM {
				chunk.PCM = bytes.Clone(payload)
			}
			chunk.Audio, err = encode(ctx, payload)
			if err != nil {
				return err
			}
			if chunk.Format == FormatMP3 && len(chunk.Audio) > MaxAudioBytes {
				return errors.New("a parte de áudio excede o limite seguro de envio")
			}
		}
		if err := yield(chunk); err != nil {
			return err
		}
		if count < targetBytes {
			return nil
		}
		copy(buffer, buffer[count-overlapBytes:count])
		kept = overlapBytes
		startSamples += int64((count - kept) / SampleBytes)
		index++
	}
}
