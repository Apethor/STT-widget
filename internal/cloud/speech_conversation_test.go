package cloud

import (
	"errors"
	"strings"
	"testing"

	"ai-talker/internal/config"
	"ai-talker/internal/realtime"
)

func TestSpeechConversationRejectsUnsupportedConfigurationBeforeCredentials(t *testing.T) {
	settings := config.Default()
	settings.Profile = "this-profile-must-never-be-loaded"
	settings.Region = "eu-west-1"
	if _, err := NewConversation(t.Context(), settings, realtime.Options{}, nil); err == nil ||
		!strings.Contains(err.Error(), "eu-west-1") {
		t.Fatalf("region error = %v", err)
	}
	settings.Region = "us-east-1"
	if _, err := NewConversation(t.Context(), settings, realtime.Options{ModelID: "openrouter-audio"}, nil); !errors.Is(err, realtime.ErrUnsupportedProvider) {
		t.Fatalf("model error = %v", err)
	}
	if _, err := NewConversation(t.Context(), settings, realtime.Options{VoiceID: "invented"}, nil); err == nil ||
		!strings.Contains(err.Error(), "voz") {
		t.Fatalf("voice error = %v", err)
	}
}
