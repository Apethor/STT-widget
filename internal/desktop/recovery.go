package desktop

import (
	"fmt"
	"slices"
	"strings"

	"ai-talker/internal/config"
	"ai-talker/internal/live"
	"ai-talker/internal/transcript"
)

func (d *desktop) syncConcurrency(settings config.Settings) {
	if runner, ok := d.runner.(interface{ SetConcurrency(int) error }); ok {
		// Settings.Validate already restricts this range.
		if err := runner.SetConcurrency(settings.Parallelism()); err != nil {
			return
		}
	}
}

func entryEngine(entry live.Entry, defaultEngine string) string {
	var engines []string
	for _, part := range entry.Parts {
		if part.Engine != "" && !slices.Contains(engines, part.Engine) {
			engines = append(engines, part.Engine)
		}
	}
	if len(engines) == 0 {
		return defaultEngine
	}
	slices.Sort(engines)
	return strings.Join(engines, " + ")
}

func shiftedParts(parts []transcript.Part, offset float64) []transcript.Part {
	result := slices.Clone(parts)
	for index := range result {
		result[index].Start += offset
		result[index].Children = shiftedParts(result[index].Children, offset)
	}
	return result
}

func resultReviewNotice(result transcript.Result) string {
	var reasons []string
	for _, part := range result.Parts {
		if part.ReviewReason != "" && !slices.Contains(reasons, part.ReviewReason) {
			reasons = append(reasons, part.ReviewReason)
		}
		if len(reasons) >= 3 {
			break
		}
	}
	if len(reasons) == 0 {
		return "Compare o texto com o áudio. Há trechos pendentes ou que precisam de conferência."
	}
	return shortMessage(strings.Join(reasons, " "))
}

func (d *desktop) syncTranscriptionDetails(doc *document) {
	if doc.transcriptName.hint == nil {
		return
	}
	var lines []string
	var describe func([]transcript.Part, string)
	describe = func(parts []transcript.Part, prefix string) {
		for index, part := range parts {
			number := fmt.Sprintf("%s%d", prefix, index+1)
			if len(part.Children) > 0 {
				describe(part.Children, number+".")
				continue
			}
			engine := part.Engine
			if engine == "" && doc.lastResult != nil {
				engine = doc.lastResult.ModelID
			}
			if model, ok := config.CloudModelByID(engine); ok {
				engine = model.Name
			}
			if part.Error != "" || part.NeedsReview {
				engine += uiText(" · para conferir")
			} else if !part.DetectedSpeech && part.Text == "" {
				engine = uiText("sem fala")
			}
			if part.Recovery != nil {
				switch part.Recovery.Status {
				case "recognized":
					engine += uiText(" · recuperado pelo Whisper")
				case "failed":
					engine += uiText(" · Whisper tentou e falhou: ") + part.Recovery.Error
				case "unavailable":
					engine += uiText(" · Whisper indisponível: ") + part.Recovery.Error
				}
			}
			lines = append(lines, uiTextf("Trecho %s · %s", number, engine))
		}
	}
	if doc.lastResult != nil {
		describe(doc.lastResult.Parts, "")
	}
	doc.transcriptName.hint.text = strings.Join(lines, "\n")
}
