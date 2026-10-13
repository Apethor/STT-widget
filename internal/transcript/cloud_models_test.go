package transcript

import (
	"context"
	"testing"

	"ai-talker/internal/audio"
	"ai-talker/internal/config"
)

func TestCloudPCMEnginesPrepareLosslessWAVAndKeepIndependentCaches(t *testing.T) {
	var calls int
	service, source, _ := prepareService(t, speechParts(1), func(context.Context, config.Settings) (Transcriber, error) {
		return &fakeCloud{transcribe: func(context.Context, audio.Chunk) (ChunkResult, error) {
			calls++
			return ChunkResult{Text: "Texto reconhecido."}, nil
		}}, nil
	})
	service.options.Audio = localPreparer{fakePreparer{chunks: speechParts(1)}}
	var previous string
	models := []string{config.TranscribeBatch, config.TranscribeStreaming, config.NovaSonic, config.NovaSonic25}
	for _, model := range models {
		settings := config.Default()
		settings.Cloud.ModelID = model
		first, err := service.Transcribe(t.Context(), source, settings, Request{Workers: 1})
		if err != nil || first.Text != "Texto reconhecido." || first.ModelID != model || first.JobID == previous {
			t.Fatal("o motor não usou WAV/cache próprio", model, first, err)
		}
		settings.Cloud.StreamModelID = config.VoxtralMini
		output := 32
		settings.OutputVolume = &output
		second, err := service.Transcribe(t.Context(), source, settings, Request{Workers: 1})
		if err != nil || second.JobID != first.JobID || second.ReusedParts != 1 {
			t.Fatal("alterar o volume/modelo do outro modo invalidou o cache", second, err)
		}
		previous = first.JobID
	}
	if calls != len(models) {
		t.Fatal("os motores não isolaram/reaproveitaram seus resultados", calls)
	}
}

func TestTranscribeCacheSeparatesLanguagesAndIgnoresTransportBucket(t *testing.T) {
	settings := config.Default()
	settings.Cloud.ModelID = config.TranscribeBatch
	base, err := fingerprint("synthetic-audio", settings)
	if err != nil {
		t.Fatal(err)
	}
	settings.Cloud.S3Bucket = "another-bucket"
	same, err := fingerprint("synthetic-audio", settings)
	if err != nil || base != same {
		t.Fatal("trocar o destino temporário do S3 invalidou o reconhecimento", err)
	}
	settings.Cloud.Language = "en-US"
	changed, err := fingerprint("synthetic-audio", settings)
	if err != nil || base == changed {
		t.Fatal("idiomas diferentes reutilizaram o mesmo texto", err)
	}
	settings.Cloud.ModelID = config.VoxtralSmall
	base, err = fingerprint("synthetic-audio", settings)
	if err != nil {
		t.Fatal(err)
	}
	settings.Cloud.Language = "pt-BR"
	same, err = fingerprint("synthetic-audio", settings)
	if err != nil || same != base {
		t.Fatal("o idioma exclusivo de Transcribe alterou o cache de Voxtral", err)
	}
}
