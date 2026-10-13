package live

import (
	"slices"
	"strings"
	"testing"

	"ai-talker/internal/transcript"
)

func TestTimelineOrdersLateSpeechBeforeScreenshotAndKeepsSources(t *testing.T) {
	t.Parallel()
	entries := []Entry{
		{ID: 1, Start: 3, Screenshot: true, Path: `C:\screenshots\shot.png`},
		{ID: 2, Start: 3, End: 5, Track: "system", Label: "Áudio do sistema", Text: "Depois da foto."},
		{ID: 3, Start: 0, End: 3, Track: "microphone", Label: "Você", Pending: true},
	}
	before := Blocks(entries, true)
	current := Render("Texto anterior.", before)
	entries[2].Text, entries[2].Pending = "Antes da foto.", false
	after := Blocks(entries, true)
	result := Reconcile(current, before, after)
	if result != Render("Texto anterior.", after) || strings.Index(result, "Antes da foto.") > strings.Index(result, entries[0].Path) ||
		strings.Index(result, "Depois da foto.") < strings.Index(result, entries[0].Path) ||
		!strings.Contains(result, "Sis: Depois") || strings.Contains(result, "[00:") {
		t.Fatalf("late recognition changed the timeline: %q", result)
	}
}

func TestTimelinePreservesUserEditsAndDeletedBlocksWhileAddingLateSpeech(t *testing.T) {
	t.Parallel()
	entries := []Entry{
		{ID: 1, Start: 0, Text: "Texto recebido."},
		{ID: 2, Start: 5, Screenshot: true, Path: `C:\shot.png`},
	}
	before := Blocks(entries, false)
	current := strings.Replace(Render("Meu texto.", before), "Texto recebido.", "Texto corrigido por mim.", 1)
	entries[0].Text = "Nova tentativa não pode apagar minha edição."
	entries = slices.Insert(entries, 1, Entry{ID: 3, Start: 3, End: 5, Text: "Fala que chegou atrasada."})
	result := Reconcile(current, before, Blocks(entries, false))
	if !strings.Contains(result, "Texto corrigido por mim.") ||
		strings.Contains(result, "Nova tentativa") || !strings.HasPrefix(result, "Meu texto.") ||
		strings.Index(result, "Fala que chegou atrasada.") > strings.Index(result, entries[2].Path) {
		t.Fatal("updated timeline overwrote the user or misplaced late speech")
	}
}

func TestTimelineContinuousSpeechKeepsScreenshotBetweenBeforeAndAfter(t *testing.T) {
	t.Parallel()
	entries := []Entry{
		{ID: 1, Start: 0, End: 2, Track: "microphone", Text: "Primeira fala."},
		{ID: 2, Start: 2, End: 4, Track: "microphone", Pending: true},
		{ID: 3, Start: 4, Screenshot: true, Path: `C:\shot.png`},
		{ID: 4, Start: 4, End: 6, Track: "microphone", Text: "Fala depois da foto."},
		{ID: 5, Start: 6, End: 8, Track: "microphone", Text: "Continuação."},
	}
	before := Blocks(entries, false)
	edited := strings.Replace(Render("", before), "Primeira fala.", "Minha correção.", 1)
	entries[1].Text = "Segunda fala."
	after := Blocks(entries, false)
	result := Reconcile(edited, before, after)
	if result != "Minha correção. Segunda fala.\n\nC:\\shot.png\n\nFala depois da foto. Continuação." {
		t.Fatalf("speech was split into timed paragraphs or the screenshot moved: %q", result)
	}
	if canonical := Render("", after); canonical != "Primeira fala. Segunda fala.\n\nC:\\shot.png\n\nFala depois da foto. Continuação." {
		t.Fatalf("live speech is not continuous: %q", canonical)
	}
}

func TestTimelineOverlappingAudioKeepsOnlyNewSpeechAfterScreenshot(t *testing.T) {
	t.Parallel()
	entries := []Entry{
		{ID: 1, Start: 0, End: 2, Track: "microphone", Pending: true},
		{ID: 2, Start: 2, Screenshot: true, Path: `C:\shot.png`},
		{ID: 3, Start: 2, End: 3.5, Track: "microphone", ContextSamples: 32000,
			Text: "Hello. This is a short recording."},
	}
	before := Blocks(entries, false)
	entries[0].Text, entries[0].Pending = "Hello. This is a short", false
	after := Blocks(entries, false)
	want := "Hello. This is a short\n\nC:\\shot.png\n\nrecording."
	if got := Reconcile(Render("", before), before, after); got != want {
		t.Fatalf("late context repeated earlier speech or crossed the screenshot: %q", got)
	}
	entries[0].Hidden = true
	if got := Render("", Blocks(entries, false)); got != "C:\\shot.png\n\nrecording." {
		t.Fatalf("overlap restored cleared speech: %q", got)
	}
}

func TestTimelineOverlapNeverRemovesAnotherSourceOrAnUnknownGap(t *testing.T) {
	t.Parallel()
	entries := []Entry{
		{ID: 1, Start: 0, End: 2, Track: "microphone", Label: "Você", Text: "Uma frase em comum."},
		{ID: 2, Start: 2, End: 4, Track: "system", Label: "Sistema", ContextSamples: 32000,
			Text: "Uma frase em comum. Outra voz."},
	}
	if got := Render("", Blocks(entries, true)); got != "Mic: Uma frase em comum.\n\nSis: Uma frase em comum. Outra voz." {
		t.Fatalf("overlap crossed physical sources: %q", got)
	}
	entries[1].Track = "microphone"
	entries = slices.Insert(entries, 1, Entry{ID: 3, Start: 1, End: 2, Track: "microphone", Pending: true})
	if got := Render("", Blocks(entries, false)); got != "Uma frase em comum. Uma frase em comum. Outra voz." {
		t.Fatalf("an unrecognized gap removed a possible real repetition: %q", got)
	}
}

func TestTeamLabelsOnlyAppearOnSpeakerChangesAndNeverRepeatModelNumbers(t *testing.T) {
	t.Parallel()
	entries := []Entry{
		{ID: 1, Start: 0, Track: "system", Label: "Áudio do sistema", Text: "[Locutor 1] Primeira frase."},
		{ID: 2, Start: 2, Track: "system", Text: "[Locutor 1] Segunda frase."},
		{ID: 3, Start: 4, Track: "microphone", Text: "Minha pergunta."},
		{ID: 4, Start: 6, Track: "microphone", Text: "[Speaker 1] Continuo perguntando."},
		{ID: 5, Start: 8, Track: "system", Text: "Locutor 1: A resposta."},
	}
	want := "Sis: Primeira frase. Segunda frase.\n\nMic: Minha pergunta. Continuo perguntando.\n\nSis: A resposta."
	if got := Render("", Blocks(entries, true)); got != want {
		t.Fatalf("continuous speaker turns were labeled again: %q", got)
	}
	if got := Render("", Blocks(entries[:2], true)); got != "Primeira frase. Segunda frase." {
		t.Fatalf("single-source speech still showed technical labels: %q", got)
	}
}

func TestTimelineInterleavesTimestampedSpeakersAcrossSourcesAndKeepsNumbers(t *testing.T) {
	t.Parallel()
	entries := []Entry{
		{
			ID: 1, Start: 0, End: 4, Track: "microphone", Text: "Olá, tudo bem?",
			Parts: []transcript.Part{{
				Start: 0, Duration: 4, Segments: []transcript.SpeechSegment{
					{Start: 0.5, End: 0.7, Speaker: "spk_0", Text: "Olá,"},
					{Start: 3.0, End: 3.3, Speaker: "spk_0", Text: "tudo"},
					{Start: 3.3, End: 3.6, Speaker: "spk_0", Text: "bem?"},
				},
			}},
		},
		{
			ID: 2, Start: 0, End: 4, Track: "system", Text: "Oi. Pode falar.",
			Parts: []transcript.Part{{
				Start: 0, Duration: 4, Segments: []transcript.SpeechSegment{
					{Start: 0.5, End: 0.8, Speaker: "speaker-4", Text: "Oi."},
					{Start: 2.0, End: 2.2, Speaker: "speaker-4", Text: "Pode"},
					{Start: 2.2, End: 2.5, Speaker: "speaker-4", Text: "falar."},
				},
			}},
		},
		{
			ID: 3, Start: 4, End: 8, Track: "system", ContextSamples: 32000, ContextSeconds: 2,
			Text: "Pode falar. Estou ouvindo.",
			Parts: []transcript.Part{{
				Start: 0, Duration: 4, Segments: []transcript.SpeechSegment{
					{Start: 0, End: 0.2, Speaker: "speaker-8", Text: "Pode"},
					{Start: 0.2, End: 0.5, Speaker: "speaker-8", Text: "falar."},
					{Start: 1.5, End: 1.8, Speaker: "speaker-8", Text: "Estou"},
					{Start: 1.8, End: 2.1, Speaker: "speaker-8", Text: "ouvindo."},
				},
			}},
		},
	}
	got := Render("", Blocks(entries, true))
	want := "Mic 1: Olá,\n\nSis 1: Oi. Pode falar.\n\nMic 1: tudo bem?\n\nSis 1: Estou ouvindo."
	if got != want || strings.Contains(got, "[00:") {
		t.Fatalf("the UI lost chronological speaker turns or restarted a source ID: %q", got)
	}
}

func TestTimelineTimestampOnlyOutputKeepsSourceLabels(t *testing.T) {
	t.Parallel()
	entries := []Entry{
		{ID: 1, Start: 0, End: 2, Track: "microphone", Text: "Uma frase.",
			Parts: []transcript.Part{{Start: 0, Duration: 2, Segments: []transcript.SpeechSegment{
				{Start: 0, End: 2, Text: "Uma frase."},
			}}}},
		{ID: 2, Start: 0, End: 2, Track: "system", Text: "Outra frase.",
			Parts: []transcript.Part{{Start: 0, Duration: 2, Segments: []transcript.SpeechSegment{
				{Start: 0.5, End: 1.5, Text: "Outra frase."},
			}}}},
	}
	got := Render("", Blocks(entries, true))
	if got != "Mic: Uma frase.\n\nSis: Outra frase." {
		t.Fatalf("timestamp-only segments did not retain compact source labels: %q", got)
	}
}

func TestTimelinePreservesUntimedPartsAlongsideStructuredSpeech(t *testing.T) {
	t.Parallel()
	entries := []Entry{
		{
			ID: 1, Start: 0, End: 8, Track: "microphone", Text: "Antes. Recuperado sem tempos. Depois.",
			Parts: []transcript.Part{
				{Start: 0, Duration: 8, Children: []transcript.Part{
					{Index: 0, Start: 0, Duration: 2, Text: "Antes.", Segments: []transcript.SpeechSegment{
						{Start: 0, End: 1, Speaker: "a", Text: "Antes."},
					}},
					{Index: 1, Start: 2, Duration: 3, Text: "Recuperado sem tempos."},
					{Index: 2, Start: 5, Duration: 3, Text: "Depois.", Segments: []transcript.SpeechSegment{
						{Start: 0, End: 1, Speaker: "a", Text: "Depois."},
					}},
				}},
			},
		},
		{ID: 2, Start: 4, End: 4, Screenshot: true, Path: "foto.png"},
	}
	text := Render("", Blocks(entries, false))
	if !strings.Contains(text, "Recuperado sem tempos.") ||
		strings.Index(text, "Antes.") > strings.Index(text, "Recuperado sem tempos.") ||
		strings.Index(text, "Recuperado sem tempos.") > strings.Index(text, "foto.png") ||
		strings.Index(text, "foto.png") > strings.Index(text, "Depois.") {
		t.Fatalf("a parte sem metadados foi perdida ou saiu de sua posição: %q", text)
	}
}

func TestTimelineDoesNotInventPeopleAcrossCallsWithoutSharedSpeech(t *testing.T) {
	t.Parallel()
	entries := []Entry{
		{ID: 1, Start: 0, End: 4, Track: "system", Text: "Primeira voz. Segunda voz.",
			Parts: []transcript.Part{{Start: 0, Duration: 4, Segments: []transcript.SpeechSegment{
				{Start: 0, End: 1, Speaker: "0", Text: "Primeira voz."},
				{Start: 2, End: 3, Speaker: "1", Text: "Segunda voz."},
			}}}},
		{ID: 2, Start: 4, End: 8, Track: "system", ContextSamples: 32000, ContextSeconds: 2,
			Text: "A voz reapareceu.",
			Parts: []transcript.Part{{Start: 0, Duration: 6, Segments: []transcript.SpeechSegment{
				{Start: 3, End: 4, Speaker: "0", Text: "A voz reapareceu."},
			}}}},
	}
	want := "Sis 1: Primeira voz.\n\nSis 2: Segunda voz.\n\nSis: A voz reapareceu."
	if got := Render("", Blocks(entries, true)); got != want {
		t.Fatalf("um ID local foi apresentado como outra pessoa identificada: %q", got)
	}
}

func TestTimelineWithholdsSpeakerNumbersUntilEarlierSourceCallCompletes(t *testing.T) {
	t.Parallel()
	entries := []Entry{
		{ID: 1, Start: 0, End: 4, Track: "system", Pending: true},
		{ID: 2, Start: 4, End: 8, Track: "system", Text: "Resposta posterior.",
			Parts: []transcript.Part{{Start: 0, Duration: 4, Segments: []transcript.SpeechSegment{
				{Start: 0, End: 1, Speaker: "0", Text: "Resposta posterior."},
			}}}},
	}
	if got := Render("", Blocks(entries, true)); got != "Sis: Resposta posterior." {
		t.Fatalf("a chegada da rede decidiu a identidade do falante: %q", got)
	}
}

func TestTimelineRetainsContextTrimmingForAnUntimedEntryAmongTimedEntries(t *testing.T) {
	t.Parallel()
	entries := []Entry{
		{ID: 1, Start: 0, End: 2, Track: "system", Text: "Uma frase anterior.",
			Parts: []transcript.Part{{Start: 0, Duration: 2, Segments: []transcript.SpeechSegment{
				{Start: 0, End: 1, Text: "Uma frase anterior."},
			}}}},
		{ID: 2, Start: 2, End: 4, Track: "system", ContextSamples: 32000, ContextSeconds: 2,
			Text:  "Uma frase anterior. Frase nova.",
			Parts: []transcript.Part{{Start: 0, Duration: 4, Text: "Uma frase anterior. Frase nova."}}},
	}
	if got := Render("", Blocks(entries, true)); got != "Uma frase anterior. Frase nova." {
		t.Fatalf("o fallback de texto repetiu o contexto reconhecido: %q", got)
	}
}

func TestTimelineKeepsCompleteCachedTextWhenSomeWordMetadataIsMissing(t *testing.T) {
	t.Parallel()
	entries := []Entry{
		{ID: 1, Start: 0, End: 3, Track: "microphone", Text: "Primeira frase. Segunda frase.",
			Parts: []transcript.Part{{Start: 0, Duration: 3, Text: "Primeira frase. Segunda frase.",
				Segments: []transcript.SpeechSegment{
					{Start: 0, End: 1, Speaker: "0", Text: "Primeira frase."},
				}}}},
		{ID: 2, Start: 4, End: 5, Track: "system", Text: "Resposta.",
			Parts: []transcript.Part{{Start: 0, Duration: 1, Text: "Resposta.",
				Segments: []transcript.SpeechSegment{
					{Start: 0, End: 1, Speaker: "0", Text: "Resposta."},
				}}}},
	}
	got := Render("", Blocks(entries, true))
	if strings.Count(got, "Primeira frase.") != 1 || strings.Count(got, "Segunda frase.") != 1 ||
		strings.Index(got, "Segunda frase.") > strings.Index(got, "Resposta.") {
		t.Fatalf("a renderização do cache perdeu palavras sem timestamps: %q", got)
	}
	if len(entries[0].Parts[0].Segments) != 1 {
		t.Fatal("a renderização apagou os metadados do cache original")
	}
}

func TestTimelineUsesWordTimesWhenTheProviderSummaryGroupsSpeakers(t *testing.T) {
	t.Parallel()
	text := "Primeira pergunta. Outra pergunta. Resposta."
	entries := []Entry{{ID: 1, Start: 0, End: 5, Track: "system", Text: text,
		Parts: []transcript.Part{{Start: 0, Duration: 5, Text: text, Segments: []transcript.SpeechSegment{
			{Start: 0, End: 1, Speaker: "0", Text: "Primeira pergunta."},
			{Start: 2, End: 3, Speaker: "1", Text: "Resposta."},
			{Start: 4, End: 5, Speaker: "0", Text: "Outra pergunta."},
		}}}}}
	want := "Sis 1: Primeira pergunta.\n\nSis 2: Resposta.\n\nSis 1: Outra pergunta."
	if got := Render("", Blocks(entries, true)); got != want {
		t.Fatalf("o resumo agrupado substituiu a ordem dos tempos válidos: %q", got)
	}
}

func TestFailedRecognitionAddsNoDiagnosticTextToTheTimeline(t *testing.T) {
	t.Parallel()
	entries := []Entry{
		{ID: 1, Start: 0, Track: "microphone", Text: "Minha fala."},
		{ID: 2, Start: 2, Track: "microphone", Error: "max_tokens", NeedsReview: true},
		{ID: 3, Start: 4, Screenshot: true, Path: `C:\shot.png`},
		{ID: 4, Start: 4, Track: "microphone", ContextSamples: 32000, Text: "Minha fala. Continuação."},
	}
	if got := Render("", Blocks(entries, false)); got != "Minha fala.\n\nC:\\shot.png\n\nMinha fala. Continuação." {
		t.Fatalf("failure diagnostic contaminated the transcription or removed an unknown overlap: %q", got)
	}
}
