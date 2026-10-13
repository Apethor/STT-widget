//go:build awsintegration

package cloud

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ai-talker/internal/audio"
	"ai-talker/internal/config"
	"ai-talker/internal/transcript"
)

// This opt-in check sends only the versioned synthetic speech fixture.
func TestNova25SonicSyntheticSpeechIntegration(t *testing.T) {
	if os.Getenv("AI_TALKER_TEST_NOVA25") != "1" {
		t.Skip("ative AI_TALKER_TEST_NOVA25=1 para uma sessão Nova 2.5 com fala sintética")
	}
	settings := config.Default()
	settings.Cloud.ModelID = config.NovaSonic25
	if profile := os.Getenv("AI_TALKER_TEST_PROFILE"); profile != "" {
		settings.Profile = profile
	}
	if region := os.Getenv("AI_TALKER_TEST_REGION"); region != "" {
		settings.Region = region
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	processor, err := audio.New()
	if err != nil {
		t.Fatal(err)
	}
	var chunks []audio.Chunk
	if err := processor.Split(ctx, filepath.Join("..", "..", "tests", "fixtures", "speech.wav"),
		audio.ChunkOptions{ChunkSeconds: 15, Format: audio.FormatWAV}, func(chunk audio.Chunk) error {
			chunks = append(chunks, chunk)
			return nil
		}); err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 1 || !chunks[0].HasSpeech || chunks[0].Duration > 10 {
		t.Fatal("a fixture sintética não produziu um único trecho curto com fala")
	}
	client, err := New(ctx, settings)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	}()
	streaming, ok := client.(transcript.StreamingTranscriber)
	if !ok {
		t.Fatal("o adaptador Nova 2.5 não oferece o contrato Stream")
	}
	var latest string
	var partials int
	result, err := streaming.TranscribeStream(ctx, chunks[0], func(text string) {
		latest = text
		partials++
	})
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ToLower(result.Text)
	if !strings.Contains(text, "hello") || !strings.Contains(text, "recording") ||
		latest != result.Text || partials == 0 {
		t.Fatalf("a transcrição final USER perdeu a fala sintética: %+v; parciais=%d; última=%q", result, partials, latest)
	}
	t.Logf("model=%s region=%s audio=%.3fs elapsed=%.3fs partials=%d usage=%v text=%q",
		settings.CloudModelID(), settings.Region, chunks[0].Duration, result.ElapsedSeconds,
		partials, result.Usage, result.Text)
}
