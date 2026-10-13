package transcript

import (
	"slices"
	"strings"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

func Merge(parts []Part, hasOverlap bool) string {
	ordered := slices.Clone(parts)
	slices.SortFunc(ordered, func(left, right Part) int { return left.Index - right.Index })
	texts := make([]string, 0, len(ordered))
	var previous *Part
	for index := range ordered {
		part := &ordered[index]
		current := strings.TrimSpace(part.Text)
		isAdjacent := previous != nil && part.Index == previous.Index+1
		if hasOverlap && len(texts) > 0 && current != "" && isAdjacent && strings.TrimSpace(previous.Text) != "" {
			current = TrimOverlap(previous.Text, current)
		}
		if current != "" {
			texts = append(texts, current)
		}
		previous = part
	}
	return strings.Join(texts, "\n\n")
}

type word struct {
	value string
	end   int
}

func words(text string) []word {
	result := make([]word, 0)
	start := -1
	for offset, char := range text {
		if !unicode.IsSpace(char) {
			if start < 0 {
				start = offset
			}
			continue
		}
		if start >= 0 {
			result = append(result, word{value: normalize(text[start:offset]), end: offset})
			start = -1
		}
	}
	if start >= 0 {
		result = append(result, word{value: normalize(text[start:]), end: len(text)})
	}
	return result
}

func normalize(text string) string {
	var normalized strings.Builder
	for _, char := range cases.Fold().String(norm.NFKC.String(text)) {
		if unicode.IsLetter(char) || unicode.IsDigit(char) {
			normalized.WriteRune(char)
		}
	}
	return normalized.String()
}

// TrimOverlap removes a matching suffix/prefix from overlapping recognition.
// Ambiguous repetitions and matches shorter than three distinct words remain.
func TrimOverlap(previous, current string) string {
	left, right := words(previous), words(current)
	for count := min(16, len(left), len(right)); count >= 3; count-- {
		suffix := left[len(left)-count:]
		unique := make(map[string]bool, count)
		matches := true
		for index, item := range suffix {
			unique[item.value] = true
			if item.value == "" || item.value != right[index].value {
				matches = false
				break
			}
		}
		if matches && isPeriodic(suffix) {
			// A shorter match within a repeated cycle is still ambiguous.
			// Searching smaller suffixes here would remove a real repetition.
			return current
		}
		if !matches || len(unique) < 3 {
			continue
		}
		return strings.TrimSpace(current[right[count-1].end:])
	}
	return current
}

func isPeriodic(sequence []word) bool {
	for period := 1; period <= len(sequence)/2; period++ {
		matches := true
		for index, item := range sequence {
			if item.value != sequence[index%period].value {
				matches = false
				break
			}
		}
		if matches {
			return true
		}
	}
	return false
}

func contiguous(parts map[int]Part) []Part {
	result := make([]Part, 0, len(parts))
	for index := 0; ; index++ {
		part, exists := parts[index]
		if !exists {
			return result
		}
		result = append(result, part)
	}
}
