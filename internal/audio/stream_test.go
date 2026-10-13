package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func saveStreamPCM(t *testing.T, pcm []byte) string {
	t.Helper()
	wav, err := EncodeWAV(t.Context(), pcm)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "clip.wav")
	if err := os.WriteFile(source, wav, 0o600); err != nil {
		t.Fatal(err)
	}
	return source
}

func TestLivePCMPreparesSpeechWithoutLaunchingCodecs(t *testing.T) {
	t.Parallel()
	pcm, err := os.ReadFile(filepath.Join("testdata", "speech.pcm"))
	if err != nil {
		t.Fatal(err)
	}
	pcm = pcm[:2*SampleRate*SampleBytes]
	source := saveStreamPCM(t, pcm)
	processor := &Processor{FFmpeg: "missing-ffmpeg", FFprobe: "missing-ffprobe"}
	chunk, err := processor.PrepareLive(t.Context(), source)
	if err != nil || !chunk.HasSpeech || chunk.SpeechSeconds <= 0 ||
		chunk.Duration != 2 || chunk.Format != FormatWAV || len(chunk.Audio) != 44+len(pcm) {
		t.Fatalf("live PCM lost speech, duration or format: %+v / %v", chunk, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := processor.PrepareLive(ctx, source); !errors.Is(err, context.Canceled) {
		t.Fatal("live preparation ignored cancellation")
	}
}

func TestLivePCMSilenceDoesNotProduceAudioForCloud(t *testing.T) {
	t.Parallel()
	chunk, err := new(Processor).PrepareLive(t.Context(), saveStreamPCM(t, make([]byte, SampleRate*SampleBytes)))
	if err != nil || chunk.Duration != 1 || chunk.HasSpeech || len(chunk.Audio) != 0 {
		t.Fatalf("silence would reach a speech model: %+v / %v", chunk, err)
	}
}

func TestLiveContextSpeechCannotTriggerRecognitionDuringANewPause(t *testing.T) {
	t.Parallel()
	speech, err := os.ReadFile(filepath.Join("testdata", "speech.pcm"))
	if err != nil {
		t.Fatal(err)
	}
	context := speech[:2*SampleRate*SampleBytes]
	pcm := append(append([]byte(nil), context...), make([]byte, 2*SampleRate*SampleBytes)...)
	source := saveStreamPCM(t, pcm)
	processor := &Processor{FFmpeg: "missing-ffmpeg", FFprobe: "missing-ffprobe"}
	all, err := processor.PrepareLive(t.Context(), source)
	if err != nil || !all.HasSpeech {
		t.Fatal("the test's preceding context did not contain detected speech")
	}
	fresh, err := processor.PrepareLiveWithContext(t.Context(), source, 2*SampleRate)
	if err != nil || fresh.HasSpeech || fresh.Duration != 4 || len(fresh.Audio) != 0 {
		t.Fatalf("previous words were sent again during a pause: %+v / %v", fresh, err)
	}
	for _, samples := range []uint64{4 * SampleRate, math.MaxUint64} {
		if _, err := processor.PrepareLiveWithContext(t.Context(), source, samples); err == nil {
			t.Fatal("a context with no new audio was accepted")
		}
	}
}

func TestLiveContextKeepsPrecedingSamplesWhenNewSpeechIsDetected(t *testing.T) {
	t.Parallel()
	speech, err := os.ReadFile(filepath.Join("testdata", "speech.pcm"))
	if err != nil {
		t.Fatal(err)
	}
	pcm := append(make([]byte, SampleRate*SampleBytes), speech[:2*SampleRate*SampleBytes]...)
	chunk, err := new(Processor).PrepareLiveWithContext(t.Context(), saveStreamPCM(t, pcm), SampleRate)
	if err != nil || !chunk.HasSpeech || chunk.Duration != 3 || len(chunk.Audio) != 44+len(pcm) {
		t.Fatalf("fresh speech lost its full recognition context: %+v / %v", chunk, err)
	}
}

func TestCodecFallbackAlsoDetectsSpeechOnlyAfterItsContext(t *testing.T) {
	t.Parallel()
	speech, err := os.ReadFile(filepath.Join("testdata", "speech.pcm"))
	if err != nil {
		t.Fatal(err)
	}
	pcm := append(append([]byte(nil), speech[:2*SampleRate*SampleBytes]...), make([]byte, 2*SampleRate*SampleBytes)...)
	var result Chunk
	err = SplitPCM(t.Context(), bytes.NewReader(pcm), ChunkOptions{
		ChunkSeconds: 4, Format: FormatWAV, ContextSamples: 2 * SampleRate,
	}, func(context.Context, []byte) ([]byte, error) {
		t.Error("old speech triggered encoding during a new pause")
		return nil, nil
	}, func(chunk Chunk) error {
		result = chunk
		return nil
	})
	if err != nil || result.HasSpeech || result.Duration != 4 || len(result.Audio) != 0 {
		t.Fatalf("codec fallback repeated speech from context: %+v / %v", result, err)
	}
}

func TestLivePCMRejectsMalformedOrUnboundedWAV(t *testing.T) {
	t.Parallel()
	original, err := EncodeWAV(t.Context(), make([]byte, SampleRate*SampleBytes))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func([]byte) []byte
	}{
		{name: "float samples", change: func(wav []byte) []byte { binary.LittleEndian.PutUint16(wav[20:], 3); return wav }},
		{name: "stereo", change: func(wav []byte) []byte { binary.LittleEndian.PutUint16(wav[22:], 2); return wav }},
		{name: "other sample rate", change: func(wav []byte) []byte { binary.LittleEndian.PutUint32(wav[24:], 44100); return wav }},
		{name: "huge allocation", change: func(wav []byte) []byte { binary.LittleEndian.PutUint32(wav[40:], math.MaxUint32-1); return wav }},
		{name: "incomplete frame", change: func(wav []byte) []byte { binary.LittleEndian.PutUint32(wav[40:], 1); return wav }},
		{name: "truncated samples", change: func(wav []byte) []byte { return wav[:len(wav)-2] }},
		{name: "extra samples", change: func(wav []byte) []byte { return append(wav, 0, 0) }},
		{name: "unfinished header", change: func(wav []byte) []byte { binary.LittleEndian.PutUint32(wav[40:], 0); return wav }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			source := filepath.Join(t.TempDir(), "malformed.wav")
			if err := os.WriteFile(source, tc.change(append([]byte(nil), original...)), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := new(Processor).PrepareLive(t.Context(), source); err == nil {
				t.Fatal("a malformed live clip was accepted")
			}
		})
	}
}

func TestLiveSpeechFilterRetainsVoiceBandAndReducesRumbleAndNyquistEnergy(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		frequency float64
		maximum   float64
		minimum   float64
	}{
		{frequency: 20, maximum: 0.08},
		{frequency: 1000, minimum: 0.95, maximum: 1.01},
		{frequency: 7800, maximum: 0.08},
	} {
		pcm := make([]byte, SampleRate*SampleBytes)
		for index := range SampleRate {
			value := int16(1000 * math.Sin(2*math.Pi*tc.frequency*float64(index)/SampleRate))
			binary.LittleEndian.PutUint16(pcm[index*SampleBytes:], uint16(value))
		}
		filterStreamSpeech(pcm)
		var energy float64
		// Ignore the startup transient, just as for a fresh ffmpeg filter.
		for index := SampleRate / 4; index < SampleRate; index++ {
			value := float64(int16(binary.LittleEndian.Uint16(pcm[index*SampleBytes:])))
			energy += value * value
		}
		gain := math.Sqrt(energy/(SampleRate*0.75)) / (1000 / math.Sqrt2)
		if gain < tc.minimum || gain > tc.maximum {
			t.Fatalf("speech filter at %.0f Hz retained %.3f of the original level", tc.frequency, gain)
		}
	}
}
