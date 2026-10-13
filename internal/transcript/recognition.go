package transcript

import (
	"regexp"
	"strings"
	"unicode"
)

var unintelligibleMarkers = regexp.MustCompile(
	`(?i)[\[(]\s*(?:inaud[ií]vel|inaudible|unintelligible|incompreens[ií]vel)\s*[\])]`,
)

// Only annotations count as markers. A spoken word or any recognized words
// around an annotation remain a transcription and must not be replaced.
func onlyUnintelligibleMarkers(text string) bool {
	if !unintelligibleMarkers.MatchString(text) {
		return false
	}
	remaining := unintelligibleMarkers.ReplaceAllString(text, "")
	return strings.TrimFunc(remaining, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune(".,;:!?…—–-", r)
	}) == ""
}
