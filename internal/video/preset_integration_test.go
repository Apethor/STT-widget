//go:build integration

package video

import (
	"encoding/json"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"ai-talker/internal/audio"
	"ai-talker/internal/platform"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

func TestVideoPresetsEncodeTextFramesWithExpectedResolutionRateAndAspect(t *testing.T) {
	processor, err := audio.New()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	if evidence := os.Getenv("AI_TALKER_VIDEO_PRESET_EVIDENCE"); evidence != "" {
		directory = evidence
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	region := platform.ScreenRegion{Width: 2560, Height: 1442}
	source := filepath.Join(directory, "preset-text-source.png")
	writePresetTextSample(t, source, region)
	for _, preset := range []Preset{PresetLow, PresetMedium, PresetHigh} {
		t.Run(string(preset), func(t *testing.T) {
			output := filepath.Join(directory, string(preset)+".mp4")
			captureArgs := capturePresetArguments(region, output, preset)
			input := slices.Index(captureArgs, "-i")
			args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
				"-loop", "1", "-framerate", strconv.Itoa(preset.frameRate()), "-i", source, "-t", "0.4"}
			args = append(args, captureArgs[input+2:]...)
			command := exec.CommandContext(t.Context(), processor.FFmpeg, args...)
			platform.Hide(command)
			if diagnostic, err := command.CombinedOutput(); err != nil {
				t.Fatalf("codificar a amostra de texto com %s: %s: %v", preset, diagnostic, err)
			}
			command = exec.CommandContext(t.Context(), processor.FFprobe, "-v", "error", "-select_streams", "v:0",
				"-show_entries", "stream=width,height,r_frame_rate,display_aspect_ratio,sample_aspect_ratio",
				"-of", "json", output)
			platform.Hide(command)
			payload, err := command.Output()
			if err != nil {
				t.Fatal(err)
			}
			var result struct {
				Streams []struct {
					Width, Height int
					FrameRate     string `json:"r_frame_rate"`
					DisplayAspect string `json:"display_aspect_ratio"`
					SampleAspect  string `json:"sample_aspect_ratio"`
				} `json:"streams"`
			}
			if err := json.Unmarshal(payload, &result); err != nil || len(result.Streams) != 1 {
				t.Fatal("o preset não produziu uma faixa de vídeo válida", string(payload), err)
			}
			stream := result.Streams[0]
			expected := preset.encoding(region)
			if stream.Width != expected.width || stream.Height != expected.height ||
				stream.FrameRate != strconv.Itoa(expected.fps)+"/1" || stream.DisplayAspect != "1280:721" {
				t.Fatalf("o preset perdeu dimensões, quadros ou a proporção original: %+v", stream)
			}
			frame := filepath.Join(directory, string(preset)+"-text.png")
			command = exec.CommandContext(t.Context(), processor.FFmpeg, "-v", "error", "-nostdin", "-y",
				"-i", output, "-frames:v", "1", frame)
			platform.Hide(command)
			if diagnostic, err := command.CombinedOutput(); err != nil {
				t.Fatalf("extrair a amostra para conferir a leitura: %s: %v", diagnostic, err)
			}
			if err := os.WriteFile(filepath.Join(directory, string(preset)+"-probe.json"), payload, 0o600); err != nil {
				t.Fatal(err)
			}
			t.Logf("%s: %dx%d, %d fps, DAR %s, quadro para conferência: %s",
				preset, stream.Width, stream.Height, expected.fps, stream.DisplayAspect, frame)
		})
	}
}

func writePresetTextSample(t *testing.T, path string, region platform.ScreenRegion) {
	t.Helper()
	pixels := image.NewRGBA(image.Rect(0, 0, region.Width, region.Height))
	draw.Draw(pixels, pixels.Bounds(), image.NewUniform(color.RGBA{248, 248, 250, 255}), image.Point{}, draw.Src)
	parsed, err := opentype.Parse(goregular.TTF)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		text string
		size float64
		y    int
	}{
		{text: "AI Talker — qualidade de captura de vídeo", size: 42, y: 90},
		{text: "A proporção da área selecionada é preservada.", size: 32, y: 170},
		{text: "Microfone e Sistema mantêm os arquivos WAV originais.", size: 32, y: 230},
		{text: "Média equilibra nitidez e tamanho. Alterações valem para a próxima captura.", size: 24, y: 310},
		{text: "Texto pequeno: AWS, Whisper, Nova 2.5 Sonic, transcrição e gravação.", size: 20, y: 370},
	} {
		face, err := opentype.NewFace(parsed, &opentype.FaceOptions{Size: tc.size, DPI: 72, Hinting: font.HintingFull})
		if err != nil {
			t.Fatal(err)
		}
		drawer := font.Drawer{Dst: pixels, Src: image.NewUniform(color.RGBA{36, 44, 58, 255}),
			Face: face, Dot: fixed.P(60, tc.y)}
		drawer.DrawString(tc.text)
		_ = face.Close()
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, pixels); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
