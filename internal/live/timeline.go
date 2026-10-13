// Package live orders independently recognized audio and screenshot markers
// using positions in the saved recording, rather than network arrival order.
package live

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"ai-talker/internal/transcript"
)

var speakerLabels = regexp.MustCompile(`(?i)\[(?:locutor|speaker)\s*[0-9]+\]\s*:?\s*|(?m)^(?:Locutor|Speaker)\s*[0-9]+\s*:\s*`)

type Entry struct {
	ID             uint64            `json:"id"`
	Start          float64           `json:"start"`
	End            float64           `json:"end"`
	Track          string            `json:"track,omitempty"`
	Label          string            `json:"label,omitempty"`
	FirstSample    uint64            `json:"first_sample,omitempty"`
	LastSample     uint64            `json:"last_sample,omitempty"`
	ContextSamples uint64            `json:"context_samples,omitempty"`
	ContextSeconds float64           `json:"context_seconds,omitempty"`
	Text           string            `json:"text,omitempty"`
	Screenshot     bool              `json:"screenshot,omitempty"`
	Path           string            `json:"path,omitempty"`
	Pending        bool              `json:"pending,omitempty"`
	Error          string            `json:"error,omitempty"`
	NeedsReview    bool              `json:"needs_review,omitempty"`
	Hidden         bool              `json:"hidden,omitempty"`
	Parts          []transcript.Part `json:"parts,omitempty"`
}

type Block struct {
	ID      uint64 `json:"id"`
	Segment uint32 `json:"segment,omitempty"`
	Text    string `json:"text"`
	Join    bool   `json:"join,omitempty"`
}

func Blocks(entries []Entry, labels bool) []Block {
	ordered := slices.Clone(entries)
	slices.SortStableFunc(ordered, func(a, b Entry) int {
		if a.Start < b.Start {
			return -1
		}
		if a.Start > b.Start {
			return 1
		}
		// A screenshot at a boundary precedes speech that starts there.
		if a.Screenshot != b.Screenshot {
			if a.Screenshot {
				return -1
			}
			return 1
		}
		return 0
	})
	if hasTimedSegments(ordered) {
		return timedBlocks(ordered, labels)
	}
	blocks := make([]Block, 0, len(ordered))
	previousTrack, previousSpeech := "", false
	context := make(map[string]string)
	speakingTracks := make(map[string]bool)
	for _, entry := range ordered {
		if !entry.Screenshot && !entry.Hidden && strings.TrimSpace(entry.Text) != "" {
			speakingTracks[entry.Track] = true
		}
	}
	showSpeakers := labels && len(speakingTracks) > 1
	for _, entry := range ordered {
		var text string
		if entry.Screenshot {
			if entry.Path != "" {
				text = entry.Path
			}
		} else if strings.TrimSpace(entry.Text) != "" {
			text = strings.TrimSpace(entry.Text)
			if labels {
				text = strings.TrimSpace(speakerLabels.ReplaceAllString(text, ""))
			}
			if entry.ContextSamples > 0 {
				text = transcript.TrimOverlap(context[entry.Track], text)
			}
			words := strings.Fields(context[entry.Track] + " " + text)
			context[entry.Track] = strings.Join(words[max(0, len(words)-32):], " ")
			if showSpeakers && (!previousSpeech || previousTrack != entry.Track) && text != "" {
				name := entry.Label
				switch entry.Track {
				case "microphone":
					name = "Mic"
				case "system":
					name = "Sis"
				}
				if name != "" {
					text = name + ": " + text
				}
			}
		} else if entry.Error != "" {
			delete(context, entry.Track)
		} else {
			delete(context, entry.Track)
		}
		if entry.Hidden {
			continue
		}
		if text != "" {
			speech := !entry.Screenshot && strings.TrimSpace(entry.Text) != ""
			blocks = append(blocks, Block{ID: entry.ID, Text: text,
				Join: speech && previousSpeech && previousTrack == entry.Track})
			previousTrack, previousSpeech = entry.Track, speech
		}
	}
	return blocks
}

type timedBlock struct {
	id      uint64
	segment uint32
	group   string
	track   string
	start   float64
	end     float64
	speaker string
	person  int
	text    string
	screen  bool
}

func hasTimedSegments(entries []Entry) bool {
	for _, entry := range entries {
		if entry.Hidden || entry.Screenshot {
			continue
		}
		if partHasTimedSegments(entry.Parts) {
			return true
		}
	}
	return false
}

func partHasTimedSegments(parts []transcript.Part) bool {
	for _, part := range parts {
		if len(transcript.CompleteSpeechSegments(part)) > 0 || partHasTimedSegments(part.Children) {
			return true
		}
	}
	return false
}

func timedBlocks(entries []Entry, labels bool) []Block {
	var items []timedBlock
	speakingTracks := make(map[string]bool)
	diarizedTracks := make(map[string]bool)
	firstEntries := make(map[string]uint64)
	for _, entry := range entries {
		if !entry.Hidden && !entry.Screenshot && strings.TrimSpace(entry.Text) != "" {
			speakingTracks[entry.Track] = true
		}
		if !entry.Hidden && !entry.Screenshot &&
			(entry.Pending || strings.TrimSpace(entry.Text) != "" || partHasTimedSegments(entry.Parts)) {
			if _, exists := firstEntries[entry.Track]; !exists {
				firstEntries[entry.Track] = entry.ID
			}
		}
	}
	showSources := labels && len(speakingTracks) > 1
	contexts := make(map[string]string)
	for _, entry := range entries {
		if entry.Hidden {
			continue
		}
		if entry.Screenshot {
			if entry.Path != "" {
				items = append(items, timedBlock{
					id: entry.ID, start: entry.Start, end: entry.End,
					text: entry.Path, screen: true,
				})
			}
			continue
		}
		entryText := strings.TrimSpace(entry.Text)
		if labels {
			entryText = strings.TrimSpace(speakerLabels.ReplaceAllString(entryText, ""))
		}
		if entry.ContextSamples > 0 {
			entryText = transcript.TrimOverlap(contexts[entry.Track], entryText)
		}
		if entryText != "" {
			words := strings.Fields(contexts[entry.Track] + " " + entryText)
			contexts[entry.Track] = strings.Join(words[max(0, len(words)-32):], " ")
		}
		segmentIndex := uint32(0)
		base := entry.Start - entry.ContextSeconds
		var collect func([]transcript.Part, string)
		collect = func(parts []transcript.Part, path string) {
			for index, part := range parts {
				childPath := fmt.Sprintf("%s.%d", path, index)
				if len(part.Children) > 0 {
					collect(part.Children, childPath)
					continue
				}
				before := segmentIndex
				for _, segment := range transcript.CompleteSpeechSegments(part) {
					text := strings.TrimSpace(segment.Text)
					start, end := base+part.Start+segment.Start, base+part.Start+segment.End
					if text == "" || segment.Start < 0 || segment.End <= segment.Start ||
						math.IsNaN(start+end) || math.IsInf(start+end, 0) {
						continue
					}
					segmentIndex++
					if segmentIndex == 0 || segmentIndex > 100000 {
						continue
					}
					items = append(items, timedBlock{
						id: entry.ID, segment: segmentIndex,
						group: fmt.Sprintf("%d/%s", entry.ID, childPath), track: entry.Track,
						start: start, end: end, speaker: strings.TrimSpace(segment.Speaker), text: text,
					})
				}
				if segmentIndex == before && strings.TrimSpace(part.Text) != "" {
					// A local fallback or an incomplete provider response can return
					// text without timestamps beside another structured part. Keep
					// that complete part at its known audio-block position.
					segmentIndex++
					text := strings.TrimSpace(part.Text)
					if labels {
						text = strings.TrimSpace(speakerLabels.ReplaceAllString(text, ""))
					}
					items = append(items, timedBlock{
						id: entry.ID, segment: segmentIndex,
						group: fmt.Sprintf("%d/%s", entry.ID, childPath), track: entry.Track,
						start: base + part.Start, end: base + part.Start + part.Duration, text: text,
					})
				}
			}
		}
		if partHasTimedSegments(entry.Parts) {
			collect(entry.Parts, "")
		}
		if segmentIndex == 0 {
			items = append(items, timedBlock{
				id: entry.ID, track: entry.Track, start: entry.Start, end: entry.End, text: entryText,
			})
		}
	}
	references := make(map[string]string, len(firstEntries))
	for track, entryID := range firstEntries {
		references[track] = fmt.Sprintf("%d/pending", entryID)
	}
	observations := make([]transcript.SpeakerObservation, len(items))
	for index, item := range items {
		observations[index] = transcript.SpeakerObservation{
			Source: item.track, Scope: item.group, Speaker: item.speaker,
			Text: item.text, Start: item.start, End: item.end,
		}
		diarizedTracks[item.track] = diarizedTracks[item.track] || item.speaker != ""
		if item.speaker != "" && item.id == firstEntries[item.track] &&
			references[item.track] == fmt.Sprintf("%d/pending", item.id) {
			references[item.track] = item.group
		}
	}
	numbers := transcript.ResolveSpeakerNumbers(observations, references)
	for index := range items {
		items[index].person = numbers[index]
	}
	slices.SortStableFunc(items, func(a, b timedBlock) int {
		if a.start < b.start {
			return -1
		}
		if a.start > b.start {
			return 1
		}
		if a.screen != b.screen {
			if a.screen {
				return -1
			}
			return 1
		}
		if a.end < b.end {
			return -1
		}
		if a.end > b.end {
			return 1
		}
		if a.track < b.track {
			return -1
		}
		if a.track > b.track {
			return 1
		}
		return 0
	})

	var prior []timedBlock
	unique := make([]timedBlock, 0, len(items))
	for _, item := range items {
		if item.screen {
			unique = append(unique, item)
			prior = append(prior, item)
			continue
		}
		duplicate := false
		for index := len(prior) - 1; index >= 0; index-- {
			candidate := prior[index]
			if candidate.screen || candidate.track != item.track || candidate.group == item.group ||
				normalizedText(item.text) == "" || normalizedText(candidate.text) != normalizedText(item.text) ||
				!overlapMatches(candidate.start, candidate.end, item.start, item.end) {
				continue
			}
			samePerson := item.person > 0 && candidate.person == item.person
			noIdentity := item.speaker == "" && candidate.speaker == ""
			if samePerson || noIdentity {
				duplicate = true
				break
			}
		}
		prior = append(prior, item)
		if !duplicate {
			unique = append(unique, item)
		}
	}

	blocks := make([]Block, 0, len(unique))
	previousTrack, previousLabel := "", ""
	previousIdentity := ""
	previousSpeech := false
	for _, item := range unique {
		if item.screen {
			blocks = append(blocks, Block{ID: item.id, Text: item.text})
			previousTrack, previousLabel, previousIdentity, previousSpeech = "", "", "", false
			continue
		}
		text := strings.TrimSpace(item.text)
		if text == "" {
			continue
		}
		label := ""
		identity := ""
		if item.person > 0 {
			label = sourceLabel(item.track) + fmt.Sprintf(" %d", item.person)
			identity = fmt.Sprintf("person/%d", item.person)
		} else if item.speaker != "" {
			// The provider distinguishes local voices, but this call could not
			// link them to earlier people. Preserve the turn boundary and source.
			label = sourceLabel(item.track)
			identity = item.group + "\x00" + item.speaker
		} else if showSources || diarizedTracks[item.track] {
			label = sourceLabel(item.track)
		}
		if label != "" && (!previousSpeech || previousTrack != item.track ||
			previousLabel != label || previousIdentity != identity) {
			text = label + ": " + text
		}
		blocks = append(blocks, Block{
			ID: item.id, Segment: item.segment, Text: text,
			Join: previousSpeech && previousTrack == item.track &&
				previousLabel == label && previousIdentity == identity,
		})
		previousTrack, previousLabel, previousIdentity, previousSpeech = item.track, label, identity, true
	}
	return blocks
}

func sourceLabel(track string) string {
	switch track {
	case "microphone":
		return "Mic"
	case "system":
		return "Sis"
	default:
		return "Pessoa"
	}
}

func normalizedText(text string) string {
	var normalized strings.Builder
	for _, character := range strings.ToLower(text) {
		if unicode.IsLetter(character) || unicode.IsDigit(character) {
			normalized.WriteRune(character)
		}
	}
	return normalized.String()
}

func overlapMatches(aStart, aEnd, bStart, bEnd float64) bool {
	overlap := min(aEnd, bEnd) - max(aStart, bStart)
	shorter := min(aEnd-aStart, bEnd-bStart)
	return overlap > 0 && shorter > 0 && overlap/shorter >= 0.5
}

func Render(prefix string, blocks []Block) string {
	var text strings.Builder
	text.WriteString(prefix)
	for _, block := range blocks {
		if text.Len() > 0 {
			text.WriteString(blockSeparator(block))
		}
		text.WriteString(block.Text)
	}
	return text.String()
}

func blockSeparator(block Block) string {
	if block.Join {
		return " "
	}
	return "\n\n"
}

type blockKey struct {
	id      uint64
	segment uint32
}

func identity(block Block) blockKey {
	return blockKey{id: block.ID, segment: block.Segment}
}

// Reconcile preserves user edits to already shown blocks. Late recognition is
// inserted before the next unchanged block (often a screenshot).
// A block edited or removed by the user is never silently overwritten.
func Reconcile(current string, previous, next []Block) string {
	old := make(map[blockKey]Block, len(previous))
	future := make(map[blockKey]bool, len(next))
	for _, block := range previous {
		old[identity(block)] = block
	}
	for _, block := range next {
		future[identity(block)] = true
	}
	for _, block := range previous {
		if !future[identity(block)] {
			current = strings.Replace(current, block.Text, "", 1)
		}
	}
	for index, block := range next {
		prior, exists := old[identity(block)]
		if exists {
			if prior.Text != block.Text {
				current = strings.Replace(current, prior.Text, block.Text, 1)
			}
			continue
		}
		position := -1
		leading, trailing := blockSeparator(block), "\n\n"
		oldSeparator := ""
		for _, following := range next[index+1:] {
			prior := old[identity(following)]
			anchor := prior.Text
			if anchor == "" {
				continue
			}
			if found := strings.Index(current, anchor); found >= 0 {
				position = found
				trailing, oldSeparator = blockSeparator(following), blockSeparator(prior)
				break
			}
		}
		if position >= 0 {
			before := strings.TrimSuffix(current[:position], oldSeparator)
			if before != "" {
				before += leading
			}
			current = before + block.Text + trailing + current[position:]
		} else {
			if current != "" && !strings.HasSuffix(current, leading) {
				current += leading
			}
			current += block.Text
		}
	}
	return strings.TrimRight(current, "\n")
}
