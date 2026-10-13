package desktop

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ai-talker/internal/config"
	"ai-talker/internal/transcript"

	"fyne.io/fyne/v2/test"
)

func TestSelectingTranscribeWithoutBucketOffersSetupBeforeChangingTheModel(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.d.editor.SetText("Texto preservado.")
	h.d.setCloudModel(config.TranscribeBatch, false)
	form := h.d.transcribeSetup
	if form == nil || h.d.settings.CloudModelID() != config.VoxtralSmall ||
		h.d.editor.Text != "Texto preservado." {
		t.Fatal("escolher o modelo incompleto mudou preferências/texto antes de oferecer preparação")
	}
	test.Tap(form.configure)
	settings := h.d.settingsForm
	h.await(func() bool { return !settings.loading })
	if h.d.transcribeSetup != nil || settings.sections.Selected().Text != "AWS" ||
		settings.quality.Selected != cloudChoiceName(config.TranscribeBatch) ||
		h.d.window.Canvas().Focused() != settings.cloudUseBucket {
		t.Fatal("Configurar S3 não abriu o uso do bucket em Conexões → AWS")
	}
	settings.cloudUseBucket.SetChecked(true)
	settings.cloudBucket.SetText("bucket-sintetico")
	test.Tap(settings.save)
	h.await(func() bool { return h.d.settingsForm == nil })
	saved, err := config.Load(h.d.directory)
	if err != nil || saved.Cloud.ModelID != config.TranscribeBatch ||
		saved.Cloud.S3Bucket != "bucket-sintetico" {
		t.Fatal("a preparação não salvou a escolha e o bucket", saved.Cloud, err)
	}
}

func TestMissingBucketImportCanExplicitlyRetryWithoutS3InItsOriginTab(t *testing.T) {
	type call struct{ source, model string }
	calls := make(chan call, 1)
	h := newHarness(t, runnerFunc(func(_ context.Context, source string, settings config.Settings, _ transcript.Request) (
		transcript.Result, error,
	) {
		calls <- call{source, settings.ModelID()}
		return resultFor(source, "Texto novo do áudio."), nil
	}), nil)
	origin := h.d.document
	origin.editor.SetText("Texto anterior.")
	origin.processEditor.SetText("Resultado anterior.")
	h.d.settings.Cloud.ModelID = config.TranscribeBatch
	h.d.startImportedJob(origin, "audio-preservado.wav")
	if origin.busy || origin.lastAudio != "audio-preservado.wav" ||
		origin.editor.Text != "Texto anterior." || origin.processEditor.Text != "Resultado anterior." ||
		h.d.transcribeSetup == nil || !origin.retryButton.Visible() {
		t.Fatal("o requisito de S3 iniciou a tarefa ou perdeu áudio/textos")
	}
	select {
	case <-calls:
		t.Fatal("um áudio foi enviado antes da escolha explícita de outro motor")
	default:
	}
	test.Tap(h.d.transcribeSetup.direct)
	other := h.d.addGroup()
	other.editor.SetText("Outro grupo.")
	h.await(func() bool { return len(calls) > 0 })
	actual := waitValue(t, calls)
	h.await(func() bool { return !origin.busy && origin.lastResult != nil })
	h.settle()
	if actual.source != "audio-preservado.wav" || actual.model != config.TranscribeStreaming ||
		h.d.document != other || other.editor.Text != "Outro grupo." ||
		origin.editor.Text != "Texto anterior.\n\nTexto novo do áudio." ||
		origin.processEditor.Text != "Resultado anterior." {
		t.Fatal("a alternativa sem S3 mudou o áudio, a aba ou os textos", actual, origin.editor.Text)
	}
}

func TestCloudSettingsRetainDisabledBucketAndOfferDirectConnection(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.d.openSettings()
	form := h.d.settingsForm
	h.await(func() bool { return !form.loading })
	form.quality.SetSelected(cloudChoiceName(config.TranscribeBatch))
	settings, err := h.d.settingsFromForm(form)
	if err != nil || settings.TranscriptionSetupError() == nil || settings.Cloud.S3Enabled() {
		t.Fatal("configuração sem S3 não preservou o pré-requisito antes do envio", err)
	}
	test.Tap(form.cloudDirect)
	test.Tap(form.save)
	h.await(func() bool { return h.d.settingsForm == nil })
	if h.d.settings.CloudModelID() != config.TranscribeStreaming ||
		h.d.settings.Cloud.S3Bucket != "" {
		t.Fatal("a alternativa da configuração exige S3 ou não selecionou conexão direta")
	}
}

func TestDirectTranscribeSetupSaveFailureKeepsTheOldModelAndAllowsRecovery(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.d.setCloudModel(config.TranscribeBatch, false)
	form := h.d.transcribeSetup
	if err := os.Mkdir(filepath.Join(h.d.directory, "settings.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	test.Tap(form.direct)
	h.await(func() bool { return h.d.errorLabel.Visible() })
	if h.d.settings.CloudModelID() != config.VoxtralSmall ||
		h.d.transcribeSetup != form || form.direct.Disabled() {
		t.Fatal("falhar ao salvar trocou o motor ou deixou a preparação bloqueada")
	}
}

func TestClosingTranscribeSetupBeforeSavingCompletesDoesNotSendAudio(t *testing.T) {
	calls := make(chan string, 1)
	h := newHarness(t, runnerFunc(func(_ context.Context, source string, _ config.Settings, _ transcript.Request) (
		transcript.Result, error,
	) {
		calls <- source
		return resultFor(source, "Resposta que não deve chegar."), nil
	}), nil)
	h.d.settings.Cloud.ModelID = config.TranscribeBatch
	h.d.startImportedJob(h.d.document, "audio-preservado.wav")
	form := h.d.transcribeSetup
	test.Tap(form.direct)
	// The preference worker has finished, but its UI callback is still queued.
	h.d.workers.Wait()
	form.dialog.Hide()
	h.settle()
	select {
	case <-calls:
		t.Fatal("fechar a preparação enquanto salvava iniciou uma transcrição")
	default:
	}
	if h.d.document.busy || h.d.document.lastAudio != "audio-preservado.wav" ||
		!h.d.document.retryButton.Visible() || h.d.transcribeSetup != nil {
		t.Fatal("cancelar a preparação perdeu o áudio ou deixou a interface ocupada")
	}
}

func TestMissingBucketOfflineTimelineCanRetryItsPreservedPartsWithoutS3(t *testing.T) {
	calls := make(chan string, 4)
	h, r := streamHarness(t, runnerFunc(func(_ context.Context, source string, settings config.Settings, _ transcript.Request) (
		transcript.Result, error,
	) {
		calls <- settings.ModelID()
		return resultFor(source, "Fala recuperada."), nil
	}), nil)
	origin := h.d.document
	origin.editor.SetText("Texto anterior.")
	origin.processEditor.SetText("Resultado anterior.")
	h.d.settings.Capture.Stream, h.d.settings.Capture.Mode = false, config.CaptureTeam
	h.d.settings.Cloud.ModelID = config.TranscribeBatch
	h.d.syncCaptureOptions()
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	admitStream(h, r, 4)
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureIdle && h.d.transcribeSetup != nil })
	if origin.busy || origin.live == nil || len(origin.live.Entries) == 0 ||
		origin.editor.Text != "Texto anterior." || origin.processEditor.Text != "Resultado anterior." {
		t.Fatal("o requisito de S3 perdeu as partes ou publicou texto antes de reconhecer")
	}
	select {
	case <-calls:
		t.Fatal("um trecho foi enviado sem configurar S3")
	default:
	}
	test.Tap(h.d.transcribeSetup.direct)
	other := h.d.addGroup()
	other.editor.SetText("Outro grupo.")
	h.await(func() bool { return len(calls) > 0 })
	if actual := waitValue(t, calls); actual != config.TranscribeStreaming {
		t.Fatal("a retentativa não usou a conexão direta", actual)
	}
	h.await(func() bool { return !origin.busy && origin.lastResult != nil })
	if !strings.Contains(origin.editor.Text, "Fala recuperada.") ||
		!strings.HasPrefix(origin.editor.Text, "Texto anterior.") ||
		origin.processEditor.Text != "Resultado anterior." || other.editor.Text != "Outro grupo." ||
		h.d.document != other || origin.lastResult.Source != r.Snapshot().Path {
		t.Fatal("retomar as partes mudou o áudio, os textos ou a aba de origem")
	}
}
