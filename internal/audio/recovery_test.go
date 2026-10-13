package audio

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestVADIncludesSpeechAfterThreeMinutesOfSilence(t *testing.T) {
	voice, err := os.ReadFile(filepath.Join("testdata", "speech.pcm"))
	if err != nil {
		t.Fatal(err)
	}
	pcm := append(make([]byte, 180*SampleRate*SampleBytes), voice...)
	activity, err := DetectSpeech(pcm)
	if err != nil || !activity.HasSpeech() || activity.VoicedSeconds < 1 {
		t.Fatalf("o VAD ignorou a fala no final da gravação: %+v / %v", activity, err)
	}
}

func TestSubdivideKeepsExactFinalSampleAndDoesNotChangeOriginalPCM(t *testing.T) {
	pcm := make([]byte, (17*SampleRate+23)*SampleBytes)
	for index := range pcm {
		pcm[index] = byte(index % 3)
	}
	// The first test validates VAD separately; this silent sample keeps the
	// cut test independent of an external encoder.
	clear(pcm)
	before := append([]byte(nil), pcm...)
	parent := Chunk{Start: 31.5, Duration: float64(len(pcm)) / (SampleBytes * SampleRate), PCM: pcm, Format: FormatWAV}
	children, err := new(Processor).Subdivide(t.Context(), parent)
	if err != nil || len(children) != 2 {
		t.Fatal(children, err)
	}
	end := children[1].Start + children[1].Duration
	if math.Abs(end-(parent.Start+parent.Duration)) > 1.0/SampleRate ||
		children[0].Start != parent.Start || children[1].Start > children[0].Start+children[0].Duration {
		t.Fatal("a subdivisão perdeu amostras ou criou um intervalo vazio", children)
	}
	for index := range pcm {
		if pcm[index] != before[index] {
			t.Fatal("a recuperação mudou o áudio original")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := new(Processor).Subdivide(ctx, parent); err != context.Canceled {
		t.Fatal("Cancelado ainda preparou filhos", err)
	}
}
