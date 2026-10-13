package desktop

import (
	"ai-talker/internal/processing"

	"fyne.io/fyne/v2/widget"
)

const (
	automaticReasoning = "Automático · padrão do modelo"
	lowReasoning       = "Baixo · mais rápido"
	mediumReasoning    = "Médio"
	highReasoning      = "Alto · mais análise"
)

func reasoningChoice(effort string) string {
	switch effort {
	case "low":
		return lowReasoning
	case "medium":
		return mediumReasoning
	case "high":
		return highReasoning
	default:
		return automaticReasoning
	}
}

func selectedReasoning(choice string) string {
	switch choice {
	case lowReasoning:
		return "low"
	case mediumReasoning:
		return "medium"
	case highReasoning:
		return "high"
	default:
		return ""
	}
}

func reasoningSelect(modelID, effort string) *widget.Select {
	selectControl := widget.NewSelect([]string{
		automaticReasoning, lowReasoning, mediumReasoning, highReasoning,
	}, nil)
	setUISelected(selectControl, reasoningChoice(effort))
	syncReasoningSelect(selectControl, modelID)
	return selectControl
}

func syncReasoningSelect(control *widget.Select, modelID string) {
	setEnabled(control, processing.SupportsReasoning(modelID))
	if control.Disabled() {
		setUISelected(control, automaticReasoning)
	}
}
