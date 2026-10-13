// Package video captures a selected desktop rectangle and muxes the app's
// existing WAV sources into the finished recording. Source WAV files are never
// edited or replaced.
package video

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"ai-talker/internal/audio"
	"ai-talker/internal/platform"
	"ai-talker/internal/recorder"
)

const (
	minimumRegionSide = 32
	maximumRegionSide = 16384
)

type AudioTrack struct {
	Path     string
	Offset   float64
	Duration float64
}

type segmentProcess struct {
	path   string
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	done   chan error
	stderr *boundedOutput
}

type Recorder struct {
	mu           sync.Mutex
	ffmpeg       string
	directory    string
	audioPath    string
	region       platform.ScreenRegion
	preset       Preset
	newOutline   func(context.Context, platform.ScreenRegion) (platform.VideoOutline, error)
	outline      platform.VideoOutline
	tempDir      string
	segments     []string
	timings      []segmentTiming
	audioOrigin  time.Time
	nextPosition float64
	nextActiveAt time.Time
	captureCtx   context.Context
	captureArgs  func(platform.ScreenRegion, string) []string
	active       *segmentProcess
	finished     bool
}

func NewRecorder(directory, audioPath string, region platform.ScreenRegion) (*Recorder, error) {
	processor, err := audio.New()
	if err != nil {
		return nil, err
	}
	region, err = NormalizeRegion(region)
	if err != nil {
		return nil, err
	}
	directory, err = filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("localizar a pasta de vídeo: %w", err)
	}
	if audioPath == "" {
		return nil, errors.New("o vídeo precisa estar ligado a uma gravação de áudio")
	}
	audioPath, err = filepath.Abs(audioPath)
	if err != nil {
		return nil, errors.New("o vídeo precisa estar ligado a uma gravação de áudio")
	}
	return &Recorder{
		ffmpeg: processor.FFmpeg, directory: directory, audioPath: audioPath, region: region,
		preset: PresetMedium, newOutline: platform.NewVideoOutline,
	}, nil
}

// SetPreset configures the next video before it starts. The chosen preset
// remains unchanged across pauses, resumes and final audio alignment.
func (r *Recorder) SetPreset(preset Preset) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.tempDir != "" || r.finished {
		return errors.New("a qualidade do vídeo fica definida ao começar a gravação")
	}
	if err := preset.validate(); err != nil {
		return err
	}
	r.preset = preset.normalized()
	return nil
}

func NormalizeRegion(region platform.ScreenRegion) (platform.ScreenRegion, error) {
	if region.Width < minimumRegionSide || region.Height < minimumRegionSide ||
		region.Width > maximumRegionSide || region.Height > maximumRegionSide ||
		region.X < -maximumRegionSide || region.Y < -maximumRegionSide ||
		region.X > maximumRegionSide || region.Y > maximumRegionSide {
		return platform.ScreenRegion{}, errors.New("selecione uma área de tela com pelo menos 32 × 32 pixels")
	}
	region.Width -= region.Width % 2
	region.Height -= region.Height % 2
	return region, nil
}

// SetAudioOrigin links the first video frame to the earliest saved audio
// sample. It is called after opening the audio devices and before Start.
func (r *Recorder) SetAudioOrigin(snapshot recorder.Snapshot) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.tempDir == "" {
		r.audioOrigin = snapshot.StartedAt
	}
}

// Start receives the lifetime context of this capture. Pause and resume
// contexts bound their operations without cancelling an active encoder.
func (r *Recorder) Start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished || r.active != nil || len(r.segments) > 0 {
		return errors.New("a captura de vídeo já começou")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(r.directory, 0o700); err != nil {
		return fmt.Errorf("preparar a pasta da gravação de vídeo: %w", err)
	}
	temp, err := os.MkdirTemp(r.directory, ".ai-talker-video-")
	if err != nil {
		return fmt.Errorf("preparar trechos temporários do vídeo: %w", err)
	}
	r.tempDir = temp
	r.captureCtx = ctx
	if r.newOutline != nil {
		outline, err := r.newOutline(ctx, r.region)
		if err != nil {
			_ = r.removeTemporaryLocked()
			r.captureCtx = nil
			return fmt.Errorf("preparar a borda da área de vídeo: %w", err)
		}
		r.outline = outline
	}
	r.nextActiveAt = r.audioOrigin
	if r.nextActiveAt.IsZero() {
		r.nextActiveAt = time.Now()
	}
	if err := r.startSegmentLocked(ctx); err != nil {
		_ = r.closeOutlineLocked()
		_ = r.removeTemporaryLocked()
		r.captureCtx = nil
		return err
	}
	return nil
}

func (r *Recorder) Pause(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished || r.active == nil {
		return nil
	}
	r.closeAudioWindowLocked(r.currentPositionLocked(time.Now()))
	return r.pauseSegmentLocked(ctx)
}

// PauseAt cuts the segment at the PCM position frozen by the audio recorder,
// even if finishing the encoder occurs later on a worker.
func (r *Recorder) PauseAt(ctx context.Context, position float64, _ time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished || r.active == nil {
		return nil
	}
	if !validPosition(position) {
		return errors.New("a posição do áudio para pausar o vídeo é inválida")
	}
	r.closeAudioWindowLocked(position)
	return r.pauseSegmentLocked(ctx)
}

func (r *Recorder) pauseSegmentLocked(ctx context.Context) error {
	if r.outline != nil {
		if err := r.outline.SetPaused(true); err != nil {
			_ = r.closeOutlineLocked()
			return err
		}
	}
	if err := r.stopSegmentLocked(ctx); err != nil {
		_ = r.closeOutlineLocked()
		return err
	}
	return nil
}

func (r *Recorder) Resume(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished {
		return errors.New("a captura de vídeo já foi encerrada")
	}
	if r.active != nil {
		return nil
	}
	if r.captureCtx == nil {
		return errors.New("a captura de vídeo ainda não começou")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.nextActiveAt = time.Now()
	return r.resumeSegmentLocked(ctx)
}

// ResumeAt keeps the PCM position and the time of the user's resume action.
// A slow encoder startup is filled with its first frame instead of shifting
// all subsequent video against the audio.
func (r *Recorder) ResumeAt(ctx context.Context, position float64, activeAt time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished {
		return errors.New("a captura de vídeo já foi encerrada")
	}
	if r.active != nil {
		return nil
	}
	if r.captureCtx == nil {
		return errors.New("a captura de vídeo ainda não começou")
	}
	if !validPosition(position) || activeAt.IsZero() {
		return errors.New("a posição do áudio para retomar o vídeo é inválida")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.nextPosition, r.nextActiveAt = position, activeAt
	return r.resumeSegmentLocked(ctx)
}

func (r *Recorder) resumeSegmentLocked(ctx context.Context) error {
	if r.outline != nil {
		if err := r.outline.SetPaused(false); err != nil {
			_ = r.closeOutlineLocked()
			return err
		}
	}
	if err := r.startSegmentLocked(ctx); err != nil {
		_ = r.closeOutlineLocked()
		return err
	}
	return nil
}

func (r *Recorder) Finish(ctx context.Context, tracks []recorder.Track) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished {
		return "", errors.New("a captura de vídeo já foi encerrada")
	}
	r.finished = true
	if err := r.closeOutlineLocked(); err != nil {
		if r.active != nil {
			_ = r.stopSegmentLocked(ctx)
		}
		_ = r.removeTemporaryLocked()
		return "", err
	}
	audioTracks, err := validatedAudioTracks(tracks)
	if err != nil {
		if r.active != nil {
			_ = r.stopSegmentLocked(ctx)
		}
		_ = r.removeTemporaryLocked()
		return "", err
	}
	if r.active != nil {
		r.closeAudioWindowLocked(audioDuration(audioTracks))
		if err := r.stopSegmentLocked(ctx); err != nil {
			_ = r.removeTemporaryLocked()
			return "", err
		}
	}
	if len(r.segments) == 0 {
		_ = r.removeTemporaryLocked()
		return "", errors.New("nenhum trecho de vídeo foi capturado")
	}
	if err := r.alignSegmentsLocked(ctx, audioDuration(audioTracks)); err != nil {
		_ = r.removeTemporaryLocked()
		return "", err
	}
	output, err := reserveOutputIn(r.directory, r.audioPath)
	if err != nil {
		_ = r.removeTemporaryLocked()
		return "", err
	}
	keepOutput := false
	defer func() {
		if !keepOutput {
			_ = os.Remove(output)
		}
	}()
	videoInput, err := r.joinSegmentsLocked(ctx)
	if err != nil {
		_ = r.removeTemporaryLocked()
		return "", err
	}
	temporaryOutput := filepath.Join(r.tempDir, "finished.mp4")
	if err := r.muxLocked(ctx, videoInput, temporaryOutput, audioTracks); err != nil {
		_ = r.removeTemporaryLocked()
		return "", err
	}
	if err := ctx.Err(); err != nil {
		_ = r.removeTemporaryLocked()
		return "", err
	}
	if err := os.Remove(output); err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = r.removeTemporaryLocked()
		return "", fmt.Errorf("finalizar o nome do vídeo: %w", err)
	}
	if err := os.Rename(temporaryOutput, output); err != nil {
		_ = r.removeTemporaryLocked()
		return "", fmt.Errorf("salvar o vídeo final: %w", err)
	}
	keepOutput = true
	if err := r.removeTemporaryLocked(); err != nil {
		return output, fmt.Errorf("o vídeo foi salvo, mas não foi possível remover trechos temporários: %w", err)
	}
	return output, nil
}

func (r *Recorder) Abort() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var stopErr error
	outlineErr := r.closeOutlineLocked()
	if r.active != nil {
		stopErr = r.stopSegmentLocked(context.Background())
	}
	r.finished = true
	return errors.Join(outlineErr, stopErr, r.removeTemporaryLocked())
}

func (r *Recorder) startSegmentLocked(ctx context.Context) error {
	index := len(r.segments) + 1
	path := filepath.Join(r.tempDir, fmt.Sprintf("segment-%04d.mp4", index))
	args := capturePresetArguments(r.region, path, r.preset)
	if r.captureArgs != nil {
		args = r.captureArgs(r.region, path)
	}
	// A resume command has a short context that ends after its first frame.
	// The encoder belongs to the full capture, whose context Start receives.
	command := exec.CommandContext(r.captureCtx, r.ffmpeg, args...)
	platform.Hide(command)
	command.WaitDelay = 5 * time.Second
	stdin, err := command.StdinPipe()
	if err != nil {
		return fmt.Errorf("preparar a captura de vídeo: %w", err)
	}
	stderr := &boundedOutput{}
	progress := newCaptureProgress()
	command.Stdout, command.Stderr = progress, stderr
	if err := command.Start(); err != nil {
		_ = stdin.Close()
		return fmt.Errorf("iniciar a captura de vídeo: %s: %w", strings.TrimSpace(stderr.String()), err)
	}
	process := &segmentProcess{path: path, cmd: command, stdin: stdin, done: make(chan error, 1), stderr: stderr}
	go func() {
		process.done <- command.Wait()
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.active == process {
			_ = r.closeOutlineLocked()
		}
	}()
	startup := time.NewTimer(10 * time.Second)
	defer startup.Stop()
	select {
	case err := <-process.done:
		_ = stdin.Close()
		if err != nil {
			return fmt.Errorf("iniciar a captura de vídeo: %s: %w", strings.TrimSpace(stderr.String()), err)
		}
		return errors.New("a captura de vídeo terminou antes do primeiro quadro")
	case firstFrame := <-progress.firstFrame:
		r.active = process
		r.segments = append(r.segments, path)
		r.timings = append(r.timings, segmentTiming{
			start: r.nextPosition, activeAt: r.nextActiveAt, firstFrame: firstFrame,
		})
		return nil
	case <-startup.C:
		_ = command.Process.Kill()
		_ = stdin.Close()
		<-process.done
		return errors.New("o codificador não produziu o primeiro quadro do vídeo")
	case <-ctx.Done():
		_ = command.Process.Kill()
		_ = stdin.Close()
		<-process.done
		return ctx.Err()
	}
}

func (r *Recorder) stopSegmentLocked(ctx context.Context) error {
	process := r.active
	r.active = nil
	if process == nil {
		return nil
	}
	_, sendErr := io.WriteString(process.stdin, "q\n")
	closeErr := process.stdin.Close()
	if sendErr != nil {
		_ = process.cmd.Process.Kill()
	}
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	var waitErr error
	select {
	case waitErr = <-process.done:
	case <-timer.C:
		_ = process.cmd.Process.Kill()
		waitErr = <-process.done
		if waitErr == nil {
			waitErr = errors.New("o codificador de vídeo excedeu o tempo para finalizar")
		}
	case <-ctx.Done():
		_ = process.cmd.Process.Kill()
		waitErr = <-process.done
		if waitErr == nil {
			waitErr = ctx.Err()
		}
	}
	if sendErr != nil || closeErr != nil || waitErr != nil {
		diagnostic := strings.TrimSpace(process.stderr.String())
		if diagnostic == "" {
			diagnostic = "o codificador não finalizou o trecho"
		}
		return fmt.Errorf("finalizar a captura de vídeo: %s: %w", diagnostic, errors.Join(sendErr, closeErr, waitErr))
	}
	info, err := os.Stat(process.path)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return fmt.Errorf("o trecho de vídeo ficou vazio: %w", err)
	}
	return nil
}

func (r *Recorder) joinSegmentsLocked(ctx context.Context) (string, error) {
	if len(r.segments) == 1 {
		return r.segments[0], nil
	}
	listPath := filepath.Join(r.tempDir, "segments.ffconcat")
	var list strings.Builder
	list.WriteString("ffconcat version 1.0\n")
	for _, path := range r.segments {
		escaped := strings.ReplaceAll(filepath.ToSlash(path), "'", "\\'")
		fmt.Fprintf(&list, "file '%s'\n", escaped)
	}
	if err := os.WriteFile(listPath, []byte(list.String()), 0o600); err != nil {
		return "", fmt.Errorf("preparar a junção dos trechos de vídeo: %w", err)
	}
	output := filepath.Join(r.tempDir, "joined.mp4")
	args := []string{
		"-hide_banner", "-loglevel", "error", "-y", "-f", "concat", "-safe", "0",
		"-i", listPath, "-map", "0:v:0", "-c:v", "copy", "-an", "-movflags", "+faststart", output,
	}
	if err := r.runFFmpeg(ctx, args); err != nil {
		return "", fmt.Errorf("juntar os trechos de vídeo: %w", err)
	}
	return output, nil
}

func (r *Recorder) muxLocked(ctx context.Context, videoPath, output string, tracks []AudioTrack) error {
	args := []string{"-hide_banner", "-loglevel", "error", "-y", "-i", videoPath}
	filter := audioMixFilter(tracks)
	for _, track := range tracks {
		args = append(args, "-i", track.Path)
	}
	args = append(args, "-filter_complex", filter,
		"-map", "0:v:0", "-map", "[aout]", "-c:v", "copy",
		"-c:a", "aac", "-b:a", "192k")
	if duration := audioDuration(tracks); duration > 0 {
		args = append(args, "-t", strconv.FormatFloat(duration, 'f', 6, 64))
	}
	args = append(args, "-movflags", "+faststart", output)
	return r.runFFmpeg(ctx, args)
}

func captureArguments(region platform.ScreenRegion, path string) []string {
	return capturePresetArguments(region, path, PresetMedium)
}

func (r *Recorder) closeOutlineLocked() error {
	if r.outline == nil {
		return nil
	}
	err := r.outline.Close()
	r.outline = nil
	return err
}

func audioMixFilter(tracks []AudioTrack) string {
	var filter strings.Builder
	for index, track := range tracks {
		delay := int64(math.Round(track.Offset * 48000))
		fmt.Fprintf(&filter, "[%d:a:0]aresample=48000,aformat=sample_fmts=fltp:channel_layouts=stereo,adelay=%dS:all=1[a%d];",
			index+1, delay, index)
	}
	for index := range tracks {
		fmt.Fprintf(&filter, "[a%d]", index)
	}
	if len(tracks) > 1 {
		fmt.Fprintf(&filter, "amix=inputs=%d:duration=longest:dropout_transition=0:normalize=1[aout]", len(tracks))
	} else {
		filter.WriteString("anull[aout]")
	}
	return strings.TrimSuffix(filter.String(), ";")
}

func (r *Recorder) runFFmpeg(ctx context.Context, args []string) error {
	command := exec.CommandContext(ctx, r.ffmpeg, args...)
	platform.Hide(command)
	command.WaitDelay = 5 * time.Second
	stderr := &boundedOutput{}
	command.Stdout, command.Stderr = io.Discard, stderr
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%s: %w", strings.TrimSpace(stderr.String()), err)
	}
	return nil
}

func (r *Recorder) removeTemporaryLocked() error {
	if r.tempDir == "" {
		return nil
	}
	root, err := filepath.Abs(r.directory)
	if err != nil {
		return err
	}
	target, err := filepath.Abs(r.tempDir)
	if err != nil || filepath.Dir(target) != root || !strings.HasPrefix(filepath.Base(target), ".ai-talker-video-") {
		return errors.New("recusar a remoção de um diretório temporário de vídeo fora da pasta reservada")
	}
	removeErr := os.RemoveAll(target)
	r.tempDir = ""
	return removeErr
}

func validatedAudioTracks(tracks []recorder.Track) ([]AudioTrack, error) {
	var result []AudioTrack
	seen := make(map[string]bool)
	for _, track := range tracks {
		if track.Snapshot.WrittenSamples == 0 {
			continue
		}
		path, err := filepath.Abs(track.Snapshot.Path)
		if err != nil || path == "" || seen[strings.ToLower(path)] ||
			!(track.Offset >= 0 && track.Offset <= 24*60*60) ||
			math.IsNaN(track.Offset) || math.IsInf(track.Offset, 0) {
			return nil, errors.New("as fontes de áudio do vídeo são inválidas")
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return nil, errors.New("uma fonte de áudio do vídeo não está disponível")
		}
		seen[strings.ToLower(path)] = true
		duration := track.Snapshot.Duration
		if track.Snapshot.SampleRate > 0 {
			duration = float64(track.Snapshot.WrittenSamples) / float64(track.Snapshot.SampleRate)
		}
		if math.IsNaN(duration) || math.IsInf(duration, 0) || duration < 0 {
			return nil, errors.New("a duração de uma fonte de áudio do vídeo é inválida")
		}
		result = append(result, AudioTrack{Path: path, Offset: track.Offset, Duration: duration})
	}
	if len(result) == 0 || len(result) > 2 {
		return nil, errors.New("o vídeo precisa de uma ou duas fontes de áudio salvas")
	}
	slices.SortStableFunc(result, func(a, b AudioTrack) int {
		return strings.Compare(strings.ToLower(filepath.Base(a.Path)), strings.ToLower(filepath.Base(b.Path)))
	})
	return result, nil
}

func reserveOutput(audioPath string) (string, error) {
	return reserveOutputIn(filepath.Dir(audioPath), audioPath)
}

func reserveOutputIn(directory, audioPath string) (string, error) {
	stem := strings.TrimSuffix(filepath.Base(audioPath), filepath.Ext(audioPath))
	stem = strings.TrimSuffix(stem, "-mic")
	for index := 0; index < 1000; index++ {
		name := stem + "-video.mp4"
		if index > 0 {
			name = fmt.Sprintf("%s-video-%d.mp4", stem, index+1)
		}
		path := filepath.Join(directory, name)
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("reservar o nome do vídeo: %w", err)
		}
		if err := file.Close(); err != nil {
			_ = os.Remove(path)
			return "", fmt.Errorf("reservar o nome do vídeo: %w", err)
		}
		return path, nil
	}
	return "", errors.New("não foi possível reservar um nome exclusivo para o vídeo")
}

type boundedOutput struct{ bytes.Buffer }

func (b *boundedOutput) Write(payload []byte) (int, error) {
	length := len(payload)
	if available := 32*1024 - b.Len(); available > 0 {
		_, _ = b.Buffer.Write(payload[:min(available, length)])
	}
	return length, nil
}
