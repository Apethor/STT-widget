package transcript

import (
	"math"
	"regexp"
	"strings"
	"unicode/utf8"
)

var liveAnnotations = regexp.MustCompile(`(?i)\[(?:inaud[ií]vel|inaudible|unintelligible|incompreens[ií]vel|[^\]\r\n]*(?:imposs[ií]vel|impossible|n[aã]o [eé] poss[ií]vel|cannot|unable)[^\]\r\n]*(?:transcrev|transcrib|transcri)[^\]\r\n]*)\]`)
var liveSpacing = regexp.MustCompile(`[ \t]{2,}`)

// CleanLiveText keeps recognizer diagnostics out of the user's spoken text.
// The caller retains the review flag and audio, so omitted uncertain speech
// remains recoverable without copying a model's annotation as dictation.
func CleanLiveText(text string) (string, bool) {
	clean := liveAnnotations.ReplaceAllString(text, "")
	unclear := clean != text
	clean = strings.TrimSpace(liveSpacing.ReplaceAllString(clean, " "))
	for _, prefix := range []string{"translation:", "english translation:", "tradução:", "tradução para", "translated text:"} {
		if strings.HasPrefix(strings.ToLower(clean), prefix) {
			return "", true
		}
	}
	return clean, unclear
}

func LivePartialText(text string) string {
	// Do not briefly render the beginning of an annotation while its closing
	// bracket is still arriving. Speaker labels are shown once complete.
	if opened := strings.LastIndex(text, "["); opened >= 0 && !strings.Contains(text[opened:], "]") {
		text = text[:opened]
	}
	clean, _ := CleanLiveText(text)
	return clean
}

// ImplausibleLiveText guards provisional text as well as completed live parts.
// It rejects gross continuations without pretending to measure ASR accuracy.
func ImplausibleLiveText(text string, duration float64) bool {
	duration = max(0, min(300, duration))
	wordLimit := max(16, math.Ceil(duration*8+8))
	characterLimit := max(128, math.Ceil(duration*80))
	return float64(len(strings.Fields(text))) > wordLimit ||
		float64(utf8.RuneCountInString(text)) > characterLimit
}
