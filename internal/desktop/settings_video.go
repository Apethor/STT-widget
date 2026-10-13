package desktop

import (
	"ai-talker/internal/config"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

const (
	videoLowChoice    = "Baixa"
	videoMediumChoice = "Média"
	videoHighChoice   = "Alta"
)

func (d *desktop) buildVideoSettings(settings config.Settings) (fyne.CanvasObject, *widget.Select) {
	description := widget.NewLabel("")
	description.Wrapping = fyne.TextWrapWord
	preset := widget.NewSelect([]string{videoLowChoice, videoMediumChoice, videoHighChoice}, func(choice string) {
		switch choice {
		case videoLowChoice:
			description.SetText("Prioriza arquivos menores. Até 1280 pixels no lado maior, 15 quadros por segundo e compressão maior.")
		case videoHighChoice:
			description.SetText("Prioriza nitidez. Mantém a resolução da área selecionada, com 30 quadros por segundo e compressão menor.")
		default:
			description.SetText("Equilibra tamanho e nitidez. Até 1920 pixels no lado maior, 30 quadros por segundo e compressão moderada.")
		}
	})
	setUISelected(preset, videoMediumChoice)
	switch settings.VideoQuality() {
	case "low":
		setUISelected(preset, videoLowChoice)
	case "high":
		setUISelected(preset, videoHighChoice)
	}
	timing := widget.NewLabel("A qualidade fica definida ao começar cada gravação. Alterações durante a captura valem para o próximo vídeo.")
	timing.Wrapping = fyne.TextWrapWord
	proportion := widget.NewLabel("A proporção da área é preservada e áreas menores não são ampliadas. O tamanho do arquivo também depende do conteúdo e da duração.")
	proportion.Wrapping = fyne.TextWrapWord
	return container.NewVBox(
		settingLabel(uiText("Qualidade da captura de vídeo")),
		preset, d.smallText(description), d.smallText(timing), d.smallText(proportion),
	), preset
}

func videoPresetValue(choice string) string {
	switch choice {
	case videoLowChoice:
		return "low"
	case videoHighChoice:
		return "high"
	default:
		return "medium"
	}
}
