//go:build ignore

// Run from the repository root: go run -mod=readonly ./scripts/generate-icon.go.
// The renderer is already a pinned dependency of the desktop.
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	"github.com/fyne-io/oksvg"
	"github.com/srwiley/rasterx"
	"golang.org/x/image/draw"
)

func main() {
	if err := generate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func generate() error {
	directory := filepath.Join("internal", "branding")
	source, err := os.ReadFile(filepath.Join(directory, "ai-talker.svg"))
	if err != nil {
		return fmt.Errorf("read icon source: %w", err)
	}
	// Rasterize at the SVG's native size. Scaling its paths in this renderer
	// leaves stroke widths unchanged, which obscures the small Windows icons.
	pixels, err := rasterize(source, 256)
	if err != nil {
		return err
	}
	desktop, err := encodePNG(pixels)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(directory, "ai-talker.png"), desktop, 0o644); err != nil {
		return fmt.Errorf("save desktop icon: %w", err)
	}

	sizes := []int{16, 24, 32, 48, 64, 128, 256}
	// ICONDIR followed by ICONDIRENTRY records and their PNG payloads.
	icon := make([]byte, 6+16*len(sizes))
	binary.LittleEndian.PutUint16(icon[2:4], 1)
	binary.LittleEndian.PutUint16(icon[4:6], uint16(len(sizes)))
	for index, size := range sizes {
		small := image.NewRGBA(image.Rect(0, 0, size, size))
		draw.CatmullRom.Scale(small, small.Bounds(), pixels, pixels.Bounds(), draw.Src, nil)
		data, err := encodePNG(small)
		if err != nil {
			return err
		}
		entry := icon[6+16*index : 6+16*(index+1)]
		if size < 256 {
			entry[0], entry[1] = byte(size), byte(size)
		} // A zero dimension denotes 256 pixels in ICO.
		binary.LittleEndian.PutUint16(entry[4:6], 1)
		binary.LittleEndian.PutUint16(entry[6:8], 32)
		binary.LittleEndian.PutUint32(entry[8:12], uint32(len(data)))
		binary.LittleEndian.PutUint32(entry[12:16], uint32(len(icon)))
		icon = append(icon, data...)
	}
	if err := os.WriteFile(filepath.Join(directory, "ai-talker.ico"), icon, 0o644); err != nil {
		return fmt.Errorf("save Windows icon: %w", err)
	}
	for _, variant := range []struct {
		name   string
		colors []string
	}{
		{"light", []string{"#0f6b50", "#315fc4", "#42a77f", "#7298ea", "#effff4", "#ffffff", "#8ce7b0", "#c3d3fa"}},
		{"dark", []string{"#0f6b50", "#2a3443", "#42a77f", "#718198", "#effff4", "#ecf1f8", "#8ce7b0", "#acb9cc"}},
	} {
		artwork := strings.NewReplacer(variant.colors...).Replace(string(source))
		rendered, err := rasterize([]byte(artwork), 256)
		if err != nil {
			return fmt.Errorf("render %s title: %w", variant.name, err)
		}
		data, err := encodePNG(rendered)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(directory, "ai-talker-title-"+variant.name+".png"), data, 0o644); err != nil {
			return fmt.Errorf("save %s title: %w", variant.name, err)
		}
	}
	return nil
}

func rasterize(source []byte, size int) (image.Image, error) {
	icon, err := oksvg.ReadIconStream(bytes.NewReader(source), oksvg.StrictErrorMode)
	if err != nil {
		return nil, fmt.Errorf("parse icon: %w", err)
	}
	icon.SetTarget(0, 0, float64(size), float64(size))
	pixels := image.NewRGBA(image.Rect(0, 0, size, size))
	scanner := rasterx.NewScannerGV(size, size, pixels, pixels.Bounds())
	icon.Draw(rasterx.NewDasher(size, size, scanner), 1)
	return pixels, nil
}

func encodePNG(pixels image.Image) ([]byte, error) {
	var output bytes.Buffer
	if err := png.Encode(&output, pixels); err != nil {
		return nil, fmt.Errorf("encode icon: %w", err)
	}
	return output.Bytes(), nil
}
