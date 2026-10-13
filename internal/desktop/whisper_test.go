package desktop

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"ai-talker/internal/config"
	"ai-talker/internal/store"
	"ai-talker/internal/whisper"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func TestWhisperSettingsSaveModelLanguageCPUAndKeepTextModels(t *testing.T) {
	var authCalls atomic.Int32
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.verify = func(context.Context, config.Settings) error { authCalls.Add(1); return nil }
	})
	test.Tap(h.d.settingsButton)
	form := h.d.settingsForm
	h.await(func() bool { return !form.loading })
	local := form.whisper
	if local.provider.Selected != bedrockProviderChoice {
		t.Fatal("a nova instalação não manteve Bedrock como padrão")
	}
	local.provider.SetSelected(whisperProviderChoice)
	tiny, _ := whisper.ModelByID("tiny")
	local.model.SetSelected(whisperModelChoice(tiny))
	local.language.SetSelected("Português")
	local.threads.SetText("4")
	local.beam.SetSelected("Rápido (1)")
	local.binary.SetText(`C:\Whisper local\whisper-cli.exe`)
	local.modelPath.SetText(`C:\Whisper local\ggml-tiny.bin`)
	if !form.quality.Disabled() {
		t.Fatal("Whisper manteve a escolha de qualidade do Voxtral ativa")
	}
	test.Tap(form.save)
	h.await(func() bool { return h.d.settingsForm == nil })
	loaded, err := config.Load(h.d.directory)
	if err != nil || loaded.TranscriptionProvider() != config.ProviderWhisper ||
		loaded.Whisper.Model != "tiny" || loaded.Whisper.Language != "pt" || loaded.Whisper.Threads != 4 ||
		loaded.Whisper.BeamSize != 1 || loaded.Processing != config.Default().Processing ||
		!strings.Contains(h.d.providerButton.hint.text, "Whisper") || h.d.qualityBars.levels != 1 ||
		authCalls.Load() != 0 {
		t.Fatalf("salvar perdeu opções locais, mudou modelos de texto ou pediu autenticação: %+v, %v", loaded, err)
	}
}

func TestWhisperMotorCheckUsesUnsavedOptionsAndShowsSuccessWithoutAWS(t *testing.T) {
	selected := make(chan config.Settings, 1)
	release := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	var authCalls atomic.Int32
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.verify = func(context.Context, config.Settings) error { authCalls.Add(1); return nil }
		deps.testWhisper = func(ctx context.Context, _ string, settings config.Settings) (whisper.CheckResult, error) {
			if _, bounded := ctx.Deadline(); !bounded {
				t.Error("o teste do motor não tem limite de tempo")
			}
			selected <- settings
			<-release
			return whisper.CheckResult{Text: "Hello. This is a short recording.", ElapsedSeconds: 1.2}, nil
		}
	})
	t.Cleanup(unblock)
	test.Tap(h.d.settingsButton)
	form := h.d.settingsForm
	h.await(func() bool { return !form.loading })
	local := form.whisper
	tiny, _ := whisper.ModelByID("tiny")
	local.model.SetSelected(whisperModelChoice(tiny))
	local.language.SetSelected("Português")
	local.threads.SetText("3")
	test.Tap(local.testButton)
	settings := waitValue(t, selected)
	if settings.Whisper.Model != "tiny" || settings.Whisper.Threads != 3 || settings.Whisper.Language != "pt" ||
		!local.busy || !local.testButton.Disabled() || !local.engineDownload.Disabled() ||
		!form.save.Disabled() || local.cancelButton.Text != "Cancelar teste" {
		t.Fatal("o teste não usou as opções em edição ou permitiu operações simultâneas")
	}
	unblock()
	h.await(func() bool { return !local.busy })
	if local.status.Importance != widget.SuccessImportance || !strings.Contains(local.status.Text, "Teste aprovado") ||
		local.testButton.Disabled() || form.save.Disabled() || local.cancelButton.Visible() ||
		local.language.Disabled() || local.threads.Disabled() || authCalls.Load() != 0 {
		t.Fatal("o teste aprovado não ficou verde, mudou o perfil ou deixou controles bloqueados")
	}
	requireAbsent(t, filepath.Join(h.d.directory, "settings.json"))
}

func TestWhisperMotorCheckShowsFailureAndAllowsRetry(t *testing.T) {
	var calls atomic.Int32
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.testWhisper = func(context.Context, string, config.Settings) (whisper.CheckResult, error) {
			if calls.Add(1) == 1 {
				return whisper.CheckResult{}, errors.New("modelo Whisper ausente: baixe um arquivo GGML")
			}
			return whisper.CheckResult{Text: "Hello. This is a short recording."}, nil
		}
	})
	test.Tap(h.d.settingsButton)
	form := h.d.settingsForm
	h.await(func() bool { return !form.loading })
	test.Tap(form.whisper.testButton)
	h.await(func() bool { return !form.whisper.busy })
	if form.whisper.status.Importance != widget.DangerImportance ||
		!strings.Contains(form.whisper.status.Text, "modelo Whisper ausente") || form.save.Disabled() {
		t.Fatal("o teste não mostrou a causa da falha em vermelho")
	}
	test.Tap(form.whisper.testButton)
	h.await(func() bool { return !form.whisper.busy })
	if calls.Load() != 2 || form.whisper.status.Importance != widget.SuccessImportance {
		t.Fatal("a falha impediu tentar novamente ou o resultado ficou vermelho após o sucesso")
	}
}

func TestWhisperMotorCheckCancellationRejectsLateSuccess(t *testing.T) {
	started := make(chan context.Context, 1)
	release := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.testWhisper = func(ctx context.Context, _ string, _ config.Settings) (whisper.CheckResult, error) {
			started <- ctx
			<-release
			return whisper.CheckResult{Text: "Resposta tardia", ElapsedSeconds: 1}, nil
		}
	})
	t.Cleanup(unblock)
	test.Tap(h.d.settingsButton)
	form := h.d.settingsForm
	h.await(func() bool { return !form.loading })
	test.Tap(form.whisper.testButton)
	ctx := waitValue(t, started)
	test.Tap(form.whisper.cancelButton)
	if ctx.Err() == nil {
		t.Fatal("cancelar não sinalizou o teste")
	}
	unblock()
	h.await(func() bool { return !form.whisper.busy })
	if form.whisper.status.Importance == widget.SuccessImportance ||
		!strings.Contains(form.whisper.status.Text, "cancelado") {
		t.Fatal("um teste cancelado recebeu aprovação tardia")
	}
}

func TestWhisperMotorCheckCloseSettingsCancelsAndKeepsNewFormUntouched(t *testing.T) {
	started := make(chan context.Context, 1)
	release := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.testWhisper = func(ctx context.Context, _ string, _ config.Settings) (whisper.CheckResult, error) {
			started <- ctx
			<-release
			return whisper.CheckResult{}, errors.New("erro antigo")
		}
	})
	t.Cleanup(unblock)
	test.Tap(h.d.settingsButton)
	old := h.d.settingsForm
	h.await(func() bool { return !old.loading })
	test.Tap(old.whisper.testButton)
	ctx := waitValue(t, started)
	old.dialog.Hide()
	test.Tap(h.d.settingsButton)
	current := h.d.settingsForm
	h.await(func() bool { return !current.loading })
	current.whisper.status.SetText("Nova configuração")
	unblock()
	h.settle()
	if ctx.Err() == nil || current.whisper.status.Text != "Nova configuração" || current.whisper.busy {
		t.Fatal("fechar as configurações deixou o teste ativo ou ele alterou o novo formulário")
	}
}

func TestWhisperMotorCheckValidatesOptionsBeforeStartingWorker(t *testing.T) {
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.testWhisper = func(context.Context, string, config.Settings) (whisper.CheckResult, error) {
			t.Error("uma configuração inválida iniciou inferência")
			return whisper.CheckResult{}, nil
		}
	})
	test.Tap(h.d.settingsButton)
	form := h.d.settingsForm
	h.await(func() bool { return !form.loading })
	form.whisper.threads.SetText("não é um número")
	test.Tap(form.whisper.testButton)
	h.settle()
	if form.whisper.busy || form.whisper.status.Importance != widget.DangerImportance ||
		!strings.Contains(form.whisper.status.Text, "threads") {
		t.Fatal("as opções inválidas não produziram uma falha recuperável")
	}
}

func TestWhisperProviderSelectorPersistsAndUpdatesAllTabs(t *testing.T) {
	h := newHarness(t, nil, nil)
	first := h.d.document
	second := h.d.addDocument()
	h.d.setTranscriptionProvider(config.ProviderWhisper)
	h.settle()
	for _, doc := range []*document{first, second} {
		if !strings.Contains(doc.providerButton.hint.text, "Whisper") || doc.qualityBars.levels != 2 {
			t.Fatal("trocar o motor não atualizou os controles de todas as abas")
		}
	}
	settings, err := config.Load(h.d.directory)
	if err != nil || settings.TranscriptionProvider() != config.ProviderWhisper {
		t.Fatal("o seletor ao lado da qualidade não persistiu a escolha")
	}
	h.d.setTranscriptionProvider(config.ProviderBedrock)
	h.settle()
	if !strings.Contains(first.providerButton.hint.text, "AWS Bedrock") || first.qualityBars.levels != 4 {
		t.Fatal("voltar ao Bedrock não restaurou o modo de qualidade")
	}
}

func TestWhisperDownloadsUseSelectedModelAndKeepUIResponsive(t *testing.T) {
	started := make(chan string, 2)
	release := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.downloadWhisperModel = func(ctx context.Context, directory, model, source string, progress func(whisper.DownloadProgress)) (string, error) {
			started <- model
			progress(whisper.DownloadProgress{Message: "Baixando modelo", Bytes: 1 << 20, Total: 2 << 20})
			select {
			case <-release:
				return whisper.ModelPath(directory, model), nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
	})
	t.Cleanup(unblock)
	test.Tap(h.d.settingsButton)
	form := h.d.settingsForm
	h.await(func() bool { return !form.loading })
	small, _ := whisper.ModelByID("small")
	form.whisper.model.SetSelected(whisperModelChoice(small))
	test.Tap(form.whisper.modelDownload)
	if waitValue(t, started) != "small" || !form.whisper.busy || !form.save.Disabled() {
		t.Fatal("o download não usou o modelo selecionado ou permitiu salvar no meio da instalação")
	}
	h.await(func() bool { return form.whisper.progress.Value == 0.5 })
	form.whisper.language.SetSelected("Português")
	unblock()
	h.await(func() bool { return !form.whisper.busy })
	if form.save.Disabled() || form.whisper.modelPath.Text != whisper.ModelPath(h.d.directory, "small") ||
		form.whisper.language.Selected != "Português" || form.whisper.cancelButton.Visible() {
		t.Fatal("o download bloqueou a interface ou perdeu uma edição feita durante a espera")
	}
	requireAbsent(t, filepath.Join(h.d.directory, "settings.json"))
}

func TestWhisperCloseSettingsCancelsDownloadAndRejectsItsLateCompletion(t *testing.T) {
	started := make(chan context.Context, 1)
	release := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.downloadWhisperEngine = func(ctx context.Context, directory string, progress func(whisper.DownloadProgress)) (string, error) {
			started <- ctx
			<-release
			progress(whisper.DownloadProgress{Message: "Resultado antigo", Bytes: 1, Total: 1})
			return whisper.BinaryPath(directory), nil
		}
	})
	t.Cleanup(unblock)
	test.Tap(h.d.settingsButton)
	old := h.d.settingsForm
	h.await(func() bool { return !old.loading })
	test.Tap(old.whisper.engineDownload)
	ctx := waitValue(t, started)
	old.dialog.Hide()
	if ctx.Err() == nil || h.d.settingsForm != nil {
		t.Fatal("fechar as configurações deixou um download em andamento")
	}
	test.Tap(h.d.settingsButton)
	current := h.d.settingsForm
	h.await(func() bool { return !current.loading })
	current.whisper.binary.SetText("motor que selecionei agora.exe")
	unblock()
	h.settle()
	if current.whisper.binary.Text != "motor que selecionei agora.exe" ||
		strings.Contains(current.whisper.status.Text, "Resultado antigo") {
		t.Fatal("um download antigo alterou o novo formulário")
	}
}

func TestWhisperBlockedDownloadAllowsLocalFileAndRetry(t *testing.T) {
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.downloadWhisperModel = func(context.Context, string, string, string, func(whisper.DownloadProgress)) (string, error) {
			return "", errors.New("A rede bloqueou o download. Selecione um arquivo local existente.")
		}
	})
	test.Tap(h.d.settingsButton)
	form := h.d.settingsForm
	h.await(func() bool { return !form.loading })
	test.Tap(form.whisper.modelDownload)
	h.await(func() bool { return !form.whisper.busy })
	if form.whisper.modelDownload.Disabled() || form.save.Disabled() || !strings.Contains(form.whisper.status.Text, "bloqueou") {
		t.Fatal("a falha de rede bloqueou a configuração ou ocultou a alternativa local")
	}
	form.whisper.model.SetSelected(customWhisperChoice)
	form.whisper.modelPath.SetText(`C:\Modelos\meu-whisper.bin`)
	form.whisper.provider.SetSelected(whisperProviderChoice)
	test.Tap(form.save)
	h.await(func() bool { return h.d.settingsForm == nil })
	var saved config.Settings
	saved = config.Default()
	if err := store.ReadJSON(filepath.Join(h.d.directory, "settings.json"), &saved); err != nil ||
		saved.Whisper.Model != "custom" || saved.Whisper.ModelPath != `C:\Modelos\meu-whisper.bin` {
		t.Fatalf("a escolha de arquivo local não foi preservada: %v", err)
	}
}
