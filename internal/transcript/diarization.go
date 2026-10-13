package transcript

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"unicode"

	"ai-talker/internal/config"
)

type diarizedItem struct {
	group   int
	order   int
	start   float64
	end     float64
	speaker string
	person  int
	text    string
}

// SpeakerObservation identifies one provider label within one recognition call.
// Scope is local to that call; the same Speaker in another scope is not evidence
// that the person is the same. Source distinguishes the physical audio tracks.
type SpeakerObservation struct {
	Source  string
	Scope   string
	Speaker string
	Text    string
	Start   float64
	End     float64
}

// ResolveSpeakerNumbers links labels only through duplicate recognition of the
// same audio interval. Observations must be grouped in recognition-call order.
// Unknown identities return zero. initialScopes can reserve an earlier pending
// call so that network arrival cannot assign the first person number.
func ResolveSpeakerNumbers(observations []SpeakerObservation, initialScopes map[string]string) []int {
	type scopeKey struct{ source, scope string }
	type speakerKey struct {
		scopeKey
		speaker string
	}
	scopes := make([]scopeKey, 0)
	indexes := make(map[scopeKey][]int)
	references := make(map[string]string)
	for source, scope := range initialScopes {
		references[source] = scope
	}
	normalized := make([]string, len(observations))
	for index, observation := range observations {
		normalized[index] = normalizedSpeakerText(observation.Text)
		if observation.Speaker == "" || observation.Scope == "" {
			continue
		}
		scope := scopeKey{observation.Source, observation.Scope}
		if _, exists := indexes[scope]; !exists {
			scopes = append(scopes, scope)
		}
		indexes[scope] = append(indexes[scope], index)
		if _, exists := references[scope.source]; !exists {
			references[scope.source] = scope.scope
		}
	}
	for _, scope := range scopes {
		slices.SortStableFunc(indexes[scope], func(a, b int) int {
			if observations[a].Start < observations[b].Start {
				return -1
			}
			if observations[a].Start > observations[b].Start {
				return 1
			}
			if observations[a].End < observations[b].End {
				return -1
			}
			if observations[a].End > observations[b].End {
				return 1
			}
			return 0
		})
	}
	numbers := make(map[speakerKey]int)
	known := make(map[string]int)
	result := make([]int, len(observations))
	resolved := make(map[string]map[string][]int)
	remember := func(source string, index int) {
		if resolved[source] == nil {
			resolved[source] = make(map[string][]int)
		}
		key := normalized[index]
		resolved[source][key] = append(resolved[source][key], index)
	}
	for _, scope := range scopes {
		if scope.scope != references[scope.source] {
			continue
		}
		for _, index := range indexes[scope] {
			key := speakerKey{scope, observations[index].Speaker}
			if numbers[key] == 0 {
				known[scope.source]++
				numbers[key] = known[scope.source]
			}
			result[index] = numbers[key]
			remember(scope.source, index)
		}
	}
	for _, scope := range scopes {
		if scope.scope == references[scope.source] {
			continue
		}
		local := make(map[string][]int)
		var speakers []string
		for _, index := range indexes[scope] {
			speaker := observations[index].Speaker
			if _, exists := local[speaker]; !exists {
				speakers = append(speakers, speaker)
			}
			local[speaker] = append(local[speaker], index)
		}
		anchors := make(map[string]int)
		owners := make(map[int]string)
		conflicts := make(map[int]bool)
		for _, speaker := range speakers {
			candidates := make(map[int]bool)
			for _, index := range local[speaker] {
				item := observations[index]
				if normalized[index] == "" {
					continue
				}
				for _, previous := range resolved[scope.source][normalized[index]] {
					prior := observations[previous]
					if speakerTimesOverlap(item.Start, item.End, prior.Start, prior.End) {
						candidates[result[previous]] = true
					}
				}
			}
			if len(candidates) == 1 {
				for person := range candidates {
					anchors[speaker] = person
					if previous := owners[person]; previous != "" && previous != speaker {
						conflicts[person] = true
					}
					owners[person] = speaker
				}
			}
		}
		anchored := make(map[int]bool)
		for speaker, person := range anchors {
			if !conflicts[person] {
				numbers[speakerKey{scope, speaker}] = person
				anchored[person] = true
			}
		}
		// A distinct local label is certainly new only when every already known
		// person is represented by a reliable anchor in this same call.
		canAddPeople := known[scope.source] > 0 && len(anchored) == known[scope.source]
		for _, speaker := range speakers {
			key := speakerKey{scope, speaker}
			if numbers[key] == 0 && canAddPeople {
				known[scope.source]++
				numbers[key] = known[scope.source]
			}
			for _, index := range local[speaker] {
				result[index] = numbers[key]
				if result[index] > 0 {
					remember(scope.source, index)
				}
			}
		}
	}
	return result
}

func captureSpeakerPrefix(mode string) string {
	switch mode {
	case config.CaptureSystem:
		return "Sis"
	case config.CaptureMicrophone:
		return "Mic"
	default:
		return "Pessoa"
	}
}

// CompleteSpeechSegments returns the original metadata only when it covers all
// words in the part's text. Incomplete old caches remain intact; renderers can
// use the complete text at its audio-block position instead of losing words.
// The returned slice is borrowed and must not be changed.
func CompleteSpeechSegments(part Part) []SpeechSegment {
	if len(part.Segments) == 0 || len(part.Segments) > 100000 {
		return nil
	}
	var text strings.Builder
	for _, segment := range part.Segments {
		if strings.TrimSpace(segment.Text) == "" {
			continue
		}
		if segment.Start < 0 || segment.End <= segment.Start ||
			math.IsNaN(segment.Start+segment.End) || math.IsInf(segment.Start+segment.End, 0) {
			return nil
		}
		text.WriteString(segment.Text)
		text.WriteByte(' ')
	}
	if normalizedSpeakerText(text.String()) == "" {
		return nil
	}
	if strings.TrimSpace(part.Text) != "" && !sameSpeechWords(part.Text, text.String()) {
		return nil
	}
	return part.Segments
}

func sameSpeechWords(first, second string) bool {
	words := func(text string) []string {
		return strings.FieldsFunc(strings.ToLower(text), func(character rune) bool {
			return !unicode.IsLetter(character) && !unicode.IsDigit(character)
		})
	}
	counts := make(map[string]int)
	for _, word := range words(first) {
		counts[word]++
	}
	for _, word := range words(second) {
		if counts[word] == 0 {
			return false
		}
		counts[word]--
	}
	for _, count := range counts {
		if count != 0 {
			return false
		}
	}
	return true
}

// DiarizedText formats structured, timestamped provider output and reconciles
// speaker IDs between adjacent chunks by matching speech in their overlap.
// It returns false when the recognizer supplied no speaker IDs.
func DiarizedText(parts []Part, source string) (string, bool) {
	ordered := slices.Clone(parts)
	slices.SortStableFunc(ordered, func(a, b Part) int {
		if a.Start < b.Start {
			return -1
		}
		if a.Start > b.Start {
			return 1
		}
		return a.Index - b.Index
	})
	var items []diarizedItem
	group, order := 0, 0
	var collect func(Part)
	collect = func(part Part) {
		if len(part.Children) > 0 {
			children := slices.Clone(part.Children)
			slices.SortStableFunc(children, func(a, b Part) int {
				if a.Start < b.Start {
					return -1
				}
				if a.Start > b.Start {
					return 1
				}
				return a.Index - b.Index
			})
			for _, child := range children {
				collect(child)
			}
			return
		}
		group++
		segments := CompleteSpeechSegments(part)
		if len(segments) == 0 {
			if text := strings.TrimSpace(part.Text); text != "" {
				items = append(items, diarizedItem{group: group, order: order,
					start: part.Start, end: part.Start + part.Duration, text: text})
				order++
			}
			return
		}
		for _, segment := range segments {
			text := strings.TrimSpace(segment.Text)
			start, end := part.Start+segment.Start, part.Start+segment.End
			if text == "" || segment.Start < 0 || segment.End <= segment.Start ||
				math.IsNaN(start+end) || math.IsInf(start+end, 0) {
				continue
			}
			items = append(items, diarizedItem{
				group: group, order: order, start: start, end: end,
				speaker: strings.TrimSpace(segment.Speaker), text: text,
			})
			order++
		}
	}
	for _, part := range ordered {
		collect(part)
	}
	if len(items) == 0 {
		return "", false
	}
	observations := make([]SpeakerObservation, len(items))
	hasSpeakers := false
	for index, item := range items {
		observations[index] = SpeakerObservation{
			Source: source, Scope: fmt.Sprint(item.group), Speaker: item.speaker,
			Text: item.text, Start: item.start, End: item.end,
		}
		hasSpeakers = hasSpeakers || item.speaker != ""
	}
	if !hasSpeakers {
		return "", false
	}
	numbers := ResolveSpeakerNumbers(observations, nil)
	for index := range items {
		items[index].person = numbers[index]
	}
	slices.SortStableFunc(items, func(a, b diarizedItem) int {
		if a.start < b.start {
			return -1
		}
		if a.start > b.start {
			return 1
		}
		if a.end < b.end {
			return -1
		}
		if a.end > b.end {
			return 1
		}
		return a.order - b.order
	})

	var prior []diarizedItem
	var unique []diarizedItem
	for _, item := range items {
		duplicate := false
		for index := len(prior) - 1; index >= 0; index-- {
			candidate := prior[index]
			samePerson := item.person > 0 && candidate.person == item.person
			noIdentity := item.speaker == "" && candidate.speaker == ""
			if candidate.group != item.group && (samePerson || noIdentity) &&
				normalizedSpeakerText(candidate.text) == normalizedSpeakerText(item.text) &&
				speakerTimesOverlap(candidate.start, candidate.end, item.start, item.end) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			unique = append(unique, item)
		}
		prior = append(prior, item)
	}
	var result strings.Builder
	currentIdentity := ""
	hasText := false
	for _, item := range unique {
		if item.text == "" {
			continue
		}
		identity := fmt.Sprintf("person/%d", item.person)
		if item.person == 0 {
			identity = fmt.Sprintf("scope/%d/%s", item.group, item.speaker)
		}
		if identity != currentIdentity {
			if hasText {
				result.WriteString("\n\n")
			}
			label := source
			if label == "" {
				label = "Pessoa"
			}
			if item.person > 0 {
				fmt.Fprintf(&result, "%s %d: ", label, item.person)
			} else {
				fmt.Fprintf(&result, "%s: ", label)
			}
			currentIdentity = identity
			hasText = true
		} else if hasText {
			result.WriteByte(' ')
		}
		result.WriteString(item.text)
		hasText = true
	}
	return strings.TrimSpace(result.String()), true
}

func speakerTimesOverlap(aStart, aEnd, bStart, bEnd float64) bool {
	overlap := min(aEnd, bEnd) - max(aStart, bStart)
	shorter := min(aEnd-aStart, bEnd-bStart)
	return overlap > 0 && shorter > 0 && overlap/shorter >= 0.5
}

func normalizedSpeakerText(text string) string {
	var normalized strings.Builder
	for _, character := range strings.ToLower(text) {
		if unicode.IsLetter(character) || unicode.IsDigit(character) {
			normalized.WriteRune(character)
		}
	}
	return normalized.String()
}
