package config

import (
	"testing"

	"ai-talker/internal/apiconfig"
)

func TestStructuredDiarizationIsLimitedToNativeSpeakerLabelEngines(t *testing.T) {
	t.Parallel()
	settings := Default()
	settings.Cloud.ModelID = TranscribeBatch
	if !settings.SupportsStructuredDiarization() {
		t.Fatal("Amazon Transcribe por arquivo precisa fornecer segmentos estruturados")
	}
	settings.Cloud.ModelID = VoxtralSmall
	if settings.SupportsStructuredDiarization() {
		t.Fatal("rótulos sugeridos por prompt não devem ser tratados como diarização")
	}
	settings.Cloud.ModelID = NovaSonic
	if settings.SupportsStructuredDiarization() {
		t.Fatal("Nova Sonic não é um motor de diarização estruturada")
	}
	settings.Cloud.StreamModelID = TranscribeStreaming
	settings.Capture.Stream = true
	if !settings.SupportsStructuredDiarization() {
		t.Fatal("Amazon Transcribe streaming precisa fornecer segmentos estruturados")
	}
	settings.Provider = ProviderWhisper
	if settings.SupportsStructuredDiarization() {
		t.Fatal("Whisper sem contrato estruturado de diarização não deve habilitar rótulos")
	}
	settings.Provider = ProviderAPI
	settings.AudioAPI.Provider = apiconfig.AzureSpeech
	if !settings.SupportsStructuredDiarization() {
		t.Fatal("Azure Speech MAI-Transcribe-2 deve habilitar segmentos e locutores")
	}
	settings.AudioAPI.Provider = apiconfig.OpenAI
	if settings.SupportsStructuredDiarization() {
		t.Fatal("API sem contrato nativo de diarização não deve habilitar rótulos")
	}
}

func TestCloudChoicesPreserveLegacyQualityAndKeepModesIndependent(t *testing.T) {
	settings := Default()
	settings.Quality = "fast"
	if settings.ModelID() != VoxtralMini || settings.StreamModelID() != VoxtralMini {
		t.Fatal("a preferência antiga fast não foi preservada")
	}
	settings.Cloud.ModelID = TranscribeBatch
	if settings.StreamModelID() != VoxtralMini {
		t.Fatal("um motor exclusivo de arquivos foi selecionado para Stream")
	}
	settings.Cloud.StreamModelID = TranscribeStreaming
	if settings.ModelID() != TranscribeBatch || settings.ForStream().ModelID() != TranscribeStreaming {
		t.Fatal("as escolhas de gravação completa e Stream não ficaram independentes")
	}
	settings.Provider = ProviderWhisper
	if !settings.UsesPCM() || settings.ModelID() != "whisper.cpp/base" {
		t.Fatal("as escolhas Cloud alteraram o motor local")
	}
}

func TestCloudCatalogueAcceptsOnlyItsSupportedModes(t *testing.T) {
	var batches, streams int
	ids := make(map[string]bool)
	for _, model := range CloudModels() {
		if ids[model.ID] || model.ID == "" || model.Name == "" || model.Description == "" {
			t.Fatal("o catálogo contém um ID duplicado ou uma opção incompleta")
		}
		ids[model.ID] = true
		settings := Default()
		settings.Cloud.ModelID = model.ID
		if err := settings.Validate(); err != nil {
			t.Fatal(model.ID, err)
		}
		if settings.UsesPCM() != model.PCM {
			t.Fatal("a preparação de áudio não acompanha o motor", model.ID)
		}
		if model.Batch {
			batches++
		}
		settings.Cloud.StreamModelID = model.ID
		if model.Stream {
			streams++
			if err := settings.Validate(); err != nil {
				t.Fatal(model.ID, err)
			}
		} else if err := settings.Validate(); err == nil {
			t.Fatal("um modelo sem Stream foi aceito nesse modo")
		}
	}
	if batches != 6 || streams != 5 {
		t.Fatal("o catálogo perdeu opções", batches, streams)
	}
	sonic, ok := CloudModelByID(NovaSonic25)
	if !ok || sonic.ID != "amazon.nova-2-5-sonic" || sonic.Name != "Nova 2.5 Sonic · ASR experimental" {
		t.Fatalf("Nova 2.5 Sonic não aparece no catálogo: %+v", sonic)
	}
	legacySonic, ok := CloudModelByID(NovaSonic)
	if !ok || legacySonic.Name != "Nova 2 Sonic · ASR experimental" || legacySonic.ID == sonic.ID {
		t.Fatal("Nova 2.5 substituiu ou renomeou indevidamente o Nova 2 legado")
	}
	for _, region := range []string{"us-east-1", "us-west-2", "eu-north-1", "ap-northeast-1"} {
		if !sonic.SupportsRegion(region) {
			t.Fatalf("o catálogo recusou a região Bedrock documentada %s", region)
		}
	}
	if sonic.SupportsRegion("us-east-2") {
		t.Fatal("Nova 2.5 Sonic foi aceito fora das regiões documentadas")
	}
	if model, _ := CloudModelByID(VoxtralSmall); !model.SupportsRegion("us-east-2") {
		t.Fatal("a validação regional de Sonic restringiu outro modelo")
	}
	for _, change := range []func(*Settings){
		func(s *Settings) { s.Cloud.ModelID = "text-model" },
		func(s *Settings) { s.Cloud.StreamModelID = "amazon.nova-sonic-v1:0" },
		func(s *Settings) { s.Cloud.Language = "Português" },
		func(s *Settings) { value := 201; s.OutputVolume = &value },
		func(s *Settings) { value := -1; s.OutputVolume = &value },
	} {
		settings := Default()
		change(&settings)
		if settings.Validate() == nil {
			t.Fatal("uma configuração Cloud/volume inválida foi aceita")
		}
	}
}

func TestCloudAndMutedOutputPreferencesRoundTripWithoutChangingInputVolumes(t *testing.T) {
	directory := t.TempDir()
	settings := Default()
	settings.Cloud = CloudTranscription{
		ModelID: TranscribeBatch, StreamModelID: NovaSonic, S3Bucket: "my-transcripts", Language: "auto",
	}
	output, mic, others := 0, 45, 130
	settings.OutputVolume = &output
	settings.Capture.MicrophoneVolume, settings.Capture.PlaybackVolume = &mic, &others
	if err := Save(directory, settings); err != nil {
		t.Fatal(err)
	}
	saved, err := Load(directory)
	if err != nil || saved.Cloud != settings.Cloud || saved.OutputVolume == nil ||
		*saved.OutputVolume != 0 || CaptureVolume(saved.Capture.MicrophoneVolume) != mic ||
		CaptureVolume(saved.Capture.PlaybackVolume) != others {
		t.Fatal("salvar modelos ou volume de saída perdeu outras preferências", saved, err)
	}
	if saved.Cloud.TranscribeLanguage() != "auto" || Default().Cloud.TranscribeLanguage() != "pt-BR" {
		t.Fatal("o idioma salvo ou o padrão português foi perdido")
	}
}

func TestTranscribeBucketNamesRejectURLsAndInvalidNamespaces(t *testing.T) {
	for _, bucket := range []string{"s3://my-bucket", "my-bucket/pasta", "https://my-bucket", "A-Bucket",
		"ab", "my..bucket", "my-.bucket", "127.0.0.1", "-my-bucket", "my-bucket-"} {
		if (CloudTranscription{S3Bucket: bucket}).Validate() == nil {
			t.Fatal("um nome inválido de bucket foi aceito", bucket)
		}
	}
	for _, bucket := range []string{"", "my-bucket", "my.bucket-42"} {
		if err := (CloudTranscription{S3Bucket: bucket}).Validate(); err != nil {
			t.Fatal(bucket, err)
		}
	}
}
