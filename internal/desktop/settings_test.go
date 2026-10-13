package desktop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"ai-talker/internal/config"
	"ai-talker/internal/recorder"
	"ai-talker/internal/store"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func TestSettingsWritingFieldsUseRemainingDialogSpace(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.d.window.Resize(fyne.NewSize(700, 1000))
	test.Tap(h.d.settingsButton)
	form := h.d.settingsForm
	h.await(func() bool { return !form.loading })
	for _, section := range []struct {
		index int
		field *widget.Entry
	}{
		{1, form.glossary},
		{4, form.mergePrompt},
	} {
		form.sections.SelectIndex(section.index)
		if section.index == 4 {
			form.instructions.sections.SelectIndex(1)
		}
		form.dialog.Resize(fyne.NewSize(600, 760))
		before := section.field.Size().Height
		form.dialog.Resize(fyne.NewSize(600, 930))
		after := section.field.Size().Height
		if before < section.field.MinSize().Height || after-before < 100 {
			t.Fatalf("o campo da seção %d não aproveita o espaço: antes=%v depois=%v mínimo=%v",
				section.index, before, after, section.field.MinSize())
		}
	}
}

func TestSettingsSaveAllOriginalFieldsAndNativeMicrophone(t *testing.T) {
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.microphones = func() ([]recorder.Device, error) {
			return []recorder.Device{{ID: 3, Name: "Microfone USB"}}, nil
		}
		deps.profiles = func() ([]string, error) { return []string{"desktop-test"}, nil }
	})
	test.Tap(h.d.settingsButton)
	form := h.d.settingsForm
	h.await(func() bool { return !form.loading })
	form.profile.SetText("desktop-test")
	form.region.SetText(" us-west-2 ")
	form.quality.SetSelected(fastChoice)
	form.microphone.SetSelected("Microfone USB (3)")
	form.chunk.SetText("45")
	form.glossary.SetText(" AWS, português e TypeScript ")
	test.Tap(form.save)
	h.await(func() bool { return h.d.settingsForm == nil })
	settings, err := config.Load(h.d.directory)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Profile != "desktop-test" || settings.Region != "us-west-2" || settings.Quality != "fast" {
		t.Fatalf("as configurações AWS não foram salvas: %+v", settings)
	}
	if settings.ChunkSeconds != 45 || settings.Glossary != "AWS, português e TypeScript" || settings.Device() != 3 {
		t.Fatalf("as configurações de áudio não foram salvas: %+v", settings)
	}
	if settings.RecorderBackend != "winmm" || settings.OverlapSeconds != config.Default().OverlapSeconds {
		t.Fatal("salvar o formulário perdeu as configurações fora dos campos editáveis")
	}
	if h.d.qualityBars.levels != 2 || h.d.settings.Quality != "fast" {
		t.Fatal("a interface não adotou as configurações salvas")
	}
}

func TestSettingsRejectInvalidValuesWithoutBlockingForm(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*settingsForm)
	}{
		{name: "região vazia", change: func(f *settingsForm) { f.region.SetText(" ") }},
		{name: "parte longa", change: func(f *settingsForm) { f.chunk.SetText("301") }},
		{name: "parte curta", change: func(f *settingsForm) { f.chunk.SetText("14") }},
		{name: "duração inválida", change: func(f *settingsForm) { f.chunk.SetText("abc") }},
		{name: "glossário longo", change: func(f *settingsForm) { f.glossary.SetText(strings.Repeat("á", 4001)) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, nil, nil)
			test.Tap(h.d.settingsButton)
			form := h.d.settingsForm
			h.await(func() bool { return !form.loading })
			tc.change(form)
			test.Tap(form.save)
			if !form.errorLabel.Visible() || form.save.Disabled() || h.d.settingsForm != form {
				t.Fatal("a configuração inválida não deixou o formulário disponível para correção")
			}
			requireAbsent(t, filepath.Join(h.d.directory, "settings.json"))
			if h.d.settings.Region != config.Default().Region || h.d.settings.ChunkSeconds != 300 {
				t.Fatal("a configuração inválida substituiu as preferências atuais")
			}
		})
	}
}

func TestSettingsKeepUnsavedAndBusyAPIKeysInTheOpenForm(t *testing.T) {
	for _, tc := range []struct {
		name string
		form func(*settingsForm) *apiForm
		busy bool
	}{
		{name: "chave no gerenciador", form: func(f *settingsForm) *apiForm { return f.audioAPI.manager }},
		{name: "operação no gerenciador", form: func(f *settingsForm) *apiForm { return f.audioAPI.manager }, busy: true},
		{name: "chave de texto", form: func(f *settingsForm) *apiForm { return f.llm.api }},
		{name: "operação de texto", form: func(f *settingsForm) *apiForm { return f.llm.api }, busy: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, nil, nil)
			h.d.openSettings()
			form := h.d.settingsForm
			h.await(func() bool { return !form.loading })
			api := tc.form(form)
			if tc.busy {
				api.busy = true
				t.Cleanup(func() { api.busy = false })
			} else {
				api.key.SetText("fictitious-pending-key")
			}
			test.Tap(form.save)
			h.settle()
			if h.d.settingsForm != form || !form.errorLabel.Visible() ||
				!strings.Contains(form.errorLabel.Text, "Conclua o salvamento da chave") {
				t.Fatal("Salvar fechou a configuração com uma chave pendente ou uma operação em andamento")
			}
			if !tc.busy && api.key.Text != "fictitious-pending-key" {
				t.Fatal("a chave pendente foi descartada")
			}
			if strings.Contains(form.errorLabel.Text, "fictitious-pending-key") {
				t.Fatal("a mensagem expôs a chave")
			}
		})
	}
}

func TestSettingsEnumerationFailureKeepsSystemDefaultUsable(t *testing.T) {
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.microphones = func() ([]recorder.Device, error) { return nil, errors.New("dispositivo indisponível") }
		deps.profiles = func() ([]string, error) { return nil, errors.New("perfil ilegível") }
	})
	test.Tap(h.d.settingsButton)
	form := h.d.settingsForm
	h.await(func() bool { return !form.loading })
	if form.microphone.Disabled() || form.save.Disabled() || !form.errorLabel.Visible() {
		t.Fatal("a falha de listagem bloqueou as opções restantes")
	}
	form.profile.SetText("perfil-manual")
	test.Tap(form.save)
	h.await(func() bool { return h.d.settingsForm == nil })
	if h.d.settings.Device() != -1 || h.d.settings.Profile != "perfil-manual" {
		t.Fatal("o perfil manual ou microfone padrão não foram aplicados")
	}
}

func TestConnectUsesTypedProfileAndRegionAndShowsRetry(t *testing.T) {
	checks := make(chan config.Settings, 2)
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.verify = func(_ context.Context, settings config.Settings) error {
			checks <- settings
			if settings.Profile == "perfil-sem-login" {
				return errors.New("renove o login deste perfil")
			}
			return nil
		}
	})
	test.Tap(h.d.settingsButton)
	form := h.d.settingsForm
	h.await(func() bool { return !form.loading })
	form.profile.SetText("perfil-sem-login")
	form.region.SetText("us-west-2")
	test.Tap(form.connect)
	h.await(func() bool { return len(checks) > 0 })
	first := waitValue(t, checks)
	h.await(func() bool { return !form.connect.Disabled() })
	if first.Profile != "perfil-sem-login" || first.Region != "us-west-2" ||
		!strings.Contains(form.connection.Text, "renove") || h.d.awsIndicator.color != recordingColor {
		t.Fatal("a tentativa não usou o perfil digitado ou não permitiu corrigir o login")
	}
	form.profile.SetText("perfil-com-login")
	test.Tap(form.connect)
	h.await(func() bool { return len(checks) > 0 })
	second := waitValue(t, checks)
	h.await(func() bool { return h.d.awsLabel.Text == "AWS conectada" })
	if second.Profile != "perfil-com-login" || !strings.Contains(form.connection.Text, "perfil-com-login") {
		t.Fatal("a nova conexão não adotou o perfil corrigido")
	}
	settings, err := config.Load(h.d.directory)
	if err != nil || settings.Profile != "perfil-com-login" || settings.Region != "us-west-2" {
		t.Fatalf("a seleção de conexão não foi salva: %+v, %v", settings, err)
	}
}

func TestSettingsPreserveThemeAndSaveMergeModelPromptAndEffort(t *testing.T) {
	h := newHarness(t, nil, nil)
	test.Tap(h.d.settingsButton)
	form := h.d.settingsForm
	h.await(func() bool { return !form.loading })
	h.d.setTheme(config.ThemeLight)
	h.settle()
	form.mergeModel.SetText("openai.gpt-oss-120b-1:0")
	form.mergeReasoning.SetSelected(lowReasoning)
	form.mergePrompt.SetText("Combine em uma mensagem objetiva.")
	test.Tap(form.save)
	h.await(func() bool { return h.d.settingsForm == nil })
	settings, err := config.Load(h.d.directory)
	if err != nil || settings.Appearance.Theme != config.ThemeLight ||
		settings.Merge.ModelID != "openai.gpt-oss-120b-1:0" ||
		settings.Merge.Prompt != "Combine em uma mensagem objetiva." || settings.Merge.ReasoningEffort != "low" {
		t.Fatalf("salvar perdeu o tema ou as opções de mesclagem: %+v, %v", settings, err)
	}
}

func TestSettingsUnavailableMicrophonePreservesChoiceUntilExplicitReplacement(t *testing.T) {
	h := newHarness(t, nil, nil)
	device := 99
	h.d.settings.Microphone = &device
	test.Tap(h.d.settingsButton)
	form := h.d.settingsForm
	h.await(func() bool { return !form.loading })
	if form.microphone.Selected != unavailableDevice {
		t.Fatal("um microfone removido foi substituído pelo padrão silenciosamente")
	}
	test.Tap(form.save)
	h.await(func() bool { return h.d.settingsForm == nil })
	if h.d.settings.Device() != device || !h.d.unavailableMicrophone {
		t.Fatal("salvar outras configurações perdeu a escolha ou disponibilidade do microfone")
	}
	test.Tap(h.d.recordButton)
	h.settle()
	if h.d.capture != captureIdle || !strings.Contains(h.d.document.errorLabel.Text, "Dispositivo indisponível") {
		t.Fatal("a escolha removida iniciou captura sem pedir uma substituição explícita")
	}
}

func TestSettingsWriteFailureRestoresPreferencesAndEnablesRetry(t *testing.T) {
	h := newHarness(t, nil, nil)
	path := filepath.Join(h.d.directory, "settings.json")
	if err := os.MkdirAll(filepath.Join(path, "bloqueio"), 0o700); err != nil {
		t.Fatal(err)
	}
	test.Tap(h.d.settingsButton)
	form := h.d.settingsForm
	h.await(func() bool { return !form.loading })
	form.region.SetText("us-west-2")
	test.Tap(form.save)
	h.await(func() bool { return form.errorLabel.Visible() })
	if form.save.Disabled() || h.d.settings.Region != config.Default().Region || h.d.settingsForm != form {
		t.Fatal("a falha de escrita não recuperou as preferências e o formulário")
	}
}

func TestSettingsClosedWhileLoadingDoesNotUpdateFutureDialog(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	releaseOptions := sync.OnceFunc(func() { close(release) })
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.profiles = func() ([]string, error) {
			started <- struct{}{}
			<-release
			return []string{"perfil-antigo"}, nil
		}
	})
	t.Cleanup(releaseOptions)
	test.Tap(h.d.settingsButton)
	form := h.d.settingsForm
	waitValue(t, started)
	form.dialog.Hide()
	if h.d.settingsForm != nil {
		t.Fatal("fechar o diálogo não liberou sua instância")
	}
	releaseOptions()
	h.settle()
	if h.d.settingsForm != nil || h.d.errorLabel.Visible() {
		t.Fatal("a listagem antiga reabriu ou modificou o diálogo fechado")
	}
}

func TestCredentialResultsBelongToCurrentAuthGeneration(t *testing.T) {
	type check struct {
		ctx     context.Context
		profile string
	}
	checks := make(chan check, 2)
	release := make(chan struct{})
	releaseOld := sync.OnceFunc(func() { close(release) })
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.verify = func(ctx context.Context, settings config.Settings) error {
			checks <- check{ctx: ctx, profile: settings.Profile}
			if settings.Profile == "antigo" {
				<-release
				return errors.New("login antigo falhou")
			}
			return nil
		}
	})
	t.Cleanup(releaseOld)
	h.d.settings.Profile = "antigo"
	h.d.checkAWS()
	old := waitValue(t, checks)
	h.d.settings.Profile = "novo"
	h.d.checkAWS()
	current := waitValue(t, checks)
	h.await(func() bool { return h.d.awsLabel.Text == "AWS conectada" })
	releaseOld()
	h.settle()
	if old.ctx.Err() == nil || current.profile != "novo" || h.d.ctx.Err() != nil {
		t.Fatal("alterar o perfil deve cancelar somente a verificação antiga")
	}
	if h.d.awsLabel.Text != "AWS conectada" || h.d.errorLabel.Visible() {
		t.Fatal("a verificação antiga alterou a conexão atual")
	}
}

func TestPinUsesNativeOperationAndPersistsWithoutChangingOtherSettings(t *testing.T) {
	pins := make(chan bool, 1)
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.setPinned = func(pinned bool) error { pins <- pinned; return nil }
	})
	h.d.settings.Region = "us-west-2"
	test.Tap(h.d.pinButton)
	if pinned := waitValue(t, pins); !pinned {
		t.Fatal("a operação nativa recebeu o estado incorreto")
	}
	h.settle()
	if h.d.pinButton.Text != "" || h.d.pinButton.hint.text != "Desafixar janela" || !h.d.settings.Pinned {
		t.Fatal("fixar não atualizou a interface")
	}
	var saved config.Settings
	if err := store.ReadJSON(filepath.Join(h.d.directory, "settings.json"), &saved); err != nil {
		t.Fatal(err)
	}
	if !saved.Pinned || saved.Region != "us-west-2" || saved.RecorderBackend != "winmm" {
		t.Fatal("fixar perdeu configurações ou não persistiu")
	}
}

func TestDelayedCredentialFailureDoesNotReplaceCurrentTranscriptStatus(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	releaseCheck := sync.OnceFunc(func() { close(release) })
	defer releaseCheck()
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.verify = func(context.Context, config.Settings) error {
			started <- struct{}{}
			<-release
			return errors.New("verificação antiga expirou")
		}
	})
	h.d.checkAWS()
	waitValue(t, started)
	h.d.startJob("audio-atual.wav")
	h.await(func() bool { return !h.d.busy && h.d.editor.Text == "Texto desta gravação." })
	status := h.d.status.Text
	releaseCheck()
	h.settle()
	if h.d.errorLabel.Visible() || h.d.status.Text != status {
		t.Fatal("uma verificação AWS atrasada alterou o resultado da transcrição atual")
	}
	if result := readResult(t, h.d.latestPath()); result.Text != h.d.editor.Text {
		t.Fatal("a verificação antiga alterou a última transcrição salva")
	}
}

func TestPinFailureIsNonmodalAndRestoresPreviousState(t *testing.T) {
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.setPinned = func(bool) error { return errors.New("janela indisponível") }
	})
	test.Tap(h.d.pinButton)
	h.await(func() bool { return h.d.errorLabel.Visible() })
	if h.d.settings.Pinned || h.d.pinButton.hint.text != "Fixar janela" || h.d.clearButton.Disabled() {
		t.Fatal("a falha de fixação deixou um estado incorreto ou controles indisponíveis")
	}
}

func TestAvailableProfilesReadsNamesWithoutCredentialsExecution(t *testing.T) {
	directory := t.TempDir()
	configuration := filepath.Join(directory, "config")
	credentials := filepath.Join(directory, "credentials")
	if err := os.WriteFile(configuration, []byte(
		"[default]\nregion=us-east-1\n"+
			"[profile desktop]\ncredential_process=never-execute-this\n"+
			"[sso-session sessão]\nsso_region=us-east-1\n"+
			"[services custom]\nendpoint_url=http://example.invalid\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credentials, []byte("[desktop]\n[offline]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWS_CONFIG_FILE", configuration)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", credentials)
	profiles, err := availableProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(profiles, []string{"default", "desktop", "offline"}) {
		t.Fatalf("foram listadas seções que não são perfis: %v", profiles)
	}
}

func TestSettingsSearchKeepsTheTypingFocusAndNavigatesToTheResult(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.d.openSettingsSection("Áudio")
	form := h.d.settingsForm
	h.await(func() bool { return !form.loading })
	sections := form.sections
	if sections.tree.Root != "" {
		t.Fatal("a árvore acrescentou uma raiz vazia antes das categorias")
	}
	canvas := h.d.window.Canvas()
	overlays := len(canvas.Overlays().List())
	canvas.Focus(sections.search)
	query := "Identificação do executável"
	for _, character := range query {
		if canvas.Focused() != sections.search {
			t.Fatal("os resultados tiraram o foco da busca antes de terminar a consulta")
		}
		canvas.Focused().TypedRune(character)
		if len(canvas.Overlays().List()) != overlays {
			t.Fatal("os resultados abriram outra camada de foco sobre a caixa de busca")
		}
	}
	if sections.search.Text != query || len(sections.searchResult) != 1 {
		t.Fatal("a consulta completa não encontrou a identificação", sections.search.Text, sections.searchResult)
	}
	sections.searchList.Select(0)
	if sections.selectedID != "about" {
		t.Fatal("selecionar o resultado não abriu Sobre")
	}
	sections.search.SetText("")
	if len(sections.searchResult) != 0 {
		t.Fatal("apagar a consulta não limpou os resultados")
	}
}

func TestSettingsTreeStaysReadableInNarrowWindowsAcrossPages(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.d.window.Resize(fyne.NewSize(480, 900))
	h.d.openSettingsSection("Áudio")
	form := h.d.settingsForm
	h.await(func() bool { return !form.loading })
	for _, width := range []float32{480, 320, 1100} {
		h.d.window.Resize(fyne.NewSize(width, 900))
		for _, page := range []string{"audio", "llm", "connections/api", "about"} {
			form.sections.selectID(page)
			form.dialog.Resize(dialogSize(h.d.window))
			h.settle()
			if treeWidth := form.sections.tree.Size().Width; treeWidth < 200 {
				t.Fatalf("árvore ilegível na página %s com janela %v: largura %v",
					page, width, treeWidth)
			}
			if h.d.window.Canvas().Size().Width != width {
				t.Fatal("navegar em Configurações alterou a largura da janela")
			}
		}
	}
}
