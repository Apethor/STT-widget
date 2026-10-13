package transcript

import (
	"strings"
	"testing"
)

func TestMergePreservesSpeechAndRemovesOnlyClearOverlap(t *testing.T) {
	tests := []struct {
		name     string
		parts    []Part
		overlap  bool
		expected string
	}{
		{
			name: "technical terms and punctuation", overlap: true,
			parts: []Part{
				{Index: 0, Text: "Eu trabalho com TypeScript e Amazon Bedrock."},
				{Index: 1, Text: "TypeScript e Amazon Bedrock. Depois eu abro o VS Code."},
			},
			expected: "Eu trabalho com TypeScript e Amazon Bedrock.\n\nDepois eu abro o VS Code.",
		},
		{
			name: "real periodic repetitions", overlap: true,
			parts: []Part{
				{Index: 0, Text: "Eu repito um dois três um dois três."},
				{Index: 1, Text: "um dois três um dois três de novo."},
			},
			expected: "Eu repito um dois três um dois três.\n\num dois três um dois três de novo.",
		},
		{
			name: "silent gap", overlap: true,
			parts: []Part{
				{Index: 0, Text: "Eu uso a API do Bedrock."},
				{Index: 1},
				{Index: 2, Text: "Eu uso a API do Bedrock."},
			},
			expected: "Eu uso a API do Bedrock.\n\nEu uso a API do Bedrock.",
		},
		{
			name: "no overlap requested",
			parts: []Part{
				{Index: 0, Text: "Eu uso a API do Bedrock."},
				{Index: 1, Text: "a API do Bedrock."},
			},
			expected: "Eu uso a API do Bedrock.\n\na API do Bedrock.",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Merge(test.parts, test.overlap); got != test.expected {
				t.Fatalf("merged text:\n%s\nwant:\n%s", got, test.expected)
			}
		})
	}
}

func TestPartialTranscriptStopsAtTheFirstGap(t *testing.T) {
	parts := map[int]Part{
		0: {Index: 0, Text: "Primeira parte."},
		2: {Index: 2, Text: "Texto depois da falha."},
	}
	partial := Merge(contiguous(parts), true)
	if partial != "Primeira parte." || strings.Contains(partial, "depois") {
		t.Fatalf("partial text silently joined a gap: %q", partial)
	}
}

func TestDiarizedTextMapsSupportedIdentitiesAcrossOverlappingParts(t *testing.T) {
	t.Parallel()
	parts := []Part{
		{
			Index: 0, Start: 0, Duration: 4,
			Segments: []SpeechSegment{
				{Start: 2, End: 2.2, Speaker: "spk_0", Text: "Olá"},
				{Start: 2.2, End: 2.5, Speaker: "spk_0", Text: "mundo."},
			},
		},
		{
			Index: 1, Start: 2, Duration: 4,
			Segments: []SpeechSegment{
				{Start: 0, End: 0.2, Speaker: "spk_7", Text: "Olá"},
				{Start: 0.2, End: 0.5, Speaker: "spk_7", Text: "mundo."},
				{Start: 2, End: 2.2, Speaker: "spk_9", Text: "Tudo"},
				{Start: 2.2, End: 2.5, Speaker: "spk_9", Text: "bem?"},
			},
		},
		{
			Index: 2, Start: 4, Duration: 3,
			Segments: []SpeechSegment{
				{Start: 0, End: 0.2, Speaker: "speaker-2", Text: "Tudo"},
				{Start: 0.2, End: 0.5, Speaker: "speaker-2", Text: "bem?"},
				{Start: 1, End: 1.2, Speaker: "speaker-3", Text: "Até"},
				{Start: 1.2, End: 1.5, Speaker: "speaker-3", Text: "logo."},
			},
		},
	}
	text, ok := DiarizedText(parts, "Mic")
	if !ok || text != "Mic 1: Olá mundo.\n\nMic 2: Tudo bem?\n\nMic: Até logo." {
		t.Fatalf("a identidade confiável, o texto sobreposto ou a voz ainda anônima foram alterados: %q", text)
	}
}

func TestDiarizedTextFallsBackWhenProviderHasNoSpeakerIDs(t *testing.T) {
	t.Parallel()
	parts := []Part{{
		Index: 0, Start: 0, Duration: 2, Text: "Texto sem diarização.",
		Segments: []SpeechSegment{{Start: 0, End: 2, Text: "Texto sem diarização."}},
	}}
	if text, ok := DiarizedText(parts, "Mic"); ok || text != "" {
		t.Fatalf("timestamp-only output was presented as diarization: %q %v", text, ok)
	}
}

func TestDiarizedTextKeepsUnknownCrossCallIdentityAnonymous(t *testing.T) {
	t.Parallel()
	parts := []Part{
		{Index: 0, Start: 0, Duration: 4, Segments: []SpeechSegment{
			{Start: 0, End: 1, Speaker: "0", Text: "Voz inicial."},
		}},
		{Index: 1, Start: 2, Duration: 4, Segments: []SpeechSegment{
			{Start: 3, End: 4, Speaker: "0", Text: "Voz sem vínculo."},
		}},
	}
	if text, ok := DiarizedText(parts, "Mic"); !ok || text != "Mic 1: Voz inicial.\n\nMic: Voz sem vínculo." {
		t.Fatalf("o mesmo ID de outra chamada recebeu identidade inventada: %q / %v", text, ok)
	}
}

func TestDiarizedTextRetainsAnonymousLocalTurnBoundaries(t *testing.T) {
	t.Parallel()
	parts := []Part{
		{Index: 0, Start: 0, Duration: 4, Segments: []SpeechSegment{
			{Start: 0, End: 1, Speaker: "a", Text: "Primeira voz."},
			{Start: 2, End: 3, Speaker: "b", Text: "Segunda voz."},
		}},
		{Index: 1, Start: 4, Duration: 4, Segments: []SpeechSegment{
			{Start: 0, End: 1, Speaker: "a", Text: "Intervenção posterior."},
			{Start: 2, End: 3, Speaker: "b", Text: "Outra intervenção."},
		}},
	}
	want := "Sis 1: Primeira voz.\n\nSis 2: Segunda voz.\n\nSis: Intervenção posterior.\n\nSis: Outra intervenção."
	if text, ok := DiarizedText(parts, "Sis"); !ok || text != want {
		t.Fatalf("as vozes locais perderam seus turnos ou ganharam identificação inventada: %q / %v", text, ok)
	}
}

func TestDiarizedTextDoesNotIdentifyAmbiguousOverlappingVoices(t *testing.T) {
	t.Parallel()
	parts := []Part{
		{Index: 0, Start: 0, Duration: 4, Segments: []SpeechSegment{
			{Start: 2, End: 3, Speaker: "a", Text: "Sim."},
			{Start: 2, End: 3, Speaker: "b", Text: "Sim."},
		}},
		{Index: 1, Start: 2, Duration: 4, Segments: []SpeechSegment{
			{Start: 0, End: 1, Speaker: "c", Text: "Sim."},
		}},
	}
	want := "Mic 1: Sim.\n\nMic 2: Sim.\n\nMic: Sim."
	if text, ok := DiarizedText(parts, "Mic"); !ok || text != want {
		t.Fatalf("um overlap com duas identidades possíveis foi atribuído a uma pessoa: %q / %v", text, ok)
	}
}

func TestDiarizedTextKeepsCompleteCachedTextWhenWordMetadataIsIncomplete(t *testing.T) {
	t.Parallel()
	parts := []Part{
		{Index: 0, Start: 0, Duration: 1, Text: "Antes.",
			Segments: []SpeechSegment{{Start: 0, End: 1, Speaker: "0", Text: "Antes."}}},
		{Index: 1, Start: 1, Duration: 3, Text: "Primeira frase. Segunda frase.",
			Segments: []SpeechSegment{{Start: 0, End: 1, Speaker: "0", Text: "Primeira frase."}}},
	}
	got, ok := DiarizedText(parts, "Mic")
	if !ok || got != "Mic 1: Antes.\n\nMic: Primeira frase. Segunda frase." {
		t.Fatalf("metadados incompletos descartaram parte do texto original: %q", got)
	}
	if len(parts[1].Segments) != 1 {
		t.Fatal("a renderização apagou os metadados do cache original")
	}
}
