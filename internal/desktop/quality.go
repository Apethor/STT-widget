package desktop

import (
	"ai-talker/internal/config"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	fynedesktop "fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// These bars describe the selected transcription mode, not a measured score
// for the recording or the AWS connection.
type qualityIndicator struct {
	widget.BaseWidget
	hint     toolHint
	levels   int
	onTapped func()
}

func (d *desktop) newQualityIndicator(quality string) *qualityIndicator {
	indicator := &qualityIndicator{hint: toolHint{layer: d.hints}, onTapped: d.chooseTranscriptionModel}
	indicator.ExtendBaseWidget(indicator)
	indicator.setQuality(quality)
	return indicator
}

func (q *qualityIndicator) setQuality(quality string) {
	q.levels = 4
	text := uiText("Qualidade alta · Voxtral 24B")
	if quality == "fast" {
		q.levels = 2
		text = uiText("Modo rápido · Voxtral 3B")
	}
	if q.hint.text != text {
		q.hint.hide()
		q.hint.text = text
	}
	q.Refresh()
}

func (q *qualityIndicator) setWhisperModel(model string) {
	q.levels = 3
	switch model {
	case "tiny", "tiny-q5_1":
		q.levels = 1
	case "base", "base-q5_1":
		q.levels = 2
	case "large-v3", "large-v3-q8_0", "large-v3-turbo", "large-v3-turbo-q5_0":
		q.levels = 4
	}
	text := uiText("Whisper local · modelo ") + model + uiText(" · as barras indicam o tamanho do modelo")
	if q.hint.text != text {
		q.hint.hide()
		q.hint.text = text
	}
	q.Refresh()
}

func (q *qualityIndicator) setStream(quality string) {
	q.levels = 4
	text := uiText("Stream · Voxtral Small 24B · qualidade alta durante a gravação")
	if quality == "fast" {
		q.levels = 2
		text = uiText("Stream · Voxtral Mini 3B · modo rápido durante a gravação")
	}
	if q.hint.text != text {
		q.hint.hide()
		q.hint.text = text
	}
	q.Refresh()
}

func (q *qualityIndicator) setCloudModel(model config.CloudModel, stream bool) {
	if model.ID == config.VoxtralSmall || model.ID == config.VoxtralMini {
		quality := "quality"
		if model.ID == config.VoxtralMini {
			quality = "fast"
		}
		if stream {
			q.setStream(quality)
		} else {
			q.setQuality(quality)
		}
	} else {
		q.levels = 3
		q.hint.hide()
		q.hint.text = model.Name
		if stream {
			q.hint.text = "Stream · " + q.hint.text
		}
		q.Refresh()
	}
	q.hint.text += uiText(" · clicar para escolher o modelo")
}

func (q *qualityIndicator) Tapped(*fyne.PointEvent) {
	q.hint.hide()
	if q.onTapped != nil {
		q.onTapped()
	}
}

func (q *qualityIndicator) AccessibilityLabel() string             { return q.hint.text }
func (q *qualityIndicator) AccessibilityRole() fyne.AccessibleRole { return fyne.AccessibleRoleButton }
func (q *qualityIndicator) MouseIn(*fynedesktop.MouseEvent)        { q.hint.show(q) }
func (q *qualityIndicator) MouseMoved(*fynedesktop.MouseEvent)     {}
func (q *qualityIndicator) MouseOut()                              { q.hint.hide() }

func (q *qualityIndicator) CreateRenderer() fyne.WidgetRenderer {
	r := &qualityRenderer{indicator: q}
	for range 4 {
		bar := canvas.NewRectangle(q.Theme().Color(accentColor, theme.VariantDark))
		bar.CornerRadius = 1
		r.bars = append(r.bars, bar)
		r.objects = append(r.objects, bar)
	}
	r.applyColors()
	return r
}

type qualityRenderer struct {
	indicator *qualityIndicator
	bars      []*canvas.Rectangle
	objects   []fyne.CanvasObject
}

func (r *qualityRenderer) MinSize() fyne.Size { return fyne.NewSize(25, 20) }

func (r *qualityRenderer) Layout(size fyne.Size) {
	left, bottom := (size.Width-25)/2, (size.Height+16)/2
	for index, bar := range r.bars {
		height := float32(index+1) * 4
		bar.Move(fyne.NewPos(left+float32(index)*7, bottom-height))
		bar.Resize(fyne.NewSize(4, height))
	}
}

func (r *qualityRenderer) applyColors() {
	for index, bar := range r.bars {
		role := borderColor
		if index < r.indicator.levels {
			role = accentColor
		}
		bar.FillColor = r.indicator.Theme().Color(role, theme.VariantDark)
		bar.Refresh()
	}
}

func (r *qualityRenderer) Refresh() {
	r.applyColors()
	canvas.Refresh(r.indicator)
}

func (r *qualityRenderer) Objects() []fyne.CanvasObject { return r.objects }
func (r *qualityRenderer) Destroy()                     {}
