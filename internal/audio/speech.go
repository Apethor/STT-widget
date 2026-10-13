package audio

import (
	"encoding/binary"
	"fmt"
	"math"
	"runtime"

	webrtcvad "github.com/maxhawkins/go-webrtcvad"
)

const SpeechDetectorVersion = 2

type Activity struct {
	DurationSeconds   float64
	VoicedSeconds     float64
	LongestRunSeconds float64
}

func (a Activity) HasSpeech() bool {
	if a.VoicedSeconds+1e-9 >= 0.12 && a.LongestRunSeconds+1e-9 >= 0.09 {
		return true
	}
	// A short utterance may contain only two or three voiced 30 ms frames.
	return a.DurationSeconds > 0 && a.DurationSeconds <= 4 &&
		a.VoicedSeconds+1e-9 >= 0.06 && a.LongestRunSeconds+1e-9 >= 0.06
}

func DetectSpeech(pcm []byte) (Activity, error) {
	if len(pcm)%SampleBytes != 0 {
		return Activity{}, fmt.Errorf("o áudio PCM contém uma amostra incompleta")
	}
	vad, err := webrtcvad.New()
	if err != nil {
		return Activity{}, fmt.Errorf("iniciar a detecção de fala: %w", err)
	}
	if err := vad.SetMode(2); err != nil {
		return Activity{}, fmt.Errorf("configurar a detecção de fala: %w", err)
	}
	// The wrapper owns C memory through a finalizer; keep the Go owner alive
	// until every synchronous C processing call has returned.
	defer runtime.KeepAlive(vad)
	const frameBytes = SampleRate * 30 / 1000 * SampleBytes
	frame := make([]byte, frameBytes)
	activity := Activity{DurationSeconds: float64(len(pcm)) / (SampleRate * SampleBytes)}
	var currentRun float64
	for start := 0; start < len(pcm); start += frameBytes {
		payload := pcm[start:min(start+frameBytes, len(pcm))]
		clear(frame)
		copy(frame, payload)
		voiced, err := vad.Process(SampleRate, frame)
		if err != nil {
			return Activity{}, fmt.Errorf("detectar fala no áudio: %w", err)
		}
		if voiced {
			var sum float64
			for sample := 0; sample < len(frame); sample += 2 {
				value := float64(int16(binary.LittleEndian.Uint16(frame[sample:])))
				sum += value * value
			}
			voiced = math.Sqrt(sum/float64(len(frame)/2)) >= 35
		}
		if !voiced {
			currentRun = 0
			continue
		}
		duration := float64(len(payload)) / (SampleRate * SampleBytes)
		activity.VoicedSeconds += duration
		currentRun += duration
		activity.LongestRunSeconds = max(activity.LongestRunSeconds, currentRun)
	}
	return activity, nil
}
