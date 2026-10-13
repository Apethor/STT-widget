package audio

import (
	"encoding/binary"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"
)

func TestDetectSpeech(t *testing.T) {
	normal, err := os.ReadFile(filepath.Join("testdata", "speech.pcm"))
	if err != nil {
		t.Fatal(err)
	}
	quiet := make([]byte, len(normal))
	for offset := 0; offset < len(normal); offset += 2 {
		sample := int16(binary.LittleEndian.Uint16(normal[offset:]))
		binary.LittleEndian.PutUint16(quiet[offset:], uint16(int16(float64(sample)*0.05)))
	}
	short, err := os.ReadFile(filepath.Join("testdata", "short-word.pcm"))
	if err != nil {
		t.Fatal(err)
	}
	noise := make([]byte, SampleRate*SampleBytes*2)
	random := rand.New(rand.NewPCG(42, 43))
	for offset := 0; offset < len(noise); offset += 2 {
		binary.LittleEndian.PutUint16(noise[offset:], uint16(int16(random.IntN(61)-30)))
	}
	binary.LittleEndian.PutUint16(noise[20000:], 3000)
	tests := []struct {
		name   string
		pcm    []byte
		speech bool
	}{
		{name: "normal speech", pcm: normal, speech: true},
		{name: "quiet speech", pcm: quiet, speech: true},
		{name: "single word", pcm: short, speech: true},
		{name: "silence", pcm: make([]byte, SampleRate*SampleBytes*3)},
		{name: "background noise and click", pcm: noise},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			activity, err := DetectSpeech(test.pcm)
			if err != nil {
				t.Fatal(err)
			}
			if activity.HasSpeech() != test.speech {
				t.Fatalf("speech = %v, want %v (%+v)", activity.HasSpeech(), test.speech, activity)
			}
		})
	}
}
