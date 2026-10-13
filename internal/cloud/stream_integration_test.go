//go:build awsintegration

package cloud

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ai-talker/internal/audio"
	"ai-talker/internal/config"
	"ai-talker/internal/live"
	"ai-talker/internal/transcript"
)

// Only versioned synthetic speech is sent. This check is opt-in and is never
// part of the routine suite or applied to private recordings.
func TestTranscriptionStreamBedrockIntegration(t *testing.T) {
	if os.Getenv("AI_TALKER_TEST_STREAM_AWS") != "1" {
		t.Skip("ative AI_TALKER_TEST_STREAM_AWS=1 para duas chamadas com fala sintética")
	}
	settings := config.Default()
	if selected := os.Getenv("AI_TALKER_TEST_PROFILE"); selected != "" {
		settings.Profile = selected
	}
	if selected := os.Getenv("AI_TALKER_TEST_REGION"); selected != "" {
		settings.Region = selected
	}
	if selected := os.Getenv("AI_TALKER_TEST_QUALITY"); selected != "" {
		settings.Quality = selected
	}
	processor, err := audio.New()
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{audio.FormatMP3, audio.FormatWAV} {
		t.Run(format, func(t *testing.T) {
			preparing := time.Now()
			var chunks []audio.Chunk
			if err := processor.Split(t.Context(), filepath.Join("..", "..", "tests", "fixtures", "speech.wav"),
				audio.ChunkOptions{ChunkSeconds: 15, Format: format}, func(chunk audio.Chunk) error {
					chunks = append(chunks, chunk)
					return nil
				}); err != nil {
				t.Fatal(err)
			}
			preparation := time.Since(preparing)
			if len(chunks) != 1 || !chunks[0].HasSpeech {
				t.Fatal("synthetic speech did not reach the transcriber")
			}
			client, err := New(t.Context(), settings)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := client.Close(); err != nil {
					t.Error(err)
				}
			})
			streaming := client.(transcript.StreamingTranscriber)
			started := time.Now()
			var firstPartial time.Duration
			var partials int
			var latest string
			result, err := streaming.TranscribeStream(t.Context(), chunks[0], func(text string) {
				if partials == 0 {
					firstPartial = time.Since(started)
				}
				partials++
				latest = text
			})
			elapsed := time.Since(started)
			if err != nil {
				t.Fatal(err)
			}
			if partials == 0 || latest != result.Text ||
				!strings.Contains(strings.ToLower(result.Text), "hello") ||
				!strings.Contains(strings.ToLower(result.Text), "recording") {
				t.Fatalf("synthetic transcription lost content or partials: %+v / %d", result, partials)
			}
			t.Logf("model=%s format=%s audio=%.3fs prepare=%s first_partial=%s complete=%s partials=%d text=%q",
				settings.ModelID(), format, chunks[0].Duration, preparation, firstPartial, elapsed, partials, result.Text)
		})
	}
}

func TestNativeStreamBedrockIntegration(t *testing.T) {
	if os.Getenv("AI_TALKER_TEST_STREAM_AWS") != "1" {
		t.Skip("ative AI_TALKER_TEST_STREAM_AWS=1 para transcrever um trecho sintético de dois segundos")
	}
	settings := config.Default()
	if selected := os.Getenv("AI_TALKER_TEST_QUALITY"); selected != "" {
		settings.Quality = selected
	}
	settings.ChunkSeconds, settings.OverlapSeconds = 15, 0
	if selected := os.Getenv("AI_TALKER_TEST_PROFILE"); selected != "" {
		settings.Profile = selected
	}
	if selected := os.Getenv("AI_TALKER_TEST_REGION"); selected != "" {
		settings.Region = selected
	}
	processor, err := audio.New()
	if err != nil {
		t.Fatal(err)
	}
	pcm, err := os.ReadFile(filepath.Join("..", "audio", "testdata", "speech.pcm"))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	service, err := transcript.New(t.Context(), transcript.Options{
		Audio: processor, CloudFactory: New, DataDir: directory,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Error(err)
		}
	})
	firstEnd := 2 * audio.SampleRate * audio.SampleBytes
	for _, clip := range []struct {
		name string
		pcm  []byte
		want []string
	}{
		{"first", pcm[:firstEnd], []string{"hello", "short"}},
		// Recognition includes two seconds of preceding audio at a seam.
		{"tail", pcm[max(0, firstEnd-2*audio.SampleRate*audio.SampleBytes):], []string{"recording"}},
	} {
		t.Run(clip.name, func(t *testing.T) {
			wav, err := audio.EncodeWAV(t.Context(), clip.pcm)
			if err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(directory, clip.name+".wav")
			if err := os.WriteFile(source, wav, 0o600); err != nil {
				t.Fatal(err)
			}
			started := time.Now()
			var first time.Duration
			var partials int
			result, err := service.Transcribe(t.Context(), source, settings, transcript.Request{
				NativePCM: true, Workers: 1,
				OnPartial: func(partial transcript.Partial) {
					if partials == 0 {
						first = time.Since(started)
					}
					partials++
				},
			})
			elapsed := time.Since(started)
			if err != nil {
				t.Fatal(err)
			}
			lower := strings.ToLower(result.Text)
			duration := float64(len(clip.pcm)) / (audio.SampleRate * audio.SampleBytes)
			if partials == 0 || result.DurationSeconds != duration || result.NeedsReview ||
				strings.Contains(lower, "introduction") || strings.Contains(lower, "aws") || len(strings.Fields(lower)) > 8 {
				t.Fatalf("short native stream lost speech or live updates: %+v / %d", result, partials)
			}
			for _, word := range clip.want {
				if !strings.Contains(lower, word) {
					t.Fatalf("short native stream lost %q: %+v", word, result)
				}
			}
			t.Logf("model=%s native=true audio=%.3fs first_partial=%s complete=%s partials=%d text=%q",
				result.ModelID, duration, first, elapsed, partials, result.Text)
		})
	}
}

func TestPortugueseNativeStreamBedrockIntegration(t *testing.T) {
	if os.Getenv("AI_TALKER_TEST_STREAM_AWS") != "1" {
		t.Skip("ative AI_TALKER_TEST_STREAM_AWS=1 para fala sintética em português")
	}
	settings := config.Default()
	settings.ChunkSeconds, settings.OverlapSeconds = 15, 0
	if selected := os.Getenv("AI_TALKER_TEST_QUALITY"); selected != "" {
		settings.Quality = selected
	}
	if selected := os.Getenv("AI_TALKER_TEST_PROFILE"); selected != "" {
		settings.Profile = selected
	}
	if selected := os.Getenv("AI_TALKER_TEST_REGION"); selected != "" {
		settings.Region = selected
	}
	processor, err := audio.New()
	if err != nil {
		t.Fatal(err)
	}
	var clips []audio.Chunk
	fixture, err := os.ReadFile(filepath.Join("..", "..", "tests", "fixtures", "stream-portuguese.wav"))
	if err != nil {
		t.Fatal(err)
	}
	if len(fixture) < 44 || string(fixture[36:40]) != "data" ||
		binary.LittleEndian.Uint32(fixture[24:28]) != audio.SampleRate ||
		binary.LittleEndian.Uint16(fixture[22:24]) != 1 {
		t.Fatal("Portuguese fixture must be the recorder's mono 16 kHz PCM WAV")
	}
	// Split raw capture samples. Processor.Split already applies the speech
	// filter; using it before PrepareLive would filter this fixture twice.
	if err := audio.SplitPCM(t.Context(), bytes.NewReader(fixture[44:]),
		audio.ChunkOptions{ChunkSeconds: 4, Format: audio.FormatWAV}, audio.EncodeWAV, func(chunk audio.Chunk) error {
			clips = append(clips, chunk)
			return nil
		}); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	service, err := transcript.New(t.Context(), transcript.Options{Audio: processor, CloudFactory: New, DataDir: directory})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Error(err)
		}
	})
	var previous []byte
	var entries []live.Entry
	for index, clip := range clips {
		// The normal codec path can omit silence; retain the original fixture's
		// frames so this test exercises the same native VAD gate as capture.
		if len(clip.Audio) < 44 {
			continue
		}
		fresh := clip.Audio[44:]
		pcm := append(append([]byte(nil), previous...), fresh...)
		wav, err := audio.EncodeWAV(t.Context(), pcm)
		if err != nil {
			t.Fatal(err)
		}
		source := filepath.Join(directory, "portuguese-"+(time.Duration(index)*time.Second).String()+".wav")
		if err := os.WriteFile(source, wav, 0o600); err != nil {
			t.Fatal(err)
		}
		contextSamples := uint64(len(previous) / audio.SampleBytes)
		partials := 0
		result, err := service.Transcribe(t.Context(), source, settings, transcript.Request{
			NativePCM: true, NativeContextSamples: contextSamples, Workers: 1,
			OnPartial: func(partial transcript.Partial) {
				partials++
				lower := strings.ToLower(partial.Text)
				if strings.Contains(lower, "inaudível") || strings.Contains(lower, "translation:") {
					t.Errorf("diagnostic or translation reached the live editor: %q", partial.Text)
				}
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, live.Entry{
			ID: uint64(index + 1), Start: clip.Start, End: clip.Start + clip.Duration,
			Track: "microphone", ContextSamples: contextSamples, Text: result.Text,
		})
		t.Logf("Português trecho %d: %.2fs, %.2fs de espera, %d parciais, %q",
			index+1, clip.Duration, result.ElapsedSeconds, partials, result.Text)
		previous = append([]byte(nil), pcm[max(0, len(pcm)-2*audio.SampleRate*audio.SampleBytes):]...)
	}
	text := live.Render("", live.Blocks(entries, false))
	lower := strings.ToLower(text)
	for _, word := range []string{"estudar", "programação", "salvar", "imagem", "português"} {
		if !strings.Contains(lower, word) {
			t.Fatalf("Portuguese Stream lost %q: %q", word, text)
		}
	}
	if strings.Contains(lower, "today") || strings.Contains(lower, "english") || strings.Contains(lower, "[") {
		t.Fatalf("Portuguese speech was translated or annotated: %q", text)
	}
	t.Logf("Transcrição final em português: %s", text)
}
