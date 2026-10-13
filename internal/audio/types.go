// Package audio prepares bounded, overlapping audio parts for transcription.
package audio

const (
	SampleRate    = 16000
	SampleBytes   = 2
	BitRate       = 64000
	MaxAudioBytes = 3*1024*1024 - 8192
)

type Chunk struct {
	Index    int
	Start    float64
	Duration float64
	Audio    []byte
	// PCM is an owned, bounded mono 16 kHz copy used only for file recovery.
	// It is never sent to a provider or persisted as a diagnostic.
	PCM           []byte
	Format        string
	HasSpeech     bool
	SpeechSeconds float64
}
