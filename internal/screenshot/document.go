// Package screenshot owns captured pixels, annotations and PNG persistence.
package screenshot

import (
	"errors"
	"image"
	"image/color"
	"image/draw"
	"math"
	"strings"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

const (
	FontSans = "Sans"
	FontBold = "Sans negrito"
	FontMono = "Monoespaçada"
)

type Point struct{ X, Y float64 }

type operation struct {
	points []Point
	width  float64
	color  color.NRGBA
	erase  bool
	text   string
	font   string
	size   float64
}

// Document belongs to the editor's UI thread. Snapshot returns independent
// pixels for background saves, so drawing never races with PNG encoding.
type Document struct {
	original *image.RGBA
	pixels   *image.RGBA
	undo     []operation
	redo     []operation
	stroke   *operation
	revision uint64
}

func New(source image.Image) (*Document, error) {
	if source == nil || source.Bounds().Empty() {
		return nil, errors.New("a captura não contém pixels")
	}
	bounds := image.Rect(0, 0, source.Bounds().Dx(), source.Bounds().Dy())
	original := image.NewRGBA(bounds)
	draw.Draw(original, bounds, source, source.Bounds().Min, draw.Src)
	d := &Document{original: original}
	d.resetPixels()
	return d, nil
}

func (d *Document) Image() image.Image { return d.pixels }
func (d *Document) Revision() uint64   { return d.revision }
func (d *Document) CanUndo() bool      { return len(d.undo) > 0 }
func (d *Document) CanRedo() bool      { return len(d.redo) > 0 }

func (d *Document) Snapshot() *image.RGBA {
	result := image.NewRGBA(d.pixels.Bounds())
	copy(result.Pix, d.pixels.Pix)
	return result
}

func (d *Document) BeginStroke(point Point, width float64, ink color.NRGBA, erase bool) {
	d.EndStroke()
	d.stroke = &operation{points: []Point{point}, width: max(1, min(width, 100)), color: ink, erase: erase}
	d.disc(point, d.stroke)
}

func (d *Document) ExtendStroke(point Point) {
	if d.stroke == nil {
		return
	}
	previous := d.stroke.points[len(d.stroke.points)-1]
	if previous == point {
		return
	}
	d.stroke.points = append(d.stroke.points, point)
	d.segment(previous, point, d.stroke)
}

func (d *Document) EndStroke() {
	if d.stroke != nil {
		d.undo = append(d.undo, *d.stroke)
		d.redo = nil
		d.stroke = nil
		d.revision++
	}
}

func (d *Document) AddText(point Point, text, family string, size float64, ink color.NRGBA) error {
	d.EndStroke()
	if strings.TrimSpace(text) == "" {
		return nil
	}
	op := operation{points: []Point{point}, text: text, font: family, size: max(8, min(size, 160)), color: ink}
	if err := d.drawText(&op); err != nil {
		return err
	}
	d.undo = append(d.undo, op)
	d.redo = nil
	d.revision++
	return nil
}

func (d *Document) Undo() {
	d.EndStroke()
	if !d.CanUndo() {
		return
	}
	d.redo = append(d.redo, d.undo[len(d.undo)-1])
	d.undo = d.undo[:len(d.undo)-1]
	d.replay()
	d.revision++
}

func (d *Document) Redo() {
	d.EndStroke()
	if !d.CanRedo() {
		return
	}
	d.undo = append(d.undo, d.redo[len(d.redo)-1])
	d.redo = d.redo[:len(d.redo)-1]
	d.replay()
	d.revision++
}

func (d *Document) resetPixels() {
	d.pixels = image.NewRGBA(d.original.Bounds())
	copy(d.pixels.Pix, d.original.Pix)
}

func (d *Document) replay() {
	d.resetPixels()
	for i := range d.undo {
		op := &d.undo[i]
		if op.text != "" {
			// Font choices and sizes were validated before entering history.
			_ = d.drawText(op)
			continue
		}
		d.disc(op.points[0], op)
		for j := 1; j < len(op.points); j++ {
			d.segment(op.points[j-1], op.points[j], op)
		}
	}
}

func (d *Document) segment(from, to Point, op *operation) {
	distance := math.Hypot(to.X-from.X, to.Y-from.Y)
	steps := max(1, int(math.Ceil(distance*2)))
	for i := 1; i <= steps; i++ {
		fraction := float64(i) / float64(steps)
		d.disc(Point{from.X + (to.X-from.X)*fraction, from.Y + (to.Y-from.Y)*fraction}, op)
	}
}

func (d *Document) disc(point Point, op *operation) {
	radius := op.width / 2
	bounds := d.pixels.Bounds()
	for y := max(bounds.Min.Y, int(math.Floor(point.Y-radius))); y < min(bounds.Max.Y, int(math.Ceil(point.Y+radius))); y++ {
		for x := max(bounds.Min.X, int(math.Floor(point.X-radius))); x < min(bounds.Max.X, int(math.Ceil(point.X+radius))); x++ {
			if math.Hypot(float64(x)+0.5-point.X, float64(y)+0.5-point.Y) > radius {
				continue
			}
			if op.erase {
				d.pixels.SetRGBA(x, y, d.original.RGBAAt(x, y))
			} else {
				d.pixels.Set(x, y, op.color)
			}
		}
	}
}

func (d *Document) drawText(op *operation) error {
	payload := goregular.TTF
	switch op.font {
	case FontSans:
	case FontBold:
		payload = gobold.TTF
	case FontMono:
		payload = gomono.TTF
	default:
		return errors.New("fonte de texto desconhecida")
	}
	parsed, err := opentype.Parse(payload)
	if err != nil {
		return err
	}
	face, err := opentype.NewFace(parsed, &opentype.FaceOptions{Size: op.size, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return err
	}
	defer face.Close()
	drawer := font.Drawer{Dst: d.pixels, Src: image.NewUniform(op.color), Face: face}
	start := op.points[0]
	baseline := fixed.Int26_6(math.Round(start.Y*64)) + face.Metrics().Ascent
	for _, line := range strings.Split(strings.ReplaceAll(op.text, "\r\n", "\n"), "\n") {
		drawer.Dot = fixed.Point26_6{X: fixed.Int26_6(math.Round(start.X * 64)), Y: baseline}
		drawer.DrawString(line)
		baseline += face.Metrics().Height
	}
	return nil
}
