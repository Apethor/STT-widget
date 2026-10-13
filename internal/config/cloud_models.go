package config

import (
	"errors"
	"net"
	"regexp"
	"strings"
)

const (
	VoxtralSmall        = "mistral.voxtral-small-24b-2507"
	VoxtralMini         = "mistral.voxtral-mini-3b-2507"
	TranscribeBatch     = "aws.transcribe.batch"
	TranscribeStreaming = "aws.transcribe.streaming"
	NovaSonic           = "amazon.nova-2-sonic-v1:0"
	NovaSonic25         = "amazon.nova-2-5-sonic"

	CloudConverse   = "converse"
	CloudTranscribe = "transcribe"
	CloudStreaming  = "transcribe-streaming"
	CloudSonic      = "sonic"
)

// ErrTranscribeBucketRequired leaves legacy preferences loadable while callers
// can explain how to prepare batch transcription before starting an AWS job.
var ErrTranscribeBucketRequired = errors.New(
	"Amazon Transcribe · arquivo precisa de um bucket S3 na região selecionada. " +
		"Habilite-o em Conexões → AWS ou escolha Amazon Transcribe · streaming para usar o áudio sem S3",
)

type CloudModel struct {
	ID, Name, Backend, Description string
	Batch, Stream, Experimental    bool
	Regions                        []string
	// PCM selects lossless WAV preparation; a realtime API strips the header.
	PCM bool
}

// CloudModels lists AWS engines that accept speech and return its transcript.
// Conversational ASR is exposed explicitly as experimental, using only USER
// text events. Text-only models and retired Nova Sonic 1 are excluded.
func CloudModels() []CloudModel {
	return []CloudModel{
		{ID: VoxtralSmall, Name: "Voxtral Small 24B", Backend: CloudConverse, Batch: true, Stream: true,
			Description: "Bedrock · transcrição por trechos · modelo de 24 bilhões de parâmetros."},
		{ID: VoxtralMini, Name: "Voxtral Mini 3B", Backend: CloudConverse, Batch: true, Stream: true,
			Description: "Bedrock · transcrição por trechos · modelo de 3 bilhões de parâmetros."},
		{ID: TranscribeBatch, Name: "Amazon Transcribe · arquivo (S3)", Backend: CloudTranscribe, Batch: true, PCM: true,
			Description: "AWS · transcrição de arquivos · requer bucket S3 na mesma região · cobrança por duração."},
		{ID: TranscribeStreaming, Name: "Amazon Transcribe · streaming (sem S3)", Backend: CloudStreaming, Batch: true, Stream: true, PCM: true,
			Description: "AWS · reconhecimento incremental por conexão direta · aceita também áudio gravado · cobrança por duração."},
		{ID: NovaSonic, Name: "Nova 2 Sonic · ASR experimental", Backend: CloudSonic, Batch: true, Stream: true, Experimental: true, PCM: true,
			Regions:     []string{"us-east-1", "us-west-2", "eu-north-1", "ap-northeast-1"},
			Description: "Bedrock · versão Nova 2 Sonic · mostra a transcrição da fala do usuário · o serviço também pode gerar áudio cobrado."},
		{ID: NovaSonic25, Name: "Nova 2.5 Sonic · ASR experimental", Backend: CloudSonic, Batch: true, Stream: true, Experimental: true, PCM: true,
			Regions:     []string{"us-east-1", "us-west-2", "eu-north-1", "ap-northeast-1"},
			Description: "Bedrock · Nova 2.5 Sonic · o app mostra a transcrição da fala do usuário · o serviço também pode gerar áudio cobrado · regiões: N. Virginia, Oregon, Estocolmo e Tóquio."},
	}
}

func CloudModelByID(id string) (CloudModel, bool) {
	for _, model := range CloudModels() {
		if model.ID == id {
			return model, true
		}
	}
	return CloudModel{}, false
}

func (m CloudModel) SupportsRegion(region string) bool {
	if len(m.Regions) == 0 {
		return true
	}
	for _, supported := range m.Regions {
		if supported == region {
			return true
		}
	}
	return false
}

type CloudTranscription struct {
	ModelID       string `json:"model_id,omitempty"`
	StreamModelID string `json:"stream_model_id,omitempty"`
	Language      string `json:"language,omitempty"`
	S3Bucket      string `json:"s3_bucket,omitempty"`
	UseS3         *bool  `json:"use_s3,omitempty"`
}

// S3Enabled preserves older preferences: an existing bucket remains enabled
// until the person explicitly chooses whether to use it.
func (c CloudTranscription) S3Enabled() bool {
	if c.UseS3 != nil {
		return *c.UseS3
	}
	return strings.TrimSpace(c.S3Bucket) != ""
}

// Empty model IDs preserve the saved quality/fast preference of older releases.
func (s Settings) CloudModelID() string {
	if s.Cloud.ModelID != "" {
		return s.Cloud.ModelID
	}
	if s.Quality == "fast" {
		return VoxtralMini
	}
	return VoxtralSmall
}

func (s Settings) StreamModelID() string {
	if s.Cloud.StreamModelID != "" {
		return s.Cloud.StreamModelID
	}
	if model, ok := CloudModelByID(s.CloudModelID()); ok && model.Stream {
		return model.ID
	}
	if s.Quality == "fast" {
		return VoxtralMini
	}
	return VoxtralSmall
}

// ForStream pins the chosen streaming engine for this recording. Ordinary
// imports and retranscriptions continue to use the complete-recording choice.
func (s Settings) ForStream() Settings {
	s.Cloud.ModelID = s.StreamModelID()
	return s
}

func (s Settings) UsesPCM() bool {
	if s.TranscriptionProvider() == ProviderWhisper {
		return true
	}
	model, _ := CloudModelByID(s.CloudModelID())
	return model.PCM
}

func (s Settings) DirectCloudAudio() bool {
	model, _ := CloudModelByID(s.CloudModelID())
	return model.Backend == CloudStreaming || model.Backend == CloudSonic
}

// TranscriptionSetupError checks the selected engine's local prerequisites
// without loading credentials, contacting AWS or changing stored preferences.
func (s Settings) TranscriptionSetupError() error {
	if s.TranscriptionProvider() == ProviderBedrock && s.CloudModelID() == TranscribeBatch &&
		(!s.Cloud.S3Enabled() || strings.TrimSpace(s.Cloud.S3Bucket) == "") {
		return ErrTranscribeBucketRequired
	}
	return nil
}

func (c CloudTranscription) TranscribeLanguage() string {
	if c.Language == "" {
		return "pt-BR"
	}
	return c.Language
}

var bucketName = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)

func (c CloudTranscription) Validate() error {
	for _, choice := range []struct {
		id     string
		stream bool
	}{
		{c.ModelID, false}, {c.StreamModelID, true},
	} {
		if choice.id == "" {
			continue
		}
		model, ok := CloudModelByID(choice.id)
		if !ok || choice.stream && !model.Stream || !choice.stream && !model.Batch {
			return errors.New("selecione um modelo Cloud compatível com o modo da transcrição")
		}
	}
	switch c.TranscribeLanguage() {
	case "auto", "pt-BR", "pt-PT", "en-US", "es-US", "fr-FR", "de-DE", "it-IT", "ja-JP", "zh-CN":
	default:
		return errors.New("selecione um idioma válido para o Amazon Transcribe")
	}
	if c.S3Bucket != "" && (!bucketName.MatchString(c.S3Bucket) ||
		strings.Contains(c.S3Bucket, "..") || strings.Contains(c.S3Bucket, ".-") ||
		strings.Contains(c.S3Bucket, "-.") || net.ParseIP(c.S3Bucket) != nil) {
		return errors.New("informe somente o nome do bucket S3, sem caminho ou endereço")
	}
	return nil
}
