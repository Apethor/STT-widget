//go:build integration

package video

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"ai-talker/internal/audio"
	"ai-talker/internal/platform"
	"ai-talker/internal/recorder"
)

func TestFinishJoinsPausedVideoAndMuxesAudioWithoutChangingOriginalWAV(t *testing.T) {
	processor, err := audio.New()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	recordings := filepath.Join(root, "gravacoes")
	if err := os.MkdirAll(recordings, 0o700); err != nil {
		t.Fatal(err)
	}
	audioPath := filepath.Join(recordings, "20261011-120000-mic.wav")
	pcm := make([]byte, audio.SampleRate*audio.SampleBytes)
	for sample := 0; sample < audio.SampleRate; sample++ {
		value := int16(3000 * math.Sin(2*math.Pi*440*float64(sample)/audio.SampleRate))
		binary.LittleEndian.PutUint16(pcm[sample*audio.SampleBytes:], uint16(value))
	}
	wav, err := audio.EncodeWAV(t.Context(), pcm)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(audioPath, wav, 0o600); err != nil {
		t.Fatal(err)
	}
	before := sha256.Sum256(wav)

	tempDir, err := os.MkdirTemp(recordings, ".ai-talker-video-")
	if err != nil {
		t.Fatal(err)
	}
	segments := []string{
		filepath.Join(tempDir, "segment-0001.mp4"),
		filepath.Join(tempDir, "segment-0002.mp4"),
	}
	for _, segment := range segments {
		command := exec.CommandContext(t.Context(), processor.FFmpeg,
			"-hide_banner", "-loglevel", "error",
			"-f", "lavfi", "-i", "color=c=blue:s=160x120:r=10:d=0.5",
			"-an", "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", segment,
		)
		platform.Hide(command)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("preparar trecho sintético de vídeo: %s: %v", output, err)
		}
	}
	engine := &Recorder{
		ffmpeg: processor.FFmpeg, directory: recordings, audioPath: audioPath,
		region:  platform.ScreenRegion{Width: 160, Height: 120},
		tempDir: tempDir, segments: segments,
	}
	output, err := engine.Finish(t.Context(), []recorder.Track{{
		ID: "microphone", Label: "Mic",
		Snapshot: recorder.Snapshot{
			Path: audioPath, SampleRate: audio.SampleRate, WrittenSamples: audio.SampleRate,
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(output) != "20261011-120000-video.mp4" {
		t.Fatalf("nome inesperado do vídeo combinado: %s", output)
	}
	if _, err := os.Stat(tempDir); !os.IsNotExist(err) {
		t.Fatalf("os segmentos temporários não foram removidos: %v", err)
	}

	command := exec.CommandContext(t.Context(), processor.FFprobe,
		"-v", "error", "-show_entries", "stream=codec_type:format=duration", "-of", "json", output,
	)
	platform.Hide(command)
	probe, err := command.Output()
	if err != nil {
		t.Fatalf("o MP4 final não abre no ffprobe: %v", err)
	}
	var result struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(probe, &result); err != nil {
		t.Fatal(err)
	}
	var hasVideo, hasAudio bool
	for _, stream := range result.Streams {
		hasVideo = hasVideo || stream.CodecType == "video"
		hasAudio = hasAudio || stream.CodecType == "audio"
	}
	duration, parseErr := strconv.ParseFloat(result.Format.Duration, 64)
	if !hasVideo || !hasAudio || parseErr != nil || math.Abs(duration-1) > 0.15 {
		t.Fatalf("MP4 final sem faixas válidas ou com duração errada: %+v, parse=%v", result, parseErr)
	}
	currentWAV, err := os.ReadFile(audioPath)
	if err != nil {
		t.Fatal(err)
	}
	if after := sha256.Sum256(currentWAV); after != before {
		t.Fatal("a mixagem do MP4 alterou o WAV original")
	}
}

type fixtureAudioInput struct{ receive func([]byte, error) }

func (*fixtureAudioInput) Open(context.Context, int, int) error { return nil }
func (i *fixtureAudioInput) Start(_ context.Context, receive func([]byte, error)) error {
	i.receive = receive
	return nil
}
func (*fixtureAudioInput) Close() error { return nil }

func fixtureTone(seconds, frequency float64) []byte {
	pcm := make([]byte, int(seconds*audio.SampleRate)*audio.SampleBytes)
	for sample := range len(pcm) / audio.SampleBytes {
		value := int16(7000 * math.Sin(2*math.Pi*frequency*float64(sample)/audio.SampleRate))
		binary.LittleEndian.PutUint16(pcm[sample*audio.SampleBytes:], uint16(value))
	}
	return pcm
}

func TestCaptureLifecycleAlignsColorAndToneChangesAtAudioPauseWithoutCuttingWAV(t *testing.T) {
	processor, err := audio.New()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	input := &fixtureAudioInput{}
	capture := recorder.NewWithInput(-1, root, input)
	audioPath, err := capture.Start(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = capture.Stop() })
	engine, err := NewRecorder(root, audioPath, platform.ScreenRegion{Width: 160, Height: 120})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Abort() })
	outline := &fakeVideoOutline{}
	engine.newOutline = func(context.Context, platform.ScreenRegion) (platform.VideoOutline, error) {
		return outline, nil
	}
	engine.SetAudioOrigin(capture.Snapshot())
	segment := 0
	engine.captureArgs = func(_ platform.ScreenRegion, destination string) []string {
		color := "red"
		if segment > 0 {
			color = "blue"
		}
		segment++
		return []string{
			"-hide_banner", "-loglevel", "error", "-nostats",
			"-progress", "pipe:1", "-stats_period", "0.05",
			"-re", "-f", "lavfi", "-i", "color=c=" + color + ":s=160x120:r=30",
			"-an", "-c:v", "libx264", "-preset", "ultrafast",
			"-tune", "zerolatency", "-pix_fmt", "yuv420p", destination,
		}
	}
	if err := engine.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	input.receive(fixtureTone(1, 440), nil)
	capture.Pause()
	paused := capture.Snapshot().Duration
	if err := engine.PauseAt(t.Context(), paused, time.Now()); err != nil {
		t.Fatal(err)
	}
	input.receive(fixtureTone(0.5, 1000), nil)
	capture.Resume()
	activeAt := time.Now()
	resumeCtx, resumeCancel := context.WithTimeout(t.Context(), 15*time.Second)
	t.Cleanup(resumeCancel)
	if err := engine.ResumeAt(resumeCtx, capture.Snapshot().Duration, activeAt); err != nil {
		t.Fatal(err)
	}
	resumeCancel()
	engine.mu.Lock()
	resumed := engine.active
	engine.mu.Unlock()
	select {
	case err := <-resumed.done:
		engine.mu.Lock()
		engine.active = nil
		engine.mu.Unlock()
		t.Fatalf("encerrar o contexto do comando de retomada matou a captura: %v", err)
	case <-time.After(200 * time.Millisecond):
	case <-t.Context().Done():
		t.Fatal(t.Context().Err())
	}
	input.receive(fixtureTone(1, 880), nil)
	if _, err := capture.Stop(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(audioPath)
	if err != nil {
		t.Fatal(err)
	}
	originalHash := sha256.Sum256(before)
	output, err := engine.Finish(t.Context(), []recorder.Track{{
		ID: "microphone", Snapshot: capture.Snapshot(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(outline.paused) != 2 || !outline.paused[0] || outline.paused[1] || outline.closed != 1 {
		t.Fatal("a borda não acompanhou a pausa, retomada e encerramento", outline)
	}
	for _, scenario := range []struct {
		name string
		at   string
		red  bool
	}{
		{name: "fala anterior à pausa", at: "0.2", red: true},
		{name: "fala depois da retomada", at: "1.2", red: false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			command := exec.CommandContext(t.Context(), processor.FFmpeg,
				"-v", "error", "-nostdin", "-ss", scenario.at, "-i", output,
				"-frames:v", "1", "-f", "rawvideo", "-pix_fmt", "rgb24", "pipe:1",
			)
			platform.Hide(command)
			pixels, err := command.Output()
			if err != nil || len(pixels) < 3 {
				t.Fatal("o MP4 perdeu o quadro do intervalo de fala", err)
			}
			if scenario.red && pixels[0] < 200 || !scenario.red && pixels[2] < 200 {
				t.Fatalf("a mudança de vídeo não seguiu a pausa do áudio: RGB=%v", pixels[:3])
			}
		})
	}
	command := exec.CommandContext(t.Context(), processor.FFmpeg,
		"-v", "error", "-nostdin", "-i", output, "-vn",
		"-ar", strconv.Itoa(audio.SampleRate), "-ac", "1", "-f", "s16le", "pipe:1",
	)
	platform.Hide(command)
	pcm, err := command.Output()
	if err != nil || math.Abs(float64(len(pcm)/2)/audio.SampleRate-2) > 0.07 {
		t.Fatal("o MP4 cortou áudio ao compensar o início ou a pausa do vídeo", err)
	}
	for _, tone := range []struct {
		at, frequency float64
	}{
		{at: 0.2, frequency: 440},
		{at: 1.2, frequency: 880},
	} {
		if fixtureAmplitude(pcm, tone.at, tone.frequency) < 0.15 {
			t.Fatalf("a faixa de áudio perdeu o tom em %.1f s", tone.at)
		}
	}
	after, err := os.ReadFile(audioPath)
	if err != nil || sha256.Sum256(after) != originalHash || capture.Snapshot().Duration != 2 {
		t.Fatal("o vídeo alterou o WAV original ou preservou amostras do intervalo pausado", err)
	}
}

func fixtureAmplitude(pcm []byte, at, frequency float64) float64 {
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
