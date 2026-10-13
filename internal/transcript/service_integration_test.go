//go:build integration

package transcript

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"ai-talker/internal/audio"
	"ai-talker/internal/config"
)

func TestBriefQuietSpeechReachesTranscriber(t *testing.T) {
	word, err := os.ReadFile(filepath.Join("..", "audio", "testdata", "short-word.pcm"))
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]byte, 2*audio.SampleRate*audio.SampleBytes)
	start := 3 * audio.SampleRate * audio.SampleBytes / 10
	end := 6 * audio.SampleRate * audio.SampleBytes / 10
	for offset := start; offset < end; offset += audio.SampleBytes {
		value := int16(binary.LittleEndian.Uint16(word[offset:]))
		destination := audio.SampleRate*audio.SampleBytes/2 + offset - start
		binary.LittleEndian.PutUint16(pcm[destination:], uint16(int16(float64(value)*0.1)))
	}
	wav, err := audio.EncodeWAV(t.Context(), pcm)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	source := filepath.Join(directory, "brief-voice.wav")
	if err := os.WriteFile(source, wav, 0o600); err != nil {
		t.Fatal(err)
	}
	processor, err := audio.New()
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	service, err := New(t.Context(), Options{
		DataDir: directory, Audio: processor, RequestTimeout: 5 * time.Second,
		CloudFactory: func(context.Context, config.Settings) (Transcriber, error) {
			return &fakeCloud{transcribe: func(_ context.Context, chunk audio.Chunk) (ChunkResult, error) {
				if chunk.Format != audio.FormatMP3 || len(chunk.Audio) == 0 {
					t.Errorf("brief speech was not encoded for the transcriber")
				}
				calls.Add(1)
				return ChunkResult{Text: "Oi."}, nil
			}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	result, err := service.Transcribe(t.Context(), source, config.Default(), Request{})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || result.Text != "Oi." || len(result.Parts) != 1 ||
		!result.Parts[0].DetectedSpeech || result.Parts[0].SpeechSeconds >= 0.12 {
		t.Fatalf("brief speech did not reach the transcriber: calls=%d result=%+v", calls.Load(), result)
	}
}
