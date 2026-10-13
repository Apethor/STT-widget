package desktop

import (
	"context"
	"slices"
	"strings"
	"testing"

	"ai-talker/internal/config"
	"ai-talker/internal/transcript"

	"fyne.io/fyne/v2/test"
)

func TestCloudSettingsOfferAndPersistSeparateModelsForEachMode(t *testing.T) {
	h := newHarness(t, nil, nil)
	test.Tap(h.d.settingsButton)
	form := h.d.settingsForm
	h.await(func() bool { return !form.loading })
	if len(form.quality.Options) != 6 || len(form.streamModel.Options) != 5 ||
		slices.Contains(form.streamModel.Options, cloudChoiceName(config.TranscribeBatch)) {
		t.Fatal("o catálogo não distingue as opções de arquivo e Stream")
	}
	if !slices.Contains(form.quality.Options, "Nova 2.5 Sonic · ASR experimental") ||
		cloudChoiceID("Nova 2.5 Sonic · ASR experimental") != "amazon.nova-2-5-sonic" {
		t.Fatal("a opção visível de Nova 2.5 não selecionou o ID real da AWS")
	}
	form.quality.SetSelected(cloudChoiceName(config.TranscribeBatch))
	form.streamModel.SetSelected(cloudChoiceName(config.TranscribeStreaming))
	form.cloudBucket.SetText(" meu-bucket ")
	form.cloudLanguage.SetSelected("Português · Brasil")
	test.Tap(form.save)
	h.await(func() bool { return h.d.settingsForm == nil })
	saved, err := config.Load(h.d.directory)
	if err != nil || saved.Cloud.ModelID != config.TranscribeBatch ||
		saved.Cloud.StreamModelID != config.TranscribeStreaming ||
		saved.Cloud.S3Bucket != "meu-bucket" || saved.Cloud.Language != "pt-BR" {
		t.Fatal("as escolhas separadas não foram salvas", saved, err)
	}
	chooseRecorderAudioMode(h, 1)
	if !strings.Contains(h.d.streamButton.hint.text, "Transcribe") ||
		!strings.Contains(h.d.qualityBars.hint.text, "streaming") || h.d.settings.CloudModelID() != config.TranscribeBatch {
		t.Fatal("ativar Stream alterou a escolha de gravação completa ou mostrou o modelo errado")
	}
	h.d.setCloudModel(config.NovaSonic, true)
	h.settle()
	if h.d.settings.StreamModelID() != config.NovaSonic || h.d.settings.CloudModelID() != config.TranscribeBatch {
		t.Fatal("o atalho do modelo trocou as duas preferências")
	}
}

func TestStreamPinsItsSelectedModelWithoutChangingRetranscriptionModel(t *testing.T) {
	calls := make(chan config.Settings, 4)
	h, r := streamHarness(t, runnerFunc(func(_ context.Context, source string, settings config.Settings, _ transcript.Request) (
		transcript.Result, error,
	) {
		calls <- settings
		return resultFor(source, "Fala reconhecida."), nil
	}), nil)
	h.d.settings.Cloud.ModelID = config.VoxtralSmall
	h.d.settings.Cloud.StreamModelID = config.TranscribeStreaming
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureRunning })
	admitStream(h, r, streamPartSeconds)
	first := waitValue(t, calls)
	if first.ModelID() != config.TranscribeStreaming || h.d.settings.ModelID() != config.VoxtralSmall {
		t.Fatal("a captura não usou o modelo de Stream sem alterar o modelo normal")
	}
	h.d.settings.Cloud.StreamModelID = config.NovaSonic
	admitStream(h, r, 2*streamPartSeconds)
	second := waitValue(t, calls)
	if second.ModelID() != config.TranscribeStreaming {
		t.Fatal("mudar a preferência redirecionou a gravação já iniciada")
	}
	test.Tap(h.d.recordButton)
	h.await(func() bool { return h.d.capture == captureIdle && !h.d.busy })
	if h.d.settings.ModelID() != config.VoxtralSmall || h.d.settings.StreamModelID() != config.NovaSonic {
		t.Fatal("terminar o Stream perdeu as preferências dos dois modos")
	}
	session := h.d.document.live
	session.Entries[0].Error, session.Entries[0].NeedsReview = "Falha temporária simulada.", true
	h.d.retryLiveRecording(h.d.document)
	retried := waitValue(t, calls)
	h.await(func() bool { return !h.d.busy })
	if retried.ModelID() != config.NovaSonic || h.d.settings.ModelID() != config.VoxtralSmall {
		t.Fatal("retomar Stream ignorou sua escolha independente ou alterou o modelo normal")
	}
}
