// Package config reads the desktop preferences shared with the Python release.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"ai-talker/internal/apiconfig"
	"ai-talker/internal/processing"
)

const DefaultGlossary = "AWS, Amazon Bedrock, Bedrock Mantle, Whisper, Voxtral, Nova Sonic, Nova 2.5 Sonic, " +
	"Codex, Claude, ChatGPT, GUI, API, SDK, Python, TypeScript, JavaScript, " +
	"Node.js, React, frontend, backend, PowerShell, GitHub"

// TranscriptionPromptVersion is shared by the request and cache fingerprint.
const TranscriptionPromptVersion = 4
const TranscriptionStreamPromptVersion = 3

type Settings struct {
	InterfaceLanguage     string                      `json:"interface_language,omitempty"`
	Provider              string                      `json:"transcription_provider"`
	Whisper               Whisper                     `json:"whisper"`
	Cloud                 CloudTranscription          `json:"cloud_transcription"`
	Profile               string                      `json:"profile"`
	Region                string                      `json:"region"`
	Quality               string                      `json:"quality"`
	ChunkSeconds          int                         `json:"chunk_seconds"`
	OverlapSeconds        float64                     `json:"overlap_seconds"`
	OutputVolume          *int                        `json:"output_volume,omitempty"`
	Glossary              string                      `json:"glossary"`
	Microphone            *int                        `json:"microphone"`
	Pinned                bool                        `json:"pinned"`
	RecorderBackend       string                      `json:"recorder_backend,omitempty"`
	Capture               Capture                     `json:"capture"`
	SpeakerLabels         bool                        `json:"speaker_labels,omitempty"`
	Processing            processing.Options          `json:"processing"`
	Appearance            Appearance                  `json:"appearance"`
	Merge                 processing.MergeOptions     `json:"merge"`
	WebSearchLimit        int                         `json:"web_search_limit,omitempty"`
	AudioAPI              apiconfig.Audio             `json:"audio_api,omitempty"`
	APIConnections        []apiconfig.SavedConnection `json:"api_connections,omitempty"`
	TextProfiles          []processing.Profile        `json:"text_profiles,omitempty"`
	Recovery              Recovery                    `json:"recovery,omitempty"`
	Instructions          []processing.Instruction    `json:"instructions,omitempty"`
	WhisperDownloadSource string                      `json:"whisper_download_source,omitempty"`
	VideoPreset           string                      `json:"video_preset,omitempty"`
	Conversation          Conversation                `json:"conversation,omitempty"`
}

func Default() Settings {
	return Settings{
		InterfaceLanguage: InterfacePortuguese,
		Provider:          ProviderBedrock,
		Whisper:           DefaultWhisper(),
		Region:            "us-east-1",
		Quality:           "quality",
		ChunkSeconds:      300,
		OverlapSeconds:    2,
		Glossary:          DefaultGlossary,
		RecorderBackend:   "winmm",
		Capture:           DefaultCapture(),
		Processing:        processing.Default().WithLegacyMerge(processing.DefaultMerge()),
		Appearance:        DefaultAppearance(),
		Merge:             processing.DefaultMerge(),
		VideoPreset:       VideoMedium,
	}
}

func (s Settings) ModelID() string {
	if s.TranscriptionProvider() == ProviderAPI {
		return s.AudioAPI.Model
	}
	if s.TranscriptionProvider() == ProviderWhisper {
		return "whisper.cpp/" + s.Whisper.Model
	}
	return s.CloudModelID()
}

// SupportsStructuredDiarization reports whether the selected recognizer can
// return provider-owned speaker IDs and timed segments. Prompt-based labels
// from generative models are deliberately excluded.
func (s Settings) SupportsStructuredDiarization() bool {
	if s.TranscriptionProvider() == ProviderAPI {
		return s.AudioAPI.Provider == apiconfig.AzureSpeech
	}
	if s.TranscriptionProvider() != ProviderBedrock {
		return false
	}
	modelID := s.CloudModelID()
	if s.Capture.Stream {
		modelID = s.StreamModelID()
	}
	model, ok := CloudModelByID(modelID)
	return ok && (model.Backend == CloudTranscribe || model.Backend == CloudStreaming)
}

func (s Settings) Device() int {
	if s.Microphone == nil {
		return -1
	}
	return *s.Microphone
}

func (s Settings) Validate() error {
	if err := s.Conversation.Validate(); err != nil {
		return err
	}
	if err := validateInterfaceLanguage(s.InterfaceLanguage); err != nil {
		return err
	}
	if err := validateVideoPreset(s.VideoPreset); err != nil {
		return err
	}
	if err := processing.ValidateInstructions(s.Instructions); err != nil {
		return err
	}
	if err := s.validateAPIConnections(); err != nil {
		return err
	}
	if s.WhisperDownloadSource != "" && s.WhisperDownloadSource != "github" && s.WhisperDownloadSource != "huggingface" {
		return errors.New("selecione GitHub ou Hugging Face para baixar modelos Whisper")
	}
	if err := s.Recovery.Validate(); err != nil {
		return err
	}
	if s.WebSearchLimit < 0 || s.WebSearchLimit > 100 {
		return errors.New("use entre 1 e 100 buscas por pedido")
	}
	if s.Provider != "" && s.Provider != ProviderBedrock && s.Provider != ProviderWhisper && s.Provider != ProviderAPI {
		return errors.New("selecione AWS Cloud, Whisper local ou API para transcrever")
	}
	if s.Provider == ProviderAPI {
		if err := s.AudioAPI.Validate(); err != nil {
			return err
		}
	}
	if len(s.TextProfiles) > 64 {
		return errors.New("use no máximo 64 perfis de processamento")
	}
	profileIDs := make(map[string]bool)
	profileNames := make(map[string]bool)
	for _, profile := range s.TextProfiles {
		if profile.ID == "" || len(profile.ID) > 128 || profileIDs[profile.ID] ||
			strings.TrimSpace(profile.Name) == "" || utf8.RuneCountInString(profile.Name) > 80 ||
			strings.ContainsAny(profile.Name+profile.ID, "\x00\r\n\t") || !utf8.ValidString(profile.Name) ||
			profileNames[strings.ToLower(strings.TrimSpace(profile.Name))] {
			return errors.New("os perfis de processamento têm dados inválidos")
		}
		profileIDs[profile.ID] = true
		profileNames[strings.ToLower(strings.TrimSpace(profile.Name))] = true
		if err := profile.Options.Validate(); err != nil {
			return err
		}
	}
	if err := s.Whisper.Validate(); err != nil {
		return err
	}
	if err := s.Cloud.Validate(); err != nil {
		return err
	}
	if s.OutputVolume != nil && (*s.OutputVolume < 0 || *s.OutputVolume > 200) {
		return errors.New("o volume de reprodução deve ficar entre 0 e 200%")
	}
	if s.Quality != "quality" && s.Quality != "fast" {
		return errors.New("escolha quality ou fast")
	}
	if s.ChunkSeconds < 15 || s.ChunkSeconds > 300 {
		return errors.New("cada parte deve ter entre 15 e 300 segundos")
	}
	// This also rejects NaN, which comparisons written as < 0 || > 5 miss.
	if !(s.OverlapSeconds >= 0 && s.OverlapSeconds <= 5) {
		return errors.New("a sobreposição deve ter entre zero e cinco segundos")
	}
	if strings.TrimSpace(s.Region) == "" {
		return errors.New("informe uma região AWS")
	}
	if utf8.RuneCountInString(s.Glossary) > 4000 {
		return errors.New("o glossário deve ter no máximo 4.000 caracteres")
	}
	if s.Microphone != nil && *s.Microphone < 0 {
		return errors.New("selecione um microfone válido ou o dispositivo padrão")
	}
	if err := s.Capture.Validate(); err != nil {
		return err
	}
	if err := s.Processing.Validate(); err != nil {
		return err
	}
	if err := s.validateConnectionSelection(s.Processing.API, true); err != nil {
		return err
	}
	if err := s.validateConnectionSelection(s.AudioAPI.Connection, false); err != nil {
		return err
	}
	for _, profile := range s.TextProfiles {
		if err := s.validateConnectionSelection(profile.Options.API, true); err != nil {
			return err
		}
	}
	if err := s.Appearance.Validate(); err != nil {
		return err
	}
	return s.Merge.Validate()
}

func DataDir() (string, error) {
	if override := os.Getenv("AI_TALKER_DATA_DIR"); override != "" {
		if strings.HasPrefix(override, "~/") || strings.HasPrefix(override, `~\`) {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("localizar a pasta pessoal: %w", err)
			}
			override = filepath.Join(homeDir, override[2:])
		}
		return filepath.Abs(override)
	}
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		var err error
		base, err = os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("localizar a pasta de dados: %w", err)
		}
	}
	return filepath.Join(base, "AI-Talker"), nil
}

func Load(directory string) (Settings, error) {
	settings := Default()
	payload, err := os.ReadFile(filepath.Join(directory, "settings.json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return settings, fmt.Errorf("ler as configurações: %w", err)
	}
	if len(payload) > 0 {
		// An invalid document is recoverable, but never rewrite it on startup.
		if err := json.Unmarshal(payload, &settings); err != nil {
			return settings, fmt.Errorf("ler as configurações salvas: %w", err)
		}
		var original map[string]json.RawMessage
		if err := json.Unmarshal(payload, &original); err != nil {
			return settings, fmt.Errorf("ler as configurações salvas: %w", err)
		}
		var originalProcessing map[string]json.RawMessage
		if raw, exists := original["processing"]; exists {
			if err := json.Unmarshal(raw, &originalProcessing); err != nil {
				return settings, fmt.Errorf("ler as opções de processamento: %w", err)
			}
		}
		if _, hasTabMerge := originalProcessing["merge_model_id"]; !hasTabMerge {
			settings.Processing.MergeModelID, settings.Processing.MergeReasoning, settings.Processing.MergePrompt = "", "", ""
			settings.Processing.MergeNeedsReview = false
		}
		if _, hasNativeDevice := original["recorder_backend"]; !hasNativeDevice {
			// PortAudio device indices and WinMM IDs refer to different lists.
			// Retain the original preferences on disk and start on the default
			// microphone until the user chooses a device in the Go interface.
			settings.Microphone = nil
			settings.RecorderBackend = "winmm"
		}
		if appearance, exists := original["appearance"]; exists {
			var originalAppearance map[string]json.RawMessage
			if err := json.Unmarshal(appearance, &originalAppearance); err != nil {
				return settings, fmt.Errorf("ler as opções de aparência: %w", err)
			}
			if _, hasFontFamily := originalAppearance["font_family"]; !hasFontFamily {
				settings.Appearance.FontFamily = ""
			}
			if _, hasFontSize := originalAppearance["font_size"]; !hasFontSize {
				settings.Appearance.FontSize = 0
			}
		}
	}
	settings.Processing = settings.Processing.WithLegacyMerge(settings.Merge)
	for index := range settings.TextProfiles {
		settings.TextProfiles[index].Options = settings.TextProfiles[index].Options.WithLegacyMerge(settings.Merge)
	}
	migrateAPIConnections(&settings)
	if settings.Profile == "" {
		settings.Profile = DefaultProfile()
	}
	validation := settings
	if settings.Provider == ProviderAPI && len(settings.AudioAPI.Language) <= 12 &&
		!strings.ContainsAny(settings.AudioAPI.Language, "\x00\r\n\t ") {
		if _, err := settings.AudioAPI.LanguageCodes(); err != nil {
			// 0.8.0 accepted a short hint without checking its model contract.
			// Keep those preferences on load; explicit saves and transcription
			// still require correction before the API receives any audio.
			validation.AudioAPI.Language = ""
		}
	}
	if err := validation.Validate(); err != nil {
		return settings, fmt.Errorf("configurações salvas inválidas: %w", err)
	}
	return settings, nil
}

func (s Settings) validateAPIConnections() error {
	if len(s.APIConnections) > apiconfig.MaxConnectionsPerFlow*2 {
		return errors.New("salve no máximo dez conexões para Texto e dez para Áudio")
	}
	ids, names := make(map[string]bool), make(map[string]bool)
	textCount, audioCount := 0, 0
	for _, connection := range s.APIConnections {
		if err := connection.Validate(); err != nil {
			return err
		}
		name := strings.ToLower(strings.TrimSpace(connection.Name))
		if ids[connection.ID] || names[name] {
			return errors.New("as conexões de API devem ter IDs e nomes únicos")
		}
		ids[connection.ID], names[name] = true, true
		if connection.Text {
			textCount++
		}
		if connection.Audio {
			audioCount++
		}
	}
	if textCount > apiconfig.MaxConnectionsPerFlow || audioCount > apiconfig.MaxConnectionsPerFlow {
		return errors.New("salve no máximo dez conexões para Texto e dez para Áudio")
	}
	return nil
}

func (s Settings) validateConnectionSelection(selected apiconfig.Connection, text bool) error {
	if selected.ID == "" {
		return nil
	}
	if text && selected.Provider == apiconfig.AzureSpeech {
		return errors.New("Azure Speech é exclusivo de transcrição de áudio")
	}
	for _, saved := range s.APIConnections {
		if saved.ID != selected.ID {
			continue
		}
		connection := saved.Connection()
		if text && !saved.Text || !text && !saved.Audio || connection.ID != selected.ID {
			return errors.New("a conexão de API selecionada não corresponde ao catálogo deste fluxo")
		}
		return nil
	}
	return errors.New("a conexão de API selecionada não está salva")
}

// DefaultProfile preserves the existing application's profile preference without
// fetching credentials or launching credential_process during settings loading.
func DefaultProfile() string {
	if profile := os.Getenv("AWS_PROFILE"); profile != "" {
		return profile
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	profiles := make(map[string]bool)
	for _, name := range []string{"config", "credentials"} {
		filename := filepath.Join(homeDir, ".aws", name)
		override := os.Getenv("AWS_CONFIG_FILE")
		if name == "credentials" {
			override = os.Getenv("AWS_SHARED_CREDENTIALS_FILE")
		}
		if override != "" {
			filename = override
		}
		payload, err := os.ReadFile(filename)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(payload), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
				section := strings.TrimSpace(line[1 : len(line)-1])
				section = strings.TrimPrefix(section, "profile ")
				profiles[section] = true
			}
		}
	}
	for _, candidate := range []string{"bedrock-codex-process", "bedrock-codex", "bedrock", "default"} {
		if profiles[candidate] {
			return candidate
		}
	}
	return ""
}
