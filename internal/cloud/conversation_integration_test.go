//go:build awsintegration

package cloud

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ai-talker/internal/audio"
	"ai-talker/internal/config"
	"ai-talker/internal/realtime"
)

// Only the versioned synthetic speech fixture is sent to the service. Two
// utterances share the same continuous session and must yield actual speech,
// rather than merely confirming the existing transcription adapter.
func TestNovaConversationSyntheticIntegration(t *testing.T) {
	if os.Getenv("AI_TALKER_TEST_CONVERSATION") != "1" {
		t.Skip("ative AI_TALKER_TEST_CONVERSATION=1 para conversar com fala sintética")
	}
	settings := config.Default()
	if profile := os.Getenv("AI_TALKER_TEST_PROFILE"); profile != "" {
		settings.Profile = profile
	}
	if region := os.Getenv("AI_TALKER_TEST_REGION"); region != "" {
		settings.Region = region
	}
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()
	processor, err := audio.New()
	if err != nil {
		t.Fatal(err)
	}
	var pcm []byte
	err = processor.Split(ctx, filepath.Join("..", "..", "tests", "fixtures", "speech.wav"),
		audio.ChunkOptions{ChunkSeconds: 15, Format: audio.FormatWAV}, func(chunk audio.Chunk) error {
			pcm, err = speechPCM(chunk)
			return err
		})
	if err != nil || len(pcm) == 0 {
		t.Fatalf("synthetic input = %d bytes / %v", len(pcm), err)
	}
	options := realtime.Options{
		ModelID: realtime.Nova25, VoiceID: "carolina", MaxTokens: 128,
		SystemPrompt: "Você está validando uma conversa por voz com uma amostra sintética. Responda a cada fala muito brevemente em português, confirmando que ouviu a gravação. Não acrescente perguntas nem instruções.",
	}
	if model := os.Getenv("AI_TALKER_TEST_CONVERSATION_MODEL"); model != "" {
		options.ModelID = model
	}
	if voice := os.Getenv("AI_TALKER_TEST_CONVERSATION_VOICE"); voice != "" {
		options.VoiceID = voice
	}
	completed := make(chan realtime.Event, 8)
	started := time.Now()
	var timingMu sync.Mutex
	speechEnd := make([]time.Time, 2)
	firstText := make(map[string]time.Time)
	firstAudio := make(map[string]time.Time)
	session, err := NewConversation(ctx, settings, options, func(event realtime.Event) {
		timingMu.Lock()
		switch {
		case event.Kind == realtime.AssistantText && event.Text != "" && firstText[event.CompletionID].IsZero():
			firstText[event.CompletionID] = time.Now()
		case event.Kind == realtime.AssistantAudio && len(event.PCM) != 0 && firstAudio[event.CompletionID].IsZero():
			firstAudio[event.CompletionID] = time.Now()
		}
		timingMu.Unlock()
		if event.Kind == realtime.ResponseEnd || event.Kind == realtime.SessionEnd {
			completed <- event
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		session.Cancel()
		<-session.Done()
	}()
	feedCtx, stopFeed := context.WithCancel(ctx)
	defer stopFeed()
	feedDone := make(chan error, 1)
	go func() {
		frameBytes := realtime.InputSampleRate * 2 * 32 / 1000
		// Seven seconds between utterances lets the first short response finish
		// while microphone silence continues at a natural sampling cadence.
		input := append(append(append([]byte{}, pcm...), make([]byte, 7*realtime.InputSampleRate*2)...), pcm...)
		speechEnds := []int{len(pcm), len(input)}
		sentBytes := 0
		silence := make([]byte, frameBytes)
		ticker := time.NewTicker(32 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-feedCtx.Done():
				feedDone <- feedCtx.Err()
				return
			case <-ticker.C:
				frame := silence
				if len(input) > 0 {
					frame = input[:min(frameBytes, len(input))]
					input = input[len(frame):]
				}
				if err := session.SendAudio(feedCtx, frame); err != nil {
					feedDone <- err
					return
				}
				sentBytes += len(frame)
				timingMu.Lock()
				for i, end := range speechEnds {
					if sentBytes >= end && speechEnd[i].IsZero() {
						speechEnd[i] = time.Now()
					}
				}
				timingMu.Unlock()
			}
		}
	}()
	var turns int
	for turns < 2 {
		select {
		case <-ctx.Done():
			responses := session.Snapshot()
			for i := range responses {
				responses[i].PCM = nil
			}
			t.Fatalf("two spoken responses did not complete: %v / %+v", ctx.Err(), responses)
		case err := <-feedDone:
			t.Fatalf("continuous synthetic input failed: %v", err)
		case event := <-completed:
			if event.Kind == realtime.SessionEnd {
				t.Fatalf("session ended before two spoken responses: %v", event.Err)
			}
			if !event.Final {
				t.Fatalf("response did not finish: %+v", event)
			}
			for _, response := range session.Snapshot() {
				if response.CompletionID == event.CompletionID && len(response.PCM) > 0 &&
					strings.TrimSpace(response.Text) != "" && strings.TrimSpace(response.UserText) != "" {
					turns++
					t.Logf("turn=%d id=%s response_seconds=%.3f elapsed=%.3f user=%q assistant=%q",
						turns, response.CompletionID, float64(len(response.PCM))/(2*float64(response.SampleRate)),
						time.Since(started).Seconds(), response.UserText, response.Text)
				}
			}
		}
	}
	stopFeed()
	<-feedDone
	closeCtx, closeCancel := context.WithTimeout(ctx, 15*time.Second)
	defer closeCancel()
	if err := session.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
	responses := session.Snapshot()
	var measured []map[string]any
	timingMu.Lock()
	for i, response := range responses {
		if i >= len(speechEnd) {
			break
		}
		text, speech := firstText[response.CompletionID], firstAudio[response.CompletionID]
		if speechEnd[i].IsZero() || text.IsZero() || speech.IsZero() {
			timingMu.Unlock()
			t.Fatal("a medição da última fala e dos primeiros eventos ficou incompleta")
		}
		entry := map[string]any{
			"turn": i + 1, "completion_id": response.CompletionID,
			"last_speech_frame_sent_seconds":    speechEnd[i].Sub(started).Seconds(),
			"first_assistant_text_seconds":      text.Sub(started).Seconds(),
			"first_assistant_pcm_seconds":       speech.Sub(started).Seconds(),
			"last_speech_to_first_text_seconds": text.Sub(speechEnd[i]).Seconds(),
			"last_speech_to_first_pcm_seconds":  speech.Sub(speechEnd[i]).Seconds(),
		}
		measured = append(measured, entry)
		t.Logf("turn=%d last_speech_to_first_text=%.3fs last_speech_to_first_pcm=%.3fs",
			i+1, text.Sub(speechEnd[i]).Seconds(), speech.Sub(speechEnd[i]).Seconds())
	}
	timingMu.Unlock()
	if directory := os.Getenv("AI_TALKER_SPEECH_PROBE_DIRECTORY"); directory != "" {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		for i, response := range responses {
			if len(response.PCM) == 0 {
				continue
			}
			wav, err := realtime.EncodeWAV(response.PCM, response.SampleRate)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, "response-"+string(rune('1'+i))+".wav"), wav, 0o600); err != nil {
				t.Fatal(err)
			}
			responses[i].PCM = nil
		}
		data, err := json.MarshalIndent(map[string]any{
			"model": options.ModelID, "voice": options.VoiceID, "region": settings.Region,
			"synthetic_fixture": "tests/fixtures/speech.wav", "sessions": 1, "completed_turns": turns,
			"elapsed_seconds": time.Since(started).Seconds(), "responses": responses,
			"turn_timings": measured,
		}, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "result.json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("model=%s voice=%s region=%s sessions=1 complete_turns=%d elapsed=%.3fs",
		options.ModelID, options.VoiceID, settings.Region, turns, time.Since(started).Seconds())
}
