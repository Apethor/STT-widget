//go:build integration

package audio

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestFFmpegPreparesSyntheticSpeechAndPreservesDuration(t *testing.T) {
	processor, err := New()
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join("..", "..", "tests", "fixtures", "speech.wav")
	duration, err := processor.Duration(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	parts := make([]Chunk, 0)
	if err := processor.Split(t.Context(), source, ChunkOptions{ChunkSeconds: 15}, func(chunk Chunk) error {
		parts = append(parts, chunk)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(parts) != 1 || !parts[0].HasSpeech || len(parts[0].Audio) == 0 {
		t.Fatalf("invalid encoded speech parts: %+v", parts)
	}
	if math.Abs(parts[0].Duration-duration) > 1.0/SampleRate {
		t.Fatalf("decoded duration %.6f differs from source %.6f", parts[0].Duration, duration)
	}
}

func TestFFmpegPreparesShortWord(t *testing.T) {
	processor, err := New()
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join("..", "..", "tests", "fixtures", "short-word.wav")
	var parts []Chunk
	if err := processor.Split(t.Context(), source, ChunkOptions{
		ChunkSeconds: 15, Format: FormatWAV,
	}, func(chunk Chunk) error {
		parts = append(parts, chunk)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(parts) != 1 {
		t.Fatalf("parts = %d, want 1", len(parts))
	}
	t.Logf("short word: duration=%.3f voiced=%.3f detected=%v",
		parts[0].Duration, parts[0].SpeechSeconds, parts[0].HasSpeech)
	if !parts[0].HasSpeech || len(parts[0].Audio) == 0 {
		t.Fatal("the short word was discarded before transcription")
	}
}

func TestFFmpegPreparesBriefVoicedExcerpt(t *testing.T) {
	processor, err := New()
	if err != nil {
		t.Fatal(err)
	}
	word, err := os.ReadFile(filepath.Join("testdata", "short-word.pcm"))
	if err != nil {
		t.Fatal(err)
	}
	start := 3 * SampleRate * SampleBytes / 10
	end := 6 * SampleRate * SampleBytes / 10
	pcm := make([]byte, 2*SampleRate*SampleBytes)
	for offset := start; offset < end; offset += SampleBytes {
		value := int16(binary.LittleEndian.Uint16(word[offset:]))
		destination := SampleRate*SampleBytes/2 + offset - start
		binary.LittleEndian.PutUint16(pcm[destination:], uint16(int16(float64(value)*0.1)))
	}
	wav, err := EncodeWAV(t.Context(), pcm)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "brief-voice.wav")
	if err := os.WriteFile(source, wav, 0o600); err != nil {
		t.Fatal(err)
	}
	var parts []Chunk
	if err := processor.Split(t.Context(), source, ChunkOptions{
		ChunkSeconds: 15, Format: FormatWAV,
	}, func(chunk Chunk) error {
		parts = append(parts, chunk)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(parts) != 1 {
		t.Fatalf("parts = %d, want 1", len(parts))
	}
	t.Logf("brief voice: duration=%.3f voiced=%.3f detected=%v",
		parts[0].Duration, parts[0].SpeechSeconds, parts[0].HasSpeech)
	if !parts[0].HasSpeech || len(parts[0].Audio) == 0 {
		t.Fatal("brief voice was discarded before transcription")
	}
}

func TestFFmpegRejectsShortSilenceAndClick(t *testing.T) {
	processor, err := New()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		pcm  []byte
	}{
		{name: "silence", pcm: make([]byte, 2*SampleRate*SampleBytes)},
		{name: "background noise and click", pcm: func() []byte {
			pcm := make([]byte, 2*SampleRate*SampleBytes)
			random := rand.New(rand.NewPCG(42, 43))
			for offset := 0; offset < len(pcm); offset += SampleBytes {
				binary.LittleEndian.PutUint16(pcm[offset:], uint16(int16(random.IntN(61)-30)))
			}
			binary.LittleEndian.PutUint16(pcm[20000:], 3000)
			return pcm
		}()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			wav, err := EncodeWAV(t.Context(), test.pcm)
			if err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(t.TempDir(), "no-speech.wav")
			if err := os.WriteFile(source, wav, 0o600); err != nil {
				t.Fatal(err)
			}
			var parts []Chunk
			if err := processor.Split(t.Context(), source, ChunkOptions{
				ChunkSeconds: 15, Format: FormatWAV,
			}, func(chunk Chunk) error {
				parts = append(parts, chunk)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if len(parts) != 1 || parts[0].HasSpeech || len(parts[0].Audio) != 0 {
				t.Fatalf("non-speech reached transcription: %+v", parts)
			}
		})
	}
}

func TestFFmpegPrefilterKeepsWidebandSpeech(t *testing.T) {
	processor, err := New()
	if err != nil {
		t.Fatal(err)
	}
	speech, err := os.ReadFile(filepath.Join("testdata", "speech.pcm"))
	if err != nil {
		t.Fatal(err)
	}
	mixed := make([]byte, len(speech))
	const toneLevel = 500
	for sample := 0; sample < len(speech)/SampleBytes; sample++ {
		value := int(int16(binary.LittleEndian.Uint16(speech[sample*SampleBytes:])))
		for _, frequency := range []float64{40, 1000, 7800} {
			value += int(toneLevel * math.Sin(2*math.Pi*frequency*float64(sample)/SampleRate))
		}
		binary.LittleEndian.PutUint16(mixed[sample*SampleBytes:], uint16(int16(value)))
	}
	prepare := func(name string, pcm []byte) []byte {
		t.Helper()
		wav, err := EncodeWAV(t.Context(), pcm)
		if err != nil {
			t.Fatal(err)
		}
		source := filepath.Join(t.TempDir(), name+".wav")
		if err := os.WriteFile(source, wav, 0o600); err != nil {
			t.Fatal(err)
		}
		var parts []Chunk
		if err := processor.Split(t.Context(), source, ChunkOptions{
			ChunkSeconds: 15, Format: FormatWAV,
		}, func(chunk Chunk) error {
			parts = append(parts, chunk)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if len(parts) != 1 || !parts[0].HasSpeech || len(parts[0].Audio) != 44+len(speech) {
			t.Fatalf("%s: invalid filtered WAV parts: %+v", name, parts)
		}
		return parts[0].Audio[44:]
	}
	baseline := prepare("speech", speech)
	filtered := prepare("speech-with-tones", mixed)
	gains := make(map[float64]float64)
	for _, frequency := range []float64{40, 1000, 7800} {
		inputLevel := toneAmplitudeDifference(mixed, speech, frequency)
		outputLevel := toneAmplitudeDifference(filtered, baseline, frequency)
		gains[frequency] = outputLevel / inputLevel
	}
	if gains[40] >= 0.35 || gains[1000] <= 0.85 || gains[7800] >= 0.4 {
		t.Fatalf("unexpected filter gain at 40/1000/7800 Hz: %.3f / %.3f / %.3f",
			gains[40], gains[1000], gains[7800])
	}
}

func toneAmplitudeDifference(signal, baseline []byte, frequency float64) float64 {
	var sine, cosine float64
	for sample := 0; sample < len(signal)/SampleBytes; sample++ {
		value := float64(int16(binary.LittleEndian.Uint16(signal[sample*SampleBytes:]))) -
			float64(int16(binary.LittleEndian.Uint16(baseline[sample*SampleBytes:])))
		phase := 2 * math.Pi * frequency * float64(sample) / SampleRate
		sine += value * math.Sin(phase)
		cosine += value * math.Cos(phase)
	}
	return 2 * math.Hypot(sine, cosine) / float64(len(signal)/SampleBytes)
}

func TestFFmpegPrefilterAcceptsEightKilohertzInput(t *testing.T) {
	processor, err := New()
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join("..", "..", "tests", "fixtures", "speech.wav")
	narrowband := filepath.Join(t.TempDir(), "speech-8k.wav")
	cmd := exec.CommandContext(t.Context(), processor.FFmpeg,
		"-v", "error", "-nostdin", "-i", source, "-ac", "1", "-ar", "8000", narrowband)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("prepare 8 kHz speech: %s: %v", output, err)
	}
	var parts []Chunk
	if err := processor.Split(t.Context(), narrowband, ChunkOptions{ChunkSeconds: 15},
		func(chunk Chunk) error {
			parts = append(parts, chunk)
			return nil
		}); err != nil {
		t.Fatal(err)
	}
	if len(parts) != 1 || !parts[0].HasSpeech || len(parts[0].Audio) == 0 {
		t.Fatalf("8 kHz speech was not prepared: %+v", parts)
	}
}

func TestLocalAudioCannotOpenEmbeddedNetworkReferences(t *testing.T) {
	processor, err := New()
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()
	source := filepath.Join(t.TempDir(), "audio.wav")
	playlist := fmt.Sprintf("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:4\n"+
		"#EXTINF:4,\n%s/audio.ts\n#EXT-X-ENDLIST\n", server.URL)
	if err := os.WriteFile(source, []byte(playlist), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := processor.Duration(t.Context(), source); err == nil {
		t.Fatal("probing an audio file followed an embedded network reference")
	}
	if err := processor.Split(t.Context(), source, ChunkOptions{ChunkSeconds: 15}, func(Chunk) error {
		t.Fatal("an embedded network reference produced an audio chunk")
		return nil
	}); err == nil {
		t.Fatal("decoding an audio file accepted an embedded network reference")
	}
	if requests.Load() != 0 {
		t.Fatalf("local audio opened %d network requests", requests.Load())
	}
}
