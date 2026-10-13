package desktop

import (
	"math"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// Double-clicking the thumb restores the suggested value through the same
// preview and persistence callbacks as an ordinary slider change.
type resetSlider struct {
	widget.Slider
	initial float64
}

func newResetSlider(minimum, maximum, initial float64) *resetSlider {
	slider := &resetSlider{
		Slider:  widget.Slider{Min: minimum, Max: maximum, Step: 1},
		initial: initial,
	}
	slider.ExtendBaseWidget(slider)
	slider.SetValue(initial)
	return slider
}

func (s *resetSlider) DoubleTapped(event *fyne.PointEvent) {
	if s.Disabled() || !s.onThumb(event.Position) {
		return
	}
	s.SetValue(s.initial)
}

func (s *resetSlider) onThumb(position fyne.Position) bool {
	th := s.Theme()
	diameter := th.Size(theme.SizeNameInlineIcon) - 4
	padding := diameter/2 + th.Size(theme.SizeNameInnerPadding) - 1.5
	ratio := float32(0)
	if s.Max > s.Min {
		ratio = float32((s.Value - s.Min) / (s.Max - s.Min))
	}
	center := fyne.NewPos(padding+(s.Size().Width-padding*2)*ratio, s.Size().Height/2)
	if s.Orientation == widget.Vertical {
		center = fyne.NewPos(s.Size().Width/2, padding+(s.Size().Height-padding*2)*(1-ratio))
	}
	return math.Abs(float64(position.X-center.X)) <= float64(diameter/2+2) &&
		math.Abs(float64(position.Y-center.Y)) <= float64(diameter/2+2)
}
