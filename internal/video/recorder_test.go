package video

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ai-talker/internal/platform"
	"ai-talker/internal/recorder"
)

type fakeVideoOutline struct {
	paused []bool
	closed int
}

func (o *fakeVideoOutline) SetPaused(paused bool) error {
	o.paused = append(o.paused, paused)
	return nil
}

func (o *fakeVideoOutline) Close() error {
	o.closed++
	return nil
}

func TestNormalizeRegionKeepsVirtualDesktopOriginAndEncodableDimensions(t *testing.T) {
	t.Parallel()
	region, err := NormalizeRegion(platform.ScreenRegion{X: -1920, Y: -120, Width: 641, Height: 481})
	if err != nil || region.X != -1920 || region.Y != -120 || region.Width != 640 || region.Height != 480 {
		t.Fatalf("a área selecionada perdeu posição ou alinhamento do vídeo: %+v %v", region, err)
	}
	for _, invalid := range []platform.ScreenRegion{
		{Width: 2, Height: 2},
		{Width: maximumRegionSide + 2, Height: 200},
		{X: maximumRegionSide + 1, Width: 100, Height: 100},
	} {
		if _, err := NormalizeRegion(invalid); err == nil {
			t.Fatalf("uma área inválida foi aceita: %+v", invalid)
		}
	}
}

func TestCaptureArgumentsUseSelectedRegionAndH264MP4(t *testing.T) {
	t.Parallel()
	region := platform.ScreenRegion{X: -900, Y: 40, Width: 800, Height: 600}
	args := captureArguments(region, "capture.mp4")
	for _, expected := range []string{
		"gdigrab", "-offset_x", "-900", "-offset_y", "40",
		"-video_size", "800x600", "desktop", "libx264", "yuv420p", "capture.mp4",
	} {
		if !slices.Contains(args, expected) {
			t.Fatalf("o comando não captura a região/codificação escolhida: %q", args)
		}
	}
}

func TestResumeRejectsVideoThatHasNotStarted(t *testing.T) {
	t.Parallel()
	engine := &Recorder{}
	if err := engine.Resume(t.Context()); err == nil {
		t.Fatal("retomar tentou abrir um codificador antes do início da captura")
	}
	if err := engine.ResumeAt(t.Context(), 0, time.Now()); err == nil {
		t.Fatal("retomar no ponto do áudio foi aceito sem uma captura iniciada")
	}
}

func TestFailedStartDoesNotLeaveAResumableVideoSession(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	engine := &Recorder{
		directory: directory,
		ffmpeg:    filepath.Join(directory, "codificador-ausente.exe"),
		region:    platform.ScreenRegion{Width: 160, Height: 120},
	}
	outline := &fakeVideoOutline{}
	engine.newOutline = func(context.Context, platform.ScreenRegion) (platform.VideoOutline, error) {
		return outline, nil
	}
	if err := engine.Start(t.Context()); err == nil {
		t.Fatal("o início sem codificador não retornou erro")
	}
	if engine.captureCtx != nil || engine.tempDir != "" || engine.active != nil {
		t.Fatal("a falha de início deixou recursos ou um contexto de captura ativo")
	}
	if outline.closed != 1 || engine.outline != nil {
		t.Fatal("a falha de início deixou a borda da área aberta")
	}
	if err := engine.ResumeAt(t.Context(), 0, time.Now()); err == nil ||
		!strings.Contains(err.Error(), "ainda não começou") {
		t.Fatal("retomar após falhar tentou escrever um trecho fora da pasta reservada:", err)
	}
	files, err := os.ReadDir(directory)
	if err != nil || len(files) != 0 {
		t.Fatal("a falha de início deixou arquivos temporários", err)
	}
}

func TestAudioMixFilterUsesIndependentTrackOffsetsWithoutTouchingWAVs(t *testing.T) {
	t.Parallel()
	filter := audioMixFilter([]AudioTrack{
		{Path: "mic.wav", Offset: 0},
		{Path: "system.wav", Offset: 0.25},
	})
	if !strings.Contains(filter, "adelay=0S:all=1[a0]") ||
		!strings.Contains(filter, "adelay=12000S:all=1[a1]") ||
		!strings.Contains(filter, "amix=inputs=2:duration=longest") {
		t.Fatalf("a mistura do MP4 não preservou os deslocamentos: %s", filter)
	}
}

func TestAudioTracksRejectMissingOrDuplicateSources(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "audio.wav")
	if err := os.WriteFile(path, []byte("synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	track := recorder.Track{ID: "microphone", Offset: 0, Snapshot: recorder.Snapshot{
		Path: path, WrittenSamples: 1,
	}}
	if _, err := validatedAudioTracks(nil); err == nil {
		t.Fatal("um vídeo sem áudio foi aceito")
	}
	if _, err := validatedAudioTracks([]recorder.Track{track, track}); err == nil {
		t.Fatal("duas referências iguais ao mesmo áudio foram aceitas")
	}
	tracks, err := validatedAudioTracks([]recorder.Track{track})
	if err != nil || len(tracks) != 1 || tracks[0].Path != path {
		t.Fatalf("uma fonte válida foi recusada: %+v %v", tracks, err)
	}
}

func TestReserveOutputNeverOverwritesExistingVideo(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	audioPath := filepath.Join(directory, "20261011-120000-mic.wav")
	first, err := reserveOutput(audioPath)
	if err != nil {
		t.Fatal(err)
	}
	second, err := reserveOutput(audioPath)
	if err != nil || first == second ||
		filepath.Base(first) != "20261011-120000-video.mp4" ||
		filepath.Base(second) != "20261011-120000-video-2.mp4" {
		t.Fatalf("a gravação não reservou um nome novo: %q %q %v", first, second, err)
	}
}

func TestReserveOutputUsesVideoFolderWithoutChangingOriginalAudioPath(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	audioPath := filepath.Join(root, "gravacoes", "2026-10-12", "20261012-120000-mic.wav")
	videos := filepath.Join(root, "videos", "2026-10-12")
	if err := os.MkdirAll(videos, 0o700); err != nil {
		t.Fatal(err)
	}
	videoPath, err := reserveOutputIn(videos, audioPath)
	if err != nil || filepath.Dir(videoPath) != videos || filepath.Base(videoPath) != "20261012-120000-video.mp4" {
		t.Fatal("o novo vídeo não foi salvo na pasta de capturas do dia", videoPath, err)
	}
	if filepath.Dir(audioPath) == filepath.Dir(videoPath) {
		t.Fatal("o MP4 foi reservado junto dos áudios")
	}
}

func TestVideoAbortRemovesPausedOutline(t *testing.T) {
	t.Parallel()
	outline := &fakeVideoOutline{}
	engine := &Recorder{outline: outline}
	if err := engine.Abort(); err != nil {
		t.Fatal(err)
	}
	if outline.closed != 1 || engine.outline != nil {
		t.Fatal("descartar preservou a borda da gravação")
	}
	if err := engine.Abort(); err != nil || outline.closed != 1 {
		t.Fatal("remover a borda não foi idempotente", err)
	}
}

func TestVideoFinishFailureRemovesOutlineBeforeAudioValidation(t *testing.T) {
	t.Parallel()
	outline := &fakeVideoOutline{}
	engine := &Recorder{outline: outline}
	if _, err := engine.Finish(t.Context(), nil); err == nil {
		t.Fatal("vídeo sem faixa de áudio deveria falhar")
	}
	if outline.closed != 1 || engine.outline != nil {
		t.Fatal("a falha ao finalizar deixou a borda aberta")
	}
}
