package desktop

import (
	"strings"
	"testing"
	"weak"

	"ai-talker/internal/config"
	"ai-talker/internal/locale"
	"ai-talker/internal/processing"
	"ai-talker/internal/transcript"

	"fyne.io/fyne/v2/widget"
)

func TestInterfaceLanguageChangesImmediatelyAndPreservesSettingsDrafts(t *testing.T) {
	h := newHarness(t, nil, nil)
	d := h.d
	doc := d.document
	doc.editor.SetText("Salvar · Grupo 1 · do not translate my text.")
	doc.processEditor.SetText("Settings · Processar · provider output unchanged.")
	d.openSettings()
	form := d.settingsForm
	h.await(func() bool { return !form.loading })
	form.sections.selectID("visual")
	form.region.SetText("us-west-2")
	form.glossary.SetText("Draft glossary · Português")
	originalOptions := doc.processOptions
	setUISelected(form.interfaceLanguage, "Inglês")
	if d.settings.InterfaceLocale() != config.InterfaceEnglish || locale.Current() != locale.English ||
		d.settingsForm != form || form.sections.selectedID != "visual" {
		t.Fatal("language did not apply immediately or recreated/reset the settings page")
	}
	if doc.editor.Text != "Salvar · Grupo 1 · do not translate my text." ||
		doc.processEditor.Text != "Settings · Processar · provider output unchanged." ||
		form.region.Text != "us-west-2" || form.glossary.Text != "Draft glossary · Português" ||
		doc.processOptions != originalOptions {
		t.Fatal("interface translation changed drafts, content or model options")
	}
	if !strings.Contains(doc.editor.PlaceHolder, "transcription") ||
		doc.transcriptName.Text != "Transcription" || d.recordState.Text != "Ready to start" {
		t.Fatal("visible interface text/placeholder did not translate", doc.transcriptName.Text, d.recordState.Text)
	}
	h.await(func() bool {
		saved, err := config.Load(d.directory)
		return err == nil && saved.InterfaceLocale() == config.InterfaceEnglish
	})
	setUISelected(form.interfaceLanguage, "Português (Brasil)")
	if doc.transcriptName.Text != "Transcrição" || form.region.Text != "us-west-2" {
		t.Fatal("switching back did not restore Portuguese or changed the draft")
	}
}

func TestLocalizedSelectorsKeepIndependentSourceValuesAndUserNames(t *testing.T) {
	h := newHarness(t, nil, nil)
	d := h.d
	locale.Set(locale.English)
	t.Cleanup(func() { locale.Set(locale.Portuguese) })
	video := widget.NewSelect([]string{"Baixa", "Média", "Alta"}, nil)
	reasoning := widget.NewSelect([]string{"Baixo", "Médio", "Alto"}, nil)
	setUISelected(video, "Média")
	setUISelected(reasoning, "Médio")
	d.localization.selector(video)
	d.localization.selector(reasoning)
	if video.Selected != "Medium" || reasoning.Selected != "Medium" ||
		uiSelected(video) != "Média" || uiSelected(reasoning) != "Médio" {
		t.Fatal("identical English labels lost their distinct source values")
	}
	custom := widget.NewSelect([]string{"Salvar", "Livre"}, nil)
	preserveUIChoices(custom, []string{"Salvar"})
	setUISelected(custom, "Salvar")
	d.localization.selector(custom)
	if custom.Selected != "Salvar" || custom.Options[0] != "Salvar" || custom.Options[1] != "Free" {
		t.Fatal("custom selector name was translated or application option was not", custom.Options)
	}
	var changed string
	custom.OnChanged = func(value string) { changed = value }
	d.localization.selector(custom)
	custom.SetSelected("Free")
	if changed != "Livre" || uiSelected(custom) != "Livre" {
		t.Fatal("localized selection callback did not keep the source contract", changed)
	}
}

func TestAutomaticGroupNamesTranslateAndCustomLegacyNamesRemain(t *testing.T) {
	h := newHarness(t, nil, nil)
	d := h.d
	automatic := d.addGroup()
	custom := d.addGroup()
	if err := d.renameGroup(custom.group, "Grupo 12"); err != nil {
		t.Fatal(err)
	}
	legacy := d.addGroup()
	delete(d.groupAutomaticNames, legacy.group)
	d.groupNames[legacy.group] = "Grupo 30"
	d.changeInterfaceLanguage(config.InterfaceEnglish)
	if d.groupName(automatic.group) != "Group 2" || d.groupName(custom.group) != "Grupo 12" ||
		d.groupName(legacy.group) != "Grupo 30" || d.documents[0].item.Text != "Tab 1" {
		t.Fatal("automatic name metadata changed a custom/ambiguous name", d.groupNames)
	}
	if err := d.saveWorkspace(); err != nil {
		t.Fatal(err)
	}
	snapshot := d.snapshotWorkspace()
	if snapshot.GroupAutomaticNames[automatic.group] != "Grupo 2" ||
		snapshot.GroupAutomaticNames[custom.group] != "" || snapshot.GroupAutomaticNames[legacy.group] != "" {
		t.Fatal("snapshot did not retain safe automatic-name markers")
	}
	d.changeInterfaceLanguage(config.InterfacePortuguese)
	if d.groupName(automatic.group) != "Grupo 2" || d.groupName(custom.group) != "Grupo 12" {
		t.Fatal("automatic/custom names changed when returning to Portuguese")
	}
}

func TestEnglishSettingsSaveKeepsRecognitionAndModelChoices(t *testing.T) {
	h := newHarness(t, nil, nil)
	d := h.d
	t.Cleanup(func() { locale.Set(locale.Portuguese) })
	d.settings.Cloud.Language = "pt-BR"
	d.settings.Instructions = []processing.Instruction{{ID: "custom-name", Name: "Salvar", Prompt: "Do not translate."}}
	d.openSettings()
	form := d.settingsForm
	h.await(func() bool { return !form.loading })
	setUISelected(form.interfaceLanguage, "Inglês")
	updated, err := d.settingsFromForm(form)
	if err != nil {
		t.Fatal(err)
	}
	if updated.InterfaceLocale() != config.InterfaceEnglish || updated.Cloud.Language != "pt-BR" ||
		updated.Appearance.Layout != d.settings.Appearance.Layout ||
		updated.Appearance.RecordingSize != d.settings.Appearance.RecordingSize ||
		updated.CloudModelID() != d.settings.CloudModelID() ||
		updated.Instructions[0].Name != "Salvar" || updated.Instructions[0].Prompt != "Do not translate." {
		t.Fatal("English UI changed source preference values or instruction content")
	}
}

func TestInterfaceLanguageRefreshesComposedHintsPreservingIDsAndUserNames(t *testing.T) {
	h := newHarness(t, nil, nil)
	d, doc := h.d, h.d.document
	doc.promptSteps = []promptStep{{
		ID: 1, Conversation: 1,
		Options: &processing.Options{Mode: processing.ModeFree, ModelID: "my-model/Salvar",
			InstructionName: "Configurações", ReasoningEffort: "high"},
		PreviousOptions: []processing.Options{{ModelID: "previous-model", Mode: processing.ModeFree}},
	}}
	doc.promptCursor = 0
	doc.lastResult = &transcript.Result{Parts: []transcript.Part{
		{Engine: "my-engine", DetectedSpeech: true, NeedsReview: true,
			Recovery: &transcript.RecoveryAttempt{Status: "failed", Error: "AccessDeniedException: raw"}},
		{Engine: "my-engine", DetectedSpeech: false},
	}}
	d.changeInterfaceLanguage(config.InterfaceEnglish)
	if !strings.Contains(doc.promptLabel.hint.text, "Sent with my-model/Salvar") ||
		!strings.Contains(doc.promptLabel.hint.text, "Configurações") ||
		!strings.Contains(doc.promptLabel.hint.text, "reasoning high") ||
		!strings.Contains(doc.promptLabel.hint.text, "Previous version: previous-model") ||
		!strings.Contains(doc.transcriptName.hint.text, "Whisper tried and failed: AccessDeniedException: raw") ||
		!strings.Contains(doc.transcriptName.hint.text, "\nSegment 2 · no speech") {
		t.Fatal("composed hint was partly untranslated or modified an ID/name", doc.promptLabel.hint.text, doc.transcriptName.hint.text)
	}
	d.changeInterfaceLanguage(config.InterfacePortuguese)
	if !strings.Contains(doc.promptLabel.hint.text, "Enviado com my-model/Salvar") ||
		!strings.Contains(doc.transcriptName.hint.text, "\nTrecho 2 · sem fala") {
		t.Fatal("composed hint did not restore Portuguese")
	}
}

func TestInterfaceLocalizationPrunesDisposedFormsAndExpiredUndoKeepsHiddenTabs(t *testing.T) {
	h := newHarness(t, nil, nil)
	d, hidden := h.d, h.d.document
	hidden.editor.SetText("Salvar user text unchanged.")
	d.addDocument()
	d.localizeInterface()
	if _, exists := d.localization.values[hidden.transcriptName]; !exists ||
		!d.localization.protected[hidden.transcriptPreview] {
		t.Fatal("hidden tab lost its presentation cache or user-content protection")
	}

	d.openSettings()
	form := d.settingsForm
	h.await(func() bool { return !form.loading })
	d.localizeInterface()
	if _, exists := d.localization.values[form.interfaceLanguage]; !exists {
		t.Fatal("settings selector was not tracked before closing")
	}
	form.dialog.Hide()
	d.localizeInterface()
	if _, exists := d.localization.values[form.interfaceLanguage]; exists {
		t.Fatal("closed settings form remains strongly referenced by the text cache")
	}
	if _, exists := d.localization.selectors[form.interfaceLanguage]; exists {
		t.Fatal("closed settings selector remains strongly referenced by its callback cache")
	}

	d.closeDocument(hidden.item)
	d.localizeInterface()
	if len(d.closedTabs) != 1 {
		t.Fatal("test tab did not enter the closure sequence")
	}
	if _, exists := d.localization.values[hidden.transcriptName]; !exists ||
		!d.localization.protected[hidden.transcriptPreview] {
		t.Fatal("Undo lost the hidden tab's cache or user-content protection")
	}
	d.changeInterfaceLanguage(config.InterfaceEnglish)
	if hidden.transcriptName.Text != "Transcription" || hidden.editor.Text != "Salvar user text unchanged." ||
		uiSelected(hidden.processMode) != "Teams" {
		t.Fatal("retained Undo widgets lost their translated presentation or source values")
	}
	d.changeInterfaceLanguage(config.InterfacePortuguese)
	d.forgetClosedDocument()
	d.localizeInterface()
	if _, exists := d.localization.values[hidden.transcriptName]; exists {
		t.Fatal("expired Undo tab remains strongly referenced by the text cache")
	}
	if _, exists := d.localization.selectors[hidden.processMode]; exists ||
		d.localization.protected[hidden.transcriptPreview] {
		t.Fatal("expired Undo tab retains callback or protected-content cache entries")
	}
	// form and hidden deliberately remain reachable in this test. The checks
	// prove explicit cache removal without relying on GC timing.
}

func TestLocalizedChoiceCachesUseWeakKeysAndPruneUnavailableEntries(t *testing.T) {
	locale.Set(locale.English)
	t.Cleanup(func() { locale.Set(locale.Portuguese) })
	selection := widget.NewSelect([]string{"Salvar", "Livre"}, nil)
	preserveUIChoices(selection, []string{"Salvar"})
	setUISelected(selection, "Salvar")
	key := weak.Make(selection)
	if _, exists := localizedChoices.Load(key); !exists {
		t.Fatal("source choices are not stored with a typed weak widget key")
	}
	if _, exists := preservedChoices.Load(key); !exists {
		t.Fatal("custom choices are not stored with a typed weak widget key")
	}
	// A zero weak key is always unavailable, providing a deterministic cleanup
	// case rather than waiting for a finalizer or a garbage collection cycle.
	expired := weak.Pointer[widget.Select]{}
	localizedChoices.Store(expired, map[string]string{"Free": "Livre"})
	preservedChoices.Store(expired, map[string]bool{"Salvar": true})
	pruneLocalizedChoices()
	if _, exists := localizedChoices.Load(expired); exists {
		t.Fatal("unavailable source-choice entry was not removed")
	}
	if _, exists := preservedChoices.Load(expired); exists {
		t.Fatal("unavailable custom-choice entry was not removed")
	}
	if _, exists := localizedChoices.Load(key); !exists || uiSelected(selection) != "Salvar" ||
		selection.Options[0] != "Salvar" || selection.Options[1] != "Free" {
		t.Fatal("pruning removed or changed a live selector's source/custom identities")
	}
}
