package video

import (
	"errors"
	"fmt"
	"math"
	"strconv"

	"ai-talker/internal/platform"
)

// Preset chooses capture dimensions, frame rate and compression for one video.
// A Recorder freezes this choice when Start begins.
type Preset string

const (
	PresetLow    Preset = "low"
	PresetMedium Preset = "medium"
	PresetHigh   Preset = "high"
)

func (p Preset) normalized() Preset {
	if p == "" {
		return PresetMedium
	}
	return p
}

func (p Preset) validate() error {
	switch p.normalized() {
	case PresetLow, PresetMedium, PresetHigh:
		return nil
	default:
		return errors.New("escolha a qualidade de vídeo Baixa, Média ou Alta")
	}
}

type encoding struct {
	width, height int
	fps, crf      int
}

func (p Preset) encoding(region platform.ScreenRegion) encoding {
	result := encoding{width: region.Width, height: region.Height, fps: 30, crf: 23}
	longest := 1920
	switch p.normalized() {
	case PresetLow:
		result.fps, result.crf, longest = 15, 30, 1280
	case PresetHigh:
		result.crf, longest = 18, 0
	}
	if longest == 0 || max(region.Width, region.Height) <= longest {
		return result
	}
	ratio := float64(longest) / float64(max(region.Width, region.Height))
	result.width = max(2, int(math.Floor(float64(region.Width)*ratio/2))*2)
	result.height = max(2, int(math.Floor(float64(region.Height)*ratio/2))*2)
	return result
}

func (p Preset) frameRate() int {
	if p.normalized() == PresetLow {
		return 15
	}
	return 30
}

func (e encoding) codecArguments() []string {
	return []string{
		"-c:v", "libx264", "-preset", "ultrafast", "-crf", strconv.Itoa(e.crf),
		"-tune", "zerolatency", "-pix_fmt", "yuv420p", "-movflags", "+faststart",
	}
}

func (e encoding) scaleFilter(region platform.ScreenRegion) string {
	if e.width == region.Width && e.height == region.Height {
		return "setsar=1"
	}
	numerator, denominator := region.Width*e.height, region.Height*e.width
	a, b := numerator, denominator
	for b != 0 {
		a, b = b, a%b
	}
	numerator, denominator = numerator/a, denominator/a
	// Desktop pixels are square, while gdigrab may report an unspecified
	// sample aspect. Restore the exact display aspect after even rounding.
	return fmt.Sprintf("scale=%d:%d:flags=lanczos,setsar=%d/%d:max=65535",
		e.width, e.height, numerator, denominator)
}

func capturePresetArguments(region platform.ScreenRegion, path string, preset Preset) []string {
	quality := preset.encoding(region)
	args := []string{
		"-hide_banner", "-loglevel", "error", "-nostats", "-progress", "pipe:1", "-stats_period", "0.05",
		"-f", "gdigrab", "-framerate", strconv.Itoa(quality.fps), "-draw_mouse", "1",
		"-offset_x", strconv.Itoa(region.X), "-offset_y", strconv.Itoa(region.Y),
		"-video_size", fmt.Sprintf("%dx%d", region.Width, region.Height),
		"-i", "desktop", "-an",
	}
	args = append(args, "-vf", quality.scaleFilter(region))
	args = append(args, quality.codecArguments()...)
	return append(args, path)
}
