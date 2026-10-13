package video

import (
	"path/filepath"
	"slices"
	"testing"

	"ai-talker/internal/platform"
)

func TestVideoPresetPreservesRegionAndDoesNotUpscale(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name          string
		preset        Preset
		width, height int
		wantWidth     int
		wantHeight    int
		wantFPS       int
		wantCRF       int
	}{
		{name: "baixa paisagem", preset: PresetLow, width: 3840, height: 2160, wantWidth: 1280, wantHeight: 720, wantFPS: 15, wantCRF: 30},
		{name: "média paisagem", preset: PresetMedium, width: 3840, height: 2160, wantWidth: 1920, wantHeight: 1080, wantFPS: 30, wantCRF: 23},
		{name: "alta original", preset: PresetHigh, width: 3840, height: 2160, wantWidth: 3840, wantHeight: 2160, wantFPS: 30, wantCRF: 18},
		{name: "baixa retrato", preset: PresetLow, width: 2160, height: 3840, wantWidth: 720, wantHeight: 1280, wantFPS: 15, wantCRF: 30},
		{name: "média retrato", preset: PresetMedium, width: 2160, height: 3840, wantWidth: 1080, wantHeight: 1920, wantFPS: 30, wantCRF: 23},
		{name: "área menor", preset: PresetLow, width: 640, height: 480, wantWidth: 640, wantHeight: 480, wantFPS: 15, wantCRF: 30},
		{name: "padrão", width: 640, height: 480, wantWidth: 640, wantHeight: 480, wantFPS: 30, wantCRF: 23},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := tc.preset.encoding(platform.ScreenRegion{Width: tc.width, Height: tc.height})
			if got.width != tc.wantWidth || got.height != tc.wantHeight || got.fps != tc.wantFPS || got.crf != tc.wantCRF {
				t.Fatalf("parâmetros de qualidade inesperados: %+v", got)
			}
		})
	}
}

func TestVideoPresetScalesCapturedRectangleAndPreservesDisplayAspect(t *testing.T) {
	t.Parallel()
	region := platform.ScreenRegion{X: -3840, Y: 100, Width: 3840, Height: 2160}
	args := capturePresetArguments(region, "capture.mp4", PresetLow)
	for _, value := range []string{"3840x2160", "-3840", "100", "scale=1280:720:flags=lanczos,setsar=1/1:max=65535", "15", "30"} {
		if !slices.Contains(args, value) {
			t.Fatalf("o preset alterou o recorte de entrada ou perdeu a qualidade: %v", args)
		}
	}
	irregular := platform.ScreenRegion{Width: 2560, Height: 1442}
	filter := PresetLow.encoding(irregular).scaleFilter(irregular)
	if filter != "scale=1280:720:flags=lanczos,setsar=720/721:max=65535" {
		t.Fatal("a escala perdeu a proporção do recorte original", filter)
	}
}

func TestVideoPresetIsFixedAfterCaptureBegins(t *testing.T) {
	t.Parallel()
	engine := &Recorder{}
	if err := engine.SetPreset(PresetLow); err != nil || engine.preset != PresetLow {
		t.Fatal("não foi possível escolher Baixa antes de gravar", err)
	}
	engine.tempDir = filepath.Join(t.TempDir(), ".ai-talker-video-test")
	if err := engine.SetPreset(PresetHigh); err == nil || engine.preset != PresetLow {
		t.Fatal("a mudança de preferência alterou o vídeo em andamento")
	}
	engine.tempDir = ""
	if err := engine.SetPreset(Preset("invalid")); err == nil {
		t.Fatal("uma qualidade inválida foi aceita")
	}
	if err := engine.SetPreset(""); err != nil || engine.preset != PresetMedium {
		t.Fatal("preferência antiga sem preset não escolheu Média", err)
	}
}
