//go:build ignore

// This helper records only a verified rectangle inside the native smoke
// process. It uses generated PCM and never opens an audio input or player.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"ai-talker/internal/audio"
	"ai-talker/internal/platform"
	"ai-talker/internal/recorder"
	"ai-talker/internal/store"
	"ai-talker/internal/video"
)

type videoRequest struct {
	Directory     string
	HarnessPID    uint32
	Window        uintptr
	Region        platform.ScreenRegion
	Before, After string
}

type videoResult struct {
	Path, WAV, HashBefore, HashAfter string
	AudioSeconds, MP4AudioSeconds    float64
	VideoSeconds                     float64
	VideoFrames                      int
	BeforeError, AfterError          float64
	SceneDifference                  float64
	OutlineBoundaryError             float64
	OutlineAbsentFromSavedFrames     bool
	InnerOutlineBoundaryError        float64
	InnerOutlineExcluded             bool
	HarnessPID                       uint32
	Region                           platform.ScreenRegion
	ResumeContextCancelled           bool
}

type nativeRect struct{ left, top, right, bottom int32 }
type nativePoint struct{ x, y int32 }

var (
	videoUser32       = syscall.NewLazyDLL("user32.dll")
	videoWindowPID    = videoUser32.NewProc("GetWindowThreadProcessId")
	videoClientRect   = videoUser32.NewProc("GetClientRect")
	videoClientOrigin = videoUser32.NewProc("ClientToScreen")
	videoWindowPoint  = videoUser32.NewProc("WindowFromPoint")
	videoIsChild      = videoUser32.NewProc("IsChild")
	videoDPIAware     = videoUser32.NewProc("SetProcessDpiAwarenessContext")
)

func main() {
	requestPath := flag.String("request", "", "isolated capture request JSON")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	_, _, _ = videoDPIAware.Call(^uintptr(3))
	if *requestPath == "" {
		fmt.Fprintln(os.Stderr, "informe -request com o pedido isolado")
		os.Exit(2)
	}
	var request videoRequest
	if err := store.ReadJSON(*requestPath, &request); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := captureVideoProof(ctx, request); err != nil {
		_ = store.WriteJSON(filepath.Join(request.Directory, "video-error.json"), map[string]string{"error": err.Error()})
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func validateVideoWindow(request videoRequest) error {
	if runtime.GOOS != "windows" || strconv.IntSize != 64 || request.Window == 0 || request.HarnessPID == 0 {
		return errors.New("a prova requer o HWND e o PID de um harness Windows de 64 bits")
	}
	var pid uint32
	ok, _, _ := videoWindowPID.Call(request.Window, uintptr(unsafe.Pointer(&pid)))
	if ok == 0 || pid != request.HarnessPID {
		return errors.New("a região não pertence ao PID informado do harness")
	}
	var bounds nativeRect
	ok, _, _ = videoClientRect.Call(request.Window, uintptr(unsafe.Pointer(&bounds)))
	if ok == 0 {
		return errors.New("não foi possível medir o client do harness")
	}
	var origin nativePoint
	ok, _, _ = videoClientOrigin.Call(request.Window, uintptr(unsafe.Pointer(&origin)))
	if ok == 0 {
		return errors.New("não foi possível localizar o client do harness")
	}
	region := request.Region
	width, height := int(bounds.right-bounds.left), int(bounds.bottom-bounds.top)
	if region.Width < 32 || region.Height < 32 ||
		region.X < int(origin.x) || region.Y < int(origin.y) ||
		region.X+region.Width > int(origin.x)+width || region.Y+region.Height > int(origin.y)+height {
		return errors.New("o retângulo de vídeo saiu do client do harness")
	}
	for _, point := range []nativePoint{
		{int32(region.X + 1), int32(region.Y + 1)},
		{int32(region.X + region.Width - 2), int32(region.Y + 1)},
		{int32(region.X + 1), int32(region.Y + region.Height - 2)},
		{int32(region.X + region.Width - 2), int32(region.Y + region.Height - 2)},
		{int32(region.X + region.Width/2), int32(region.Y + region.Height/2)},
	} {
		packed := uintptr(uint32(point.x)) | uintptr(uint32(point.y))<<32
		hit, _, _ := videoWindowPoint.Call(packed)
		child, _, _ := videoIsChild.Call(request.Window, hit)
		if hit != request.Window && child == 0 {
			return errors.New("outra janela cobriu a região reservada do harness")
		}
	}
	return nil
}

type generatedVideoInput struct {
	mu      sync.Mutex
	rate    int
	receive func([]byte, error)
}

func (i *generatedVideoInput) Open(ctx context.Context, _, rate int) error {
	i.rate = rate
	return ctx.Err()
}
func (i *generatedVideoInput) Start(ctx context.Context, receive func([]byte, error)) error {
	i.mu.Lock()
	i.receive = receive
	i.mu.Unlock()
	return ctx.Err()
}
func (i *generatedVideoInput) Close() error {
	i.mu.Lock()
	i.receive = nil
	i.mu.Unlock()
	return nil
}
func (i *generatedVideoInput) tone(ctx context.Context, seconds, frequency float64, realtime bool) error {
	total, block := int(seconds*float64(i.rate)), i.rate/50
	for offset := 0; offset < total; offset += block {
		if err := ctx.Err(); err != nil {
			return err
		}
		count := min(block, total-offset)
		pcm := make([]byte, count*2)
		for sample := range count {
			value := int16(7000 * math.Sin(2*math.Pi*frequency*float64(offset+sample)/float64(i.rate)))
			binary.LittleEndian.PutUint16(pcm[sample*2:], uint16(value))
		}
		i.mu.Lock()
		receive := i.receive
		i.mu.Unlock()
		if receive == nil {
			return errors.New("a entrada PCM sintética foi encerrada")
		}
		receive(pcm, nil)
		if realtime {
			timer := time.NewTimer(time.Duration(count) * time.Second / time.Duration(i.rate))
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			}
		}
	}
	return nil
}

func captureVideoProof(parent context.Context, request videoRequest) error {
	if err := validateVideoWindow(request); err != nil {
		return err
	}
	if err := os.MkdirAll(request.Directory, 0o700); err != nil {
		return err
	}
	ctx, cancel := context.WithCancelCause(parent)
	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := os.Stat(filepath.Join(request.Directory, "video-abort.json")); err == nil {
					cancel(context.Canceled)
					return
				}
				if err := validateVideoWindow(request); err != nil {
					cancel(err)
					return
				}
			}
		}
	}()
	defer func() { cancel(context.Canceled); <-monitorDone }()
	input := new(generatedVideoInput)
	capture := recorder.NewWithInput(-1, request.Directory, input)
	wavPath, err := capture.Start(ctx)
	if err != nil {
		return err
	}
	defer func() { _, _ = capture.Stop() }()
	engine, err := video.NewRecorder(request.Directory, wavPath, request.Region)
	if err != nil {
		return err
	}
	defer func() { _ = engine.Abort() }()
	insideOutline, err := platform.NewVideoOutline(ctx, platform.ScreenRegion{
		X: request.Region.X + 10, Y: request.Region.Y + 10,
		Width: request.Region.Width - 20, Height: request.Region.Height - 20,
	})
	if err != nil {
		return err
	}
	defer func() { _ = insideOutline.Close() }()
	engine.SetAudioOrigin(capture.Snapshot())
	if err := engine.Start(ctx); err != nil {
		return errors.Join(err, context.Cause(ctx))
	}
	if err := input.tone(ctx, 1.2, 440, true); err != nil {
		return err
	}
	capture.Pause()
	if err := engine.PauseAt(ctx, capture.Snapshot().Duration, time.Now()); err != nil {
		return err
	}
	if err := insideOutline.SetPaused(true); err != nil {
		return err
	}
	if err := input.tone(ctx, 0.5, 1000, false); err != nil {
		return err
	}
	if err := store.WriteJSON(filepath.Join(request.Directory, "video-phase.json"), map[string]string{"phase": "paused"}); err != nil {
		return err
	}
	if err := waitVideoResume(ctx, filepath.Join(request.Directory, "video-resume.json")); err != nil {
		return err
	}
	capture.Resume()
	resumeCtx, resumeCancel := context.WithTimeout(ctx, 15*time.Second)
	err = engine.ResumeAt(resumeCtx, capture.Snapshot().Duration, time.Now())
	resumeCancel()
	if err != nil {
		return err
	}
	resumeContextCancelled := errors.Is(resumeCtx.Err(), context.Canceled)
	if err := insideOutline.SetPaused(false); err != nil {
		return err
	}
	if !resumeContextCancelled {
		return errors.New("a prova não encerrou o contexto do comando de retomada")
	}
	if err := input.tone(ctx, 1.2, 880, true); err != nil {
		return err
	}
	if _, err := capture.Stop(); err != nil {
		return err
	}
	original, err := os.ReadFile(wavPath)
	if err != nil {
		return err
	}
	before := sha256.Sum256(original)
	if err := insideOutline.Close(); err != nil {
		return err
	}
	output, err := engine.Finish(ctx, []recorder.Track{{ID: "microphone", Snapshot: capture.Snapshot()}})
	if err != nil {
		return errors.Join(err, context.Cause(ctx))
	}
	current, err := os.ReadFile(wavPath)
	if err != nil {
		return err
	}
	after := sha256.Sum256(current)
	if before != after || math.Abs(capture.Snapshot().Duration-2.4) > 0.001 {
		return errors.New("a prova alterou o WAV ou não descartou o intervalo pausado")
	}
	processor, err := audio.New()
	if err != nil {
		return err
	}
	videoSeconds, videoFrames, err := inspectVideoProof(ctx, processor.FFprobe, output, request.Region)
	if err != nil {
		return err
	}
	pcm, err := videoCommand(ctx, processor.FFmpeg, "-v", "error", "-nostdin", "-i", output,
		"-vn", "-ar", strconv.Itoa(audio.SampleRate), "-ac", "1", "-f", "s16le", "pipe:1")
	if err != nil {
		return err
	}
	seconds := float64(len(pcm)/2) / audio.SampleRate
	if math.Abs(seconds-2.4) > 0.12 {
		return fmt.Errorf("o MP4 perdeu o final do áudio: %.4f s", seconds)
	}
	for _, interval := range []struct{ at, frequency float64 }{{0.4, 440}, {1.6, 880}} {
		if videoToneAmplitude(pcm, interval.at, interval.frequency) < 0.11 ||
			videoToneAmplitude(pcm, interval.at, 1000) > 0.025 {
			return fmt.Errorf("a faixa de áudio não preservou fala/pausa em %.1f s", interval.at)
		}
	}
	firstError, err := compareVideoFrame(ctx, processor.FFmpeg, output, "0.4", request.Before, request.Region)
	if err != nil {
		return err
	}
	lastError, err := compareVideoFrame(ctx, processor.FFmpeg, output, "1.6", request.After, request.Region)
	if err != nil {
		return err
	}
	tailError, err := compareVideoFrame(ctx, processor.FFmpeg, output, "2.2", request.After, request.Region)
	if err != nil {
		return err
	}
	firstBoundary, err := compareVideoBoundary(ctx, processor.FFmpeg, output, "0.4", request.Before, request.Region, 0)
	if err != nil {
		return err
	}
	lastBoundary, err := compareVideoBoundary(ctx, processor.FFmpeg, output, "1.6", request.After, request.Region, 0)
	if err != nil {
		return err
	}
	firstInside, err := compareVideoBoundary(ctx, processor.FFmpeg, output, "0.4", request.Before, request.Region, 9)
	if err != nil {
		return err
	}
	lastInside, err := compareVideoBoundary(ctx, processor.FFmpeg, output, "1.6", request.After, request.Region, 9)
	if err != nil {
		return err
	}
	sceneDifference, err := videoSceneDifference(request.Before, request.After, request.Region)
	if err != nil {
		return err
	}
	return store.WriteJSON(filepath.Join(request.Directory, "video-result.json"), videoResult{
		Path: output, WAV: wavPath, HashBefore: fmt.Sprintf("%x", before), HashAfter: fmt.Sprintf("%x", after),
		AudioSeconds: capture.Snapshot().Duration, MP4AudioSeconds: seconds,
		VideoSeconds: videoSeconds, VideoFrames: videoFrames,
		BeforeError: firstError, AfterError: max(lastError, tailError), SceneDifference: sceneDifference,
		OutlineBoundaryError: max(firstBoundary, lastBoundary), OutlineAbsentFromSavedFrames: true,
		InnerOutlineBoundaryError: max(firstInside, lastInside), InnerOutlineExcluded: true,
		HarnessPID: request.HarnessPID, Region: request.Region,
		ResumeContextCancelled: resumeContextCancelled,
	})
}

func compareVideoBoundary(ctx context.Context, ffmpeg, path, at, reference string, region platform.ScreenRegion, inset int) (float64, error) {
	pixels, err := videoCommand(ctx, ffmpeg, "-v", "error", "-nostdin", "-ss", at, "-i", path,
		"-frames:v", "1", "-f", "rawvideo", "-pix_fmt", "rgb24", "pipe:1")
	if err != nil {
		return 0, err
	}
	original, err := readVideoReference(reference)
	if err != nil {
		return 0, err
	}
	if len(pixels) != region.Width*region.Height*3 {
		return 0, errors.New("a borda alterou as dimensões do vídeo")
	}
	var difference float64
	var count, purplePixels int
	for y := range region.Height {
		for x := range region.Width {
			if x < inset || y < inset || x >= region.Width-inset || y >= region.Height-inset {
				continue
			}
			if x != inset && y != inset && x != region.Width-inset-1 && y != region.Height-inset-1 {
				continue
			}
			index := (y*region.Width + x) * 3
			red, green, blue, _ := original.At(x, y).RGBA()
			for channel, expected := range []uint32{red >> 8, green >> 8, blue >> 8} {
				difference += math.Abs(float64(pixels[index+channel]) - float64(expected))
			}
			// Allow H.264 rounding, while rejecting the purple indicator from
			// this isolated gray/white test scene's saved perimeter.
			if math.Abs(float64(pixels[index])-166) < 24 &&
				math.Abs(float64(pixels[index+1])-92) < 24 && pixels[index+2] > 230 {
				purplePixels++
			}
			count++
		}
	}
	meanError := difference / float64(count*3)
	if meanError > 12 || purplePixels != 0 {
		return meanError, fmt.Errorf("a borda entrou nos pixels salvos em %s s: erro médio %.2f, pixels roxos %d", at, meanError, purplePixels)
	}
	return meanError, nil
}

func inspectVideoProof(ctx context.Context, ffprobe, path string, region platform.ScreenRegion) (float64, int, error) {
	payload, err := videoCommand(ctx, ffprobe, "-v", "error", "-select_streams", "v:0", "-count_frames",
		"-show_entries", "stream=duration,nb_read_frames,width,height", "-of", "json", path)
	if err != nil {
		return 0, 0, err
	}
	var result struct {
		Streams []struct {
			Duration      string
			NbReadFrames  string `json:"nb_read_frames"`
			Width, Height int
		}
	}
	if err := json.Unmarshal(payload, &result); err != nil || len(result.Streams) != 1 {
		return 0, 0, errors.New("o MP4 não contém uma única faixa de vídeo válida")
	}
	stream := result.Streams[0]
	seconds, durationErr := strconv.ParseFloat(stream.Duration, 64)
	frames, countErr := strconv.Atoi(stream.NbReadFrames)
	if durationErr != nil || countErr != nil || math.Abs(seconds-2.4) > 1.0/30 ||
		frames != 72 || stream.Width != region.Width || stream.Height != region.Height {
		return seconds, frames, fmt.Errorf("o vídeo perdeu duração, quadros ou dimensões: %.4f s, %d quadros, %d×%d",
			seconds, frames, stream.Width, stream.Height)
	}
	return seconds, frames, nil
}

func waitVideoResume(ctx context.Context, path string) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		var command struct{ Resume bool }
		if err := store.ReadJSON(path, &command); err == nil && command.Resume {
			return nil
		}
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-ticker.C:
		}
	}
}

func videoCommand(ctx context.Context, executable string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, executable, args...)
	platform.Hide(command)
	command.WaitDelay = 5 * time.Second
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("validar o MP4: %w", err)
	}
	return output, nil
}

func videoToneAmplitude(pcm []byte, at, frequency float64) float64 {
	first, count := int(at*audio.SampleRate), audio.SampleRate/5
	var cosine, sine float64
	for index := range count {
		sample := float64(int16(binary.LittleEndian.Uint16(pcm[(first+index)*2:]))) / 32768
		phase := 2 * math.Pi * frequency * float64(first+index) / audio.SampleRate
		cosine += sample * math.Cos(phase)
		sine += sample * math.Sin(phase)
	}
	return 2 * math.Hypot(cosine, sine) / float64(count)
}

func compareVideoFrame(ctx context.Context, ffmpeg, path, at, reference string, region platform.ScreenRegion) (float64, error) {
	pixels, err := videoCommand(ctx, ffmpeg, "-v", "error", "-nostdin", "-ss", at, "-i", path,
		"-frames:v", "1", "-f", "rawvideo", "-pix_fmt", "rgb24", "pipe:1")
	if err != nil {
		return 0, err
	}
	original, err := readVideoReference(reference)
	if err != nil {
		return 0, err
	}
	if original.Bounds().Size() != image.Pt(region.Width, region.Height) || len(pixels) != region.Width*region.Height*3 {
		return 0, errors.New("o vídeo não preservou o retângulo restrito do harness")
	}
	var difference float64
	for y := range region.Height {
		for x := range region.Width {
			red, green, blue, _ := original.At(x, y).RGBA()
			for channel, expected := range []uint32{red >> 8, green >> 8, blue >> 8} {
				difference += math.Abs(float64(pixels[(y*region.Width+x)*3+channel]) - float64(expected))
			}
		}
	}
	meanError := difference / float64(len(pixels))
	if meanError > 12 {
		return meanError, fmt.Errorf("a imagem não corresponde ao intervalo de áudio em %s s: diferença média %.2f", at, meanError)
	}
	return meanError, nil
}

func readVideoReference(path string) (image.Image, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	original, decodeErr := png.Decode(file)
	return original, errors.Join(decodeErr, file.Close())
}

func videoSceneDifference(before, after string, region platform.ScreenRegion) (float64, error) {
	first, err := readVideoReference(before)
	if err != nil {
		return 0, err
	}
	second, err := readVideoReference(after)
	if err != nil {
		return 0, err
	}
	if first.Bounds().Size() != image.Pt(region.Width, region.Height) || first.Bounds() != second.Bounds() {
		return 0, errors.New("as referências da mudança de cena não têm as dimensões esperadas")
	}
	var difference float64
	for y := range region.Height {
		for x := range region.Width {
			firstRed, firstGreen, firstBlue, _ := first.At(x, y).RGBA()
			secondRed, secondGreen, secondBlue, _ := second.At(x, y).RGBA()
			for channel, value := range []uint32{firstRed >> 8, firstGreen >> 8, firstBlue >> 8} {
				next := []uint32{secondRed >> 8, secondGreen >> 8, secondBlue >> 8}[channel]
				difference += math.Abs(float64(value) - float64(next))
			}
		}
	}
	mean := difference / float64(region.Width*region.Height*3)
	if mean < 30 {
		return mean, fmt.Errorf("as duas cenas de referência não diferem o suficiente: %.2f", mean)
	}
	return mean, nil
}

var _ recorder.Input = (*generatedVideoInput)(nil)
