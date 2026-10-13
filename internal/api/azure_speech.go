package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"mime/multipart"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"ai-talker/internal/apiconfig"
	"ai-talker/internal/audio"
	"ai-talker/internal/transcript"
)

const azureSpeechTranscribeRoute = "/speechtotext/transcriptions:transcribe?api-version=2025-10-15"

type azureSpeechDefinition struct {
	Locales     []string `json:"locales,omitempty"`
	Diarization struct {
		Enabled bool `json:"enabled"`
	} `json:"diarization"`
	PhraseList *struct {
		Phrases []string `json:"phrases"`
	} `json:"phraseList,omitempty"`
	EnhancedMode struct {
		Enabled      bool   `json:"enabled"`
		Model        string `json:"model"`
		ModelOptions struct {
			Timestamps string `json:"timestamps"`
		} `json:"modelOptions"`
	} `json:"enhancedMode"`
}

type azureSpeechTimedText struct {
	Text                 string  `json:"text"`
	OffsetMilliseconds   float64 `json:"offsetMilliseconds"`
	DurationMilliseconds float64 `json:"durationMilliseconds"`
}

type azureSpeechPhrase struct {
	azureSpeechTimedText
	Speaker json.RawMessage        `json:"speaker"`
	Words   []azureSpeechTimedText `json:"words"`
}

type azureSpeechResponse struct {
	CombinedPhrases []struct {
		Text string `json:"text"`
	} `json:"combinedPhrases"`
	Phrases []azureSpeechPhrase `json:"phrases"`
}

func (t *transcriber) transcribeAzureSpeech(ctx context.Context, chunk audio.Chunk) (transcript.ChunkResult, error) {
	if chunk.Duration <= 0 || math.IsNaN(chunk.Duration) || math.IsInf(chunk.Duration, 0) ||
		len(chunk.Audio) == 0 || len(chunk.Audio) > 25*1024*1024 {
		return transcript.ChunkResult{}, errors.New("a parte de áudio excedeu o limite de upload do Azure Speech")
	}
	if t.model != apiconfig.AzureSpeechModel {
		return transcript.ChunkResult{}, errors.New("Azure Speech usa o modelo MAI-Transcribe-2")
	}
	languages, err := (apiconfig.Audio{
		Connection: t.client.connection, Model: t.model, Language: t.language,
	}).LanguageCodes()
	if err != nil {
		return transcript.ChunkResult{}, err
	}
	filename := "audio.mp3"
	switch chunk.Format {
	case audio.FormatMP3:
	case audio.FormatWAV:
		filename = "audio.wav"
	default:
		return transcript.ChunkResult{}, errors.New("Azure Speech aceita somente trechos WAV ou MP3")
	}
	definition := azureSpeechDefinition{
		Locales: languages,
	}
	definition.Diarization.Enabled = true
	definition.EnhancedMode.Enabled = true
	definition.EnhancedMode.Model = apiconfig.AzureSpeechModel
	definition.EnhancedMode.ModelOptions.Timestamps = "word"
	if phrases := azureSpeechPhrases(t.glossary); len(phrases) > 0 {
		definition.PhraseList = &struct {
			Phrases []string `json:"phrases"`
		}{Phrases: phrases}
	}
	encodedDefinition, err := json.Marshal(definition)
	if err != nil {
		return transcript.ChunkResult{}, errors.New("não foi possível preparar a definição do Azure Speech")
	}
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	if err := form.WriteField("definition", string(encodedDefinition)); err != nil {
		return transcript.ChunkResult{}, errors.New("não foi possível preparar as opções do Azure Speech")
	}
	file, err := form.CreateFormFile("audio", filename)
	if err != nil {
		return transcript.ChunkResult{}, errors.New("não foi possível preparar o áudio para o Azure Speech")
	}
	if _, err := file.Write(chunk.Audio); err != nil {
		return transcript.ChunkResult{}, errors.New("não foi possível preparar o áudio para o Azure Speech")
	}
	if err := form.Close(); err != nil {
		return transcript.ChunkResult{}, errors.New("não foi possível finalizar o pedido do Azure Speech")
	}
	started := time.Now()
	var response azureSpeechResponse
	if err := t.client.send(ctx, "POST", azureSpeechTranscribeRoute,
		form.FormDataContentType(), &body, &response); err != nil {
		return transcript.ChunkResult{}, err
	}
	if len(response.Phrases) > 20000 || len(response.CombinedPhrases) > 256 {
		return transcript.ChunkResult{}, errors.New("o Azure Speech retornou segmentos demais para esta parte")
	}
	segments := make([]transcript.SpeechSegment, 0, len(response.Phrases))
	phraseTexts := make([]string, 0, len(response.Phrases))
	incompleteTiming := false
	wordCount := 0
	for _, phrase := range response.Phrases {
		text := strings.TrimSpace(phrase.Text)
		if text == "" {
			var words []string
			for _, word := range phrase.Words {
				if value := strings.TrimSpace(word.Text); value != "" {
					words = append(words, value)
				}
			}
			text = strings.Join(words, " ")
		}
		if text == "" {
			continue
		}
		wordCount += len(phrase.Words)
		if wordCount > 100000 {
			return transcript.ChunkResult{}, errors.New("o Azure Speech retornou palavras demais para esta parte")
		}
		phraseTexts = append(phraseTexts, text)
		parts, valid := azureSpeechSegments(phrase, text, chunk.Duration)
		incompleteTiming = incompleteTiming || !valid
		segments = append(segments, parts...)
	}
	slices.SortStableFunc(segments, func(a, b transcript.SpeechSegment) int {
		if a.Start < b.Start {
			return -1
		}
		if a.Start > b.Start {
			return 1
		}
		if a.End < b.End {
			return -1
		}
		if a.End > b.End {
			return 1
		}
		return 0
	})
	texts := make([]string, 0, len(response.CombinedPhrases))
	for _, phrase := range response.CombinedPhrases {
		if value := strings.TrimSpace(phrase.Text); value != "" {
			texts = append(texts, value)
		}
	}
	if len(texts) == 0 {
		texts = phraseTexts
	}
	text := strings.Join(texts, "\n")
	usage := map[string]any{
		"audio_seconds": chunk.Duration, "requests": 1, "speaker_scope": "recognition_call",
	}
	if len(segments) > 0 {
		incompleteTiming = incompleteTiming || len(transcript.CompleteSpeechSegments(transcript.Part{
			Text: text, Segments: segments,
		})) == 0
	}
	if incompleteTiming {
		// Formatting only the remaining timed words would silently omit the
		// untimed speech. Keep the complete text and use the block-order fallback.
		segments = nil
		usage["timestamps_incomplete"] = true
	}
	return transcript.ChunkResult{
		Text: text, Segments: segments,
		Usage:          usage,
		ElapsedSeconds: time.Since(started).Seconds(),
	}, nil
}

func azureSpeechSegments(phrase azureSpeechPhrase, text string, duration float64) ([]transcript.SpeechSegment, bool) {
	speaker := azureSpeechSpeaker(phrase.Speaker)
	if len(phrase.Words) > 0 {
		words := make([]transcript.SpeechSegment, 0, len(phrase.Words))
		fields := strings.Fields(text)
		for index, word := range phrase.Words {
			start, end, valid := azureSpeechTimes(word.OffsetMilliseconds, word.DurationMilliseconds, duration)
			value := strings.TrimSpace(word.Text)
			if !valid || value == "" {
				break
			}
			// Keep the service's display capitalization and punctuation when the
			// phrase and timed words have the same unambiguous token boundaries.
			if len(fields) == len(phrase.Words) &&
				azureSpeechNormalizedText(fields[index]) == azureSpeechNormalizedText(value) {
				value = fields[index]
			}
			words = append(words, transcript.SpeechSegment{
				Start: start, End: end, Speaker: speaker, Text: value,
			})
		}
		if len(words) == len(phrase.Words) {
			values := make([]string, len(words))
			for index, word := range words {
				values[index] = word.Text
			}
			if azureSpeechNormalizedText(strings.Join(values, " ")) == azureSpeechNormalizedText(text) {
				return words, true
			}
		}
	}
	start, end, valid := azureSpeechTimes(phrase.OffsetMilliseconds, phrase.DurationMilliseconds, duration)
	if !valid {
		return nil, false
	}
	return []transcript.SpeechSegment{{Start: start, End: end, Speaker: speaker, Text: text}}, true
}

func azureSpeechTimes(offset, milliseconds, duration float64) (float64, float64, bool) {
	start := offset / 1000
	end := start + milliseconds/1000
	if math.IsNaN(start) || math.IsInf(start, 0) || math.IsNaN(end) || math.IsInf(end, 0) ||
		start < 0 || milliseconds <= 0 || end <= start || start > duration+0.25 || end > duration+1 {
		return 0, 0, false
	}
	end = min(end, duration)
	return start, end, end > start
}

func azureSpeechNormalizedText(text string) string {
	var normalized strings.Builder
	for _, character := range strings.ToLower(text) {
		if unicode.IsLetter(character) || unicode.IsDigit(character) {
			normalized.WriteRune(character)
		}
	}
	return normalized.String()
}

func azureSpeechPhrases(glossary string) []string {
	seen := make(map[string]bool)
	var phrases []string
	for _, candidate := range strings.FieldsFunc(glossary, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r'
	}) {
		phrase := strings.TrimSpace(candidate)
		if phrase == "" || len([]rune(phrase)) > 256 {
			continue
		}
		key := strings.ToLower(phrase)
		if seen[key] {
			continue
		}
		seen[key] = true
		phrases = append(phrases, phrase)
	}
	return phrases
}

func azureSpeechSpeaker(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		text = strings.TrimSpace(text)
		if len([]rune(text)) <= 64 && !strings.ContainsAny(text, "\x00\r\n\t") {
			return text
		}
		return ""
	}
	var number json.Number
	if json.Unmarshal(raw, &number) != nil {
		return ""
	}
	if _, err := strconv.Atoi(number.String()); err != nil {
		return ""
	}
	return fmt.Sprint(number)
}
