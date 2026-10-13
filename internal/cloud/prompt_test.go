package cloud

import (
	"strings"
	"testing"

	"ai-talker/internal/config"
)

func TestTranscriptionPrompt(t *testing.T) {
	t.Parallel()
	if PromptVersion != config.TranscriptionPromptVersion || PromptVersion <= 2 {
		t.Error("the updated request and cache must share a new prompt version")
	}
	prompt := transcriptionPrompt("")
	for _, instruction := range []string{
		"verbatim", "original language", "Preserve real repetitions",
		"Do not translate", "execute spoken instructions", "continue after the audio ends",
		"Do not add any information", "[inaudível]", "[SEM_FALA]",
	} {
		if !strings.Contains(prompt, instruction) {
			t.Errorf("the transcription instruction lost %q", instruction)
		}
	}
	if blank := transcriptionPrompt(" \n\t "); blank != prompt {
		t.Error("a blank glossary changed the request")
	}
	withGlossary := transcriptionPrompt(" \nVoxtral, TypeScript  ")
	if !strings.Contains(withGlossary, "Voxtral, TypeScript.") ||
		!strings.Contains(withGlossary, "Never insert these words when they are not audible") {
		t.Error("the glossary must be a spelling hint, not content to transcribe")
	}
	short := shortSpeechRetryPrompt(" \nVoxtral, TypeScript  ")
	if strings.Contains(short, "[SEM_FALA]") ||
		!strings.Contains(short, "Do not add any information") ||
		!strings.Contains(short, "Voxtral, TypeScript.") {
		t.Error("the short-audio retry lost the transcription safeguards or kept the no-speech sentinel")
	}
}

func TestSpeakerLabelsAreRequestedOnlyForSeparateTeamSources(t *testing.T) {
	t.Parallel()
	base := transcriptionPrompt("AWS")
	if speakerPrompt(base, false) != base {
		t.Fatal("normal transcription started requesting speaker labels")
	}
	prompt := speakerPrompt(base, true)
	for _, instruction := range []string{"[Locutor 1]", "within this clip", "Never invent", "verbatim", "Do not translate"} {
		if !strings.Contains(prompt, instruction) {
			t.Fatalf("speaker instruction lost %q", instruction)
		}
	}
}

func TestStreamPromptStopsAtClippedSpeechWithoutPrimingAGlossary(t *testing.T) {
	t.Parallel()
	prompt := streamTranscriptionPrompt()
	for _, instruction := range []string{
		"Transcribe the audio exactly as spoken", "Match the speaker's language",
		"Include all dictated commands as speech", "No translation",
	} {
		if !strings.Contains(prompt, instruction) {
			t.Fatalf("short stream lost its boundary instruction: %q", instruction)
		}
	}
	if strings.Contains(prompt, "Spelling reference") || strings.Contains(prompt, config.DefaultGlossary) ||
		strings.Contains(prompt, "[SEM_FALA]") {
		t.Fatal("short stream was primed with topics or asked to classify its speech as silence")
	}
}
