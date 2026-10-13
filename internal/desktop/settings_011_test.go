package desktop

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"ai-talker/internal/cloud"
	"ai-talker/internal/config"
	"ai-talker/internal/recorder"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

func TestSettingsBucketDisabledKeepsNameAndDoesNotSendAudio(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.d.settings.Cloud.S3Bucket = "preserved-bucket"
	h.d.settings.Cloud.ModelID = config.TranscribeBatch
	h.d.openSettings()
	form := h.d.settingsForm
	h.await(func() bool { return !form.loading })
	if !form.cloudUseBucket.Checked || form.cloudBucket.Disabled() {
		t.Fatal("a migração não habilitou o bucket legado")
	}
	form.cloudUseBucket.SetChecked(false)
	if !form.cloudBucket.Disabled() || !form.cloudTestBucket.Disabled() {
		t.Fatal("desabilitar S3 deixou campo/teste ativos")
	}
	test.Tap(form.save)
	h.await(func() bool { return h.d.settingsForm == nil })
	saved, err := config.Load(h.d.directory)
	if err != nil || saved.Cloud.S3Bucket != "preserved-bucket" || saved.Cloud.S3Enabled() ||
		saved.Cloud.ModelID != config.TranscribeBatch {
		t.Fatal("desabilitar S3 apagou nome ou trocou o motor", saved.Cloud, err)
	}
	doc := h.d.document
	doc.editor.SetText("Texto preservado.")
	h.d.startImportedJob(doc, "preserved-audio.wav")
	if h.d.transcribeSetup == nil || doc.lastAudio != "preserved-audio.wav" || doc.editor.Text != "Texto preservado." ||
		doc.busy || !doc.retryButton.Visible() {
		t.Fatal("bucket desabilitado perdeu o áudio ou iniciou transcrição")
	}
}

func TestSettingsBucketProbeUsesDraftProfileAndSupportsCancelAndRetry(t *testing.T) {
	calls := make(chan config.Settings, 2)
	var callCount atomic.Int32
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.verifyBucket = func(ctx context.Context, settings config.Settings) (cloud.BucketCheck, error) {
			calls <- settings
			if callCount.Add(1) == 1 {
				<-ctx.Done()
				return cloud.BucketCheck{Cleaned: true}, ctx.Err()
			}
			return cloud.BucketCheck{Bucket: settings.Cloud.S3Bucket, Region: settings.Region, Cleaned: true}, nil
		}
	})
	h.d.openSettings()
	form := h.d.settingsForm
	h.await(func() bool { return !form.loading })
	form.profile.SetText("draft-profile")
	form.region.SetText("us-west-2")
	form.cloudUseBucket.SetChecked(true)
	form.cloudBucket.SetText("draft-bucket")
	test.Tap(form.cloudTestBucket)
	first := waitValue(t, calls)
	if first.Profile != "draft-profile" || first.Region != "us-west-2" || first.Cloud.S3Bucket != "draft-bucket" ||
		h.d.settings.Cloud.S3Bucket != "" {
		t.Fatal("teste não usou os campos em edição ou os salvou antes da confirmação", first)
	}
	test.Tap(form.cloudTestBucket)
	h.await(func() bool { return form.bucketCancel == nil })
	if !strings.Contains(form.cloudBucketResult.Text, "cancelado") || !strings.Contains(form.cloudBucketResult.Text, "removido") ||
		form.cloudTestBucket.Disabled() {
		t.Fatal("cancelamento não confirmou limpeza ou impediu retentativa", form.cloudBucketResult.Text)
	}
	test.Tap(form.cloudTestBucket)
	_ = waitValue(t, calls)
	h.await(func() bool { return form.bucketCancel == nil })
	if !strings.Contains(form.cloudBucketResult.Text, "Envio, leitura e limpeza confirmados") ||
		!strings.Contains(form.cloudBucketResult.Text, "KMS") {
		t.Fatal("o resultado omitiu validação ou limite do teste", form.cloudBucketResult.Text)
	}
}

func TestSettingsBucketProbeShowsFailureAndClosingCancelsIt(t *testing.T) {
	canceled := make(chan struct{}, 1)
	started := make(chan struct{}, 1)
	var callCount atomic.Int32
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.verifyBucket = func(ctx context.Context, _ config.Settings) (cloud.BucketCheck, error) {
			if callCount.Add(1) == 1 {
				return cloud.BucketCheck{Cleaned: true}, errors.New("ler o objeto sintético: KMS Decrypt negado")
			}
			started <- struct{}{}
			<-ctx.Done()
			canceled <- struct{}{}
			return cloud.BucketCheck{Cleaned: true}, ctx.Err()
		}
	})
	h.d.openSettings()
	form := h.d.settingsForm
	h.await(func() bool { return !form.loading })
	form.cloudUseBucket.SetChecked(true)
	form.cloudBucket.SetText("synthetic-bucket")
	test.Tap(form.cloudTestBucket)
	h.await(func() bool { return form.bucketCancel == nil })
	if !strings.Contains(form.cloudBucketResult.Text, "KMS Decrypt negado") || form.cloudTestBucket.Disabled() {
		t.Fatal("o resultado não mostrou o motivo ou bloqueou correção", form.cloudBucketResult.Text)
	}
	test.Tap(form.cloudTestBucket)
	_ = waitValue(t, started)
	form.dialog.Hide()
	_ = waitValue(t, canceled)
	h.settle()
	if h.d.settingsForm != nil {
		t.Fatal("fechar Configurações deixou o diagnóstico aberto")
	}
}

func TestSettingsDialogResizePreservesPageAndUnsavedValues(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.d.window.Resize(fyne.NewSize(480, 900))
	h.d.openSettings()
	form := h.d.settingsForm
	h.await(func() bool { return !form.loading })
	form.sections.selectID("connections/aws")
	form.profile.SetText("draft-profile")
	form.region.SetText("us-west-2")
	narrow := form.dialogSize.Width
	h.d.window.Resize(fyne.NewSize(1100, 900))
	h.d.window.Content().Refresh()
	if form.dialogSize.Width < narrow+400 || h.d.settingsForm != form || form.sections.selectedID != "connections/aws" ||
		form.profile.Text != "draft-profile" || form.region.Text != "us-west-2" {
		t.Fatal("o modal não acompanhou a janela ou perdeu campos/página", form.dialogSize, narrow)
	}
	h.d.window.Resize(fyne.NewSize(480, 900))
	h.d.window.Content().Refresh()
	if form.dialogSize.Width != narrow || form.profile.Text != "draft-profile" {
		t.Fatal("estreitar não restaurou o tamanho ou perdeu a edição", form.dialogSize)
	}
}

func TestRecorderDeviceRefreshRetainsUnavailableSelectionAndBlocksNextCapture(t *testing.T) {
	var connected atomic.Bool
	connected.Store(true)
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.microphones = func() ([]recorder.Device, error) {
			if !connected.Load() {
				return nil, nil
			}
			return []recorder.Device{{ID: 3, Name: "Microfone USB", EndpointID: "mic-usb"}}, nil
		}
	})
	h.d.settings.Capture.MicrophoneEndpoint = "mic-usb"
	test.Tap(h.d.deviceButton)
	form := h.d.deviceForm
	h.await(func() bool { return !form.loading })
	if form.microphone.Selected == defaultMicrophone {
		t.Fatal("a escolha explícita foi substituída pelo padrão")
	}
	connected.Store(false)
	test.Tap(form.refresh)
	h.await(func() bool { return !form.loading })
	if form.microphone.Selected != unavailableDevice || h.d.settings.Capture.MicrophoneEndpoint != "mic-usb" ||
		!form.message.Visible() {
		t.Fatal("o dispositivo removido foi substituído silenciosamente", form.microphone.Selected)
	}
	form.popup.Hide()
	test.Tap(h.d.recordButton)
	h.settle()
	if h.d.capture != captureIdle || !strings.Contains(h.d.document.errorLabel.Text, "Dispositivo indisponível") {
		t.Fatal("uma gravação foi iniciada com seleção removida")
	}
	test.Tap(h.d.deviceButton)
	form = h.d.deviceForm
	h.await(func() bool { return !form.loading })
	connected.Store(true)
	test.Tap(form.refresh)
	h.await(func() bool { return !form.loading })
	if form.microphone.Selected == unavailableDevice || h.d.unavailableMicrophone {
		t.Fatal("reconectar não restaurou a preferência explícita")
	}
}

func TestRecorderDeviceRefreshDuringCaptureKeepsCaptureOrigin(t *testing.T) {
	var enumeration atomic.Int32
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.microphones = func() ([]recorder.Device, error) {
			enumeration.Add(1)
			return []recorder.Device{{ID: 3, Name: "Microfone USB", EndpointID: "mic-usb"}}, nil
		}
	})
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	origin := h.d.captureOrigin
	test.Tap(h.d.deviceButton)
	form := h.d.deviceForm
	if form == nil {
		t.Fatal("o menu não abriu durante a captura")
	}
	h.await(func() bool { return !form.loading })
	if !form.microphone.Disabled() || !h.d.captureSelect.Disabled() || form.refresh.Disabled() {
		t.Fatal("o menu deixou trocar fontes ativas ou bloqueou atualizar")
	}
	test.Tap(form.refresh)
	h.await(func() bool { return !form.loading })
	if enumeration.Load() < 2 || h.d.captureOrigin != origin || origin.settings.Capture.MicrophoneEndpoint != "" ||
		h.d.capture != captureRunning {
		t.Fatal("atualizar trocou ou interrompeu a gravação")
	}
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureIdle && !h.d.busy })
}

func TestSettingsRefreshAndSaveSystemOutputSelection(t *testing.T) {
	var connected atomic.Bool
	connected.Store(true)
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.playbackDevices = func() ([]recorder.Device, error) {
			if connected.Load() {
				return []recorder.Device{{ID: 100000, Name: "Fones USB", EndpointID: "headphones-usb"}}, nil
			}
			return nil, errors.New("falha sintética de listagem")
		}
	})
	h.d.openSettings()
	form := h.d.settingsForm
	h.await(func() bool { return !form.loading })
	form.playback.SetSelected("Fones USB (100000)")
	test.Tap(form.deviceRefresh)
	h.await(func() bool { return !form.devicesLoading })
	if form.playback.Selected != "Fones USB (100000)" {
		t.Fatal("a atualização apagou a saída selecionada em edição")
	}
	test.Tap(form.save)
	h.await(func() bool { return h.d.settingsForm == nil })
	saved, err := config.Load(h.d.directory)
	if err != nil || saved.Capture.PlaybackEndpoint != "headphones-usb" {
		t.Fatal("a saída de Sistema não compartilhou a preferência do gravador", saved.Capture, err)
	}
	h.d.openSettings()
	form = h.d.settingsForm
	h.await(func() bool { return !form.loading })
	connected.Store(false)
	test.Tap(form.deviceRefresh)
	h.await(func() bool { return !form.devicesLoading })
	if form.playback.Selected != unavailableDevice || !form.errorLabel.Visible() ||
		h.d.settings.Capture.PlaybackEndpoint != "headphones-usb" {
		t.Fatal("falha de atualização substituiu a saída salva ou não mostrou motivo")
	}
}
