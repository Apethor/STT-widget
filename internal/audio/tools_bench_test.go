//go:build integration

package audio

import (
	"os"
	"path/filepath"
	"testing"
)

// Measure serially, including process startup and the same speech filter used
// by transcription. Two seconds model one live capture part.
func BenchmarkStreamAudioPreparation(b *testing.B) {
	processor, err := New()
	if err != nil {
		b.Fatal(err)
	}
	pcm, err := os.ReadFile(filepath.Join("testdata", "speech.pcm"))
	if err != nil {
		b.Fatal(err)
	}
	pcm = pcm[:min(len(pcm), 2*SampleRate*SampleBytes)]
	wav, err := EncodeWAV(b.Context(), pcm)
	if err != nil {
		b.Fatal(err)
	}
	source := filepath.Join(b.TempDir(), "stream.wav")
	if err := os.WriteFile(source, wav, 0o600); err != nil {
		b.Fatal(err)
	}
	for _, format := range []string{FormatMP3, FormatWAV, "native"} {
		b.Run(format, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if format == "native" {
					chunk, err := processor.PrepareLive(b.Context(), source)
					if err != nil || !chunk.HasSpeech || len(chunk.Audio) == 0 {
						b.Fatalf("native speech preparation failed: %+v / %v", chunk, err)
					}
					continue
				}
				if _, err := processor.Duration(b.Context(), source); err != nil {
					b.Fatal(err)
				}
				if err := processor.Split(b.Context(), source, ChunkOptions{ChunkSeconds: 15, Format: format},
					func(chunk Chunk) error {
						if !chunk.HasSpeech || len(chunk.Audio) == 0 {
							b.Fatal("synthetic speech was lost")
						}
						return nil
					}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
