//go:build integration

package playback

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"ai-talker/internal/audio"
	"ai-talker/internal/platform"
	"ai-talker/internal/recorder"
	"ai-talker/internal/store"
)

func tonePCM(seconds, frequency float64) []byte {
	payload := make([]byte, int(seconds*audio.SampleRate)*2)
	for index := range len(payload) / 2 {
		sample := int16(6500 * math.Sin(2*math.Pi*frequency*float64(index)/audio.SampleRate))
		binary.LittleEndian.PutUint16(payload[index*2:], uint16(sample))
	}
	return payload
}

func tonePCMAtAmplitude(seconds, frequency float64, amplitude int16) []byte {
	payload := make([]byte, int(seconds*audio.SampleRate)*2)
	for index := range len(payload) / 2 {
		sample := int16(float64(amplitude) * math.Sin(2*math.Pi*frequency*float64(index)/audio.SampleRate))
		binary.LittleEndian.PutUint16(payload[index*2:], uint16(sample))
	}
	return payload
}

func playbackPCM(t *testing.T, processor *audio.Processor, source string) []byte {
	t.Helper()
	command := exec.CommandContext(t.Context(), processor.FFmpeg,
		"-v", "error", "-nostdin", "-i", source,
		"-af", "aformat=sample_fmts=fltp:channel_layouts=stereo",
		"-ar", "48000", "-ac", "1", "-f", "s16le", "pipe:1")
	platform.Hide(command)
	payload, err := command.Output()
	if err != nil {
		t.Fatal("decodificar o WAV preparado:", err)
	}
	return payload
}

func toneAmplitude(payload []byte, start, seconds, frequency float64) float64 {
	first, count := int(start*48000), int(seconds*48000)
	var cosine, sine float64
	for index := range count {
		sample := float64(int16(binary.LittleEndian.Uint16(payload[(first+index)*2:]))) / 32768
		angle := 2 * math.Pi * frequency * float64(first+index) / 48000
		cosine += sample * math.Cos(angle)
		sine += sample * math.Sin(angle)
	}
	return 2 * math.Hypot(cosine, sine) / float64(count)
}

func TestPlaybackPreparesImportedMP3WithoutChangingSource(t *testing.T) {
	processor, err := audio.New()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := processor.Encode(t.Context(), tonePCM(2, 440))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	source := filepath.Join(directory, "importado com acentuação.mp3")
	if err := os.WriteFile(source, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	prepared, err := prepareAudio(t.Context(), source, directory)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.remove()
	duration, err := processor.Duration(t.Context(), prepared.path)
	if err != nil || math.Abs(duration-2) > 0.2 {
		t.Fatal("a preparação perdeu o final do MP3:", duration, err)
	}
	pcm := playbackPCM(t, processor, prepared.path)
	if toneAmplitude(pcm, 0.5, 0.5, 440) < 0.1 {
		t.Fatal("o áudio importado ficou inaudível no WAV preparado")
	}
	after, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(encoded, after) {
		t.Fatal("ouvir modificou o arquivo MP3 original")
	}
}

func TestPlaybackMixesBothTeamSourcesAtTheirOriginalOffsets(t *testing.T) {
	processor, err := audio.New()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	mic, system := filepath.Join(directory, "mic.wav"), filepath.Join(directory, "system.wav")
	originals := make(map[string][]byte)
	for _, item := range []struct {
		path               string
		seconds, frequency float64
	}{{mic, 2, 440}, {system, 3, 880}} {
		wav, err := audio.EncodeWAV(t.Context(), tonePCM(item.seconds, item.frequency))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(item.path, wav, 0o600); err != nil {
			t.Fatal(err)
		}
		originals[item.path] = wav
	}
	manifest := recorder.TeamManifest{Version: 1, Tracks: []recorder.Track{
		{ID: "microphone", Snapshot: recorder.Snapshot{Path: mic}},
		{ID: "system", Offset: 0.25, Snapshot: recorder.Snapshot{Path: system}},
	}}
	if err := store.WriteJSON(mic+".team.json", manifest); err != nil {
		t.Fatal(err)
	}
	prepared, err := prepareAudio(t.Context(), mic, directory)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.remove()
	duration, err := processor.Duration(t.Context(), prepared.path)
	if err != nil || math.Abs(duration-3.25) > 0.001 {
		t.Fatal("a reprodução não cobre o fim da fonte mais longa:", duration, err)
	}
	pcm := playbackPCM(t, processor, prepared.path)
	microphoneBefore := toneAmplitude(pcm, 0.05, 0.1, 440)
	systemBefore := toneAmplitude(pcm, 0.05, 0.1, 880)
	// Mono-to-stereo routing and normalized mixing reduce amplitude. Compare
	// both sources to the measured microphone level instead of assuming a
	// particular rematrix coefficient in the codec.
	if microphoneBefore < 0.05 || systemBefore > microphoneBefore*0.05 {
		t.Fatal("o som do sistema foi adiantado ou o microfone ficou inaudível:", microphoneBefore, systemBefore)
	}
	for _, frequency := range []float64{440, 880} {
		if toneAmplitude(pcm, 0.5, 0.5, frequency) < microphoneBefore*0.8 {
			t.Fatal("a reprodução perdeu uma das fontes sobrepostas:", frequency)
		}
	}
	if toneAmplitude(pcm, 2.5, 0.5, 880) < 0.1 {
		t.Fatal("o áudio do sistema foi truncado no fim do microfone")
	}
	for path, before := range originals {
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("preparar a reprodução alterou a trilha original:", path)
		}
	}
}

func TestPlaybackSpeedPreservesVoicePitchAndSoftLimitsGainWithoutChangingSource(t *testing.T) {
	processor, err := audio.New()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	source := filepath.Join(directory, "voz-alta.wav")
	wav, err := audio.EncodeWAV(t.Context(), tonePCMAtAmplitude(4, 440, 30000))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, wav, 0o600); err != nil {
		t.Fatal(err)
	}
	prepared, err := prepareAudioWithOptions(t.Context(), source, directory, renderOptions{Speed: 2.5, Gain: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.remove()
	duration, err := processor.Duration(t.Context(), prepared.path)
	if err != nil || math.Abs(duration-1.6) > 0.08 {
		t.Fatal("a velocidade 2,5× não reduziu a duração do áudio:", duration, err)
	}
	pcm := playbackPCM(t, processor, prepared.path)
	fundamental := toneAmplitude(pcm, 0.4, 0.5, 440)
	harmonic := toneAmplitude(pcm, 0.4, 0.5, 1320)
	if fundamental < 0.7 || harmonic > 0.05 {
		t.Fatalf("a voz perdeu o tom ou sofreu clipping ao acelerar: 440 Hz=%.3f, 1320 Hz=%.3f", fundamental, harmonic)
	}
	var peak float64
	for index := range len(pcm) / 2 {
		sample := math.Abs(float64(int16(binary.LittleEndian.Uint16(pcm[index*2:])))) / 32768
		peak = max(peak, sample)
	}
	if peak > 0.99 {
		t.Fatalf("o limitador deixou pico em %.3f", peak)
	}
	after, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(wav, after) {
		t.Fatal("velocidade ou ganho alterou o WAV original")
	}
}

func TestPlaybackAllRatesAndIndependentGainsKeepPitchDurationAndOriginal(t *testing.T) {
	processor, err := audio.New()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	source := filepath.Join(directory, "tone.wav")
	wav, err := audio.EncodeWAV(t.Context(), tonePCM(4, 440))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, wav, 0o600); err != nil {
		t.Fatal(err)
	}
	renderSlots := make(chan struct{}, 4)
	for _, speed := range []float64{0.5, 0.75, 1, 1.25, 1.5, 2, 2.25, 2.5} {
		for _, gain := range []float64{0.4, 1, 2} {
			t.Run(fmt.Sprintf("%.2fx-%.0f%%", speed, gain*100), func(t *testing.T) {
				t.Parallel()
				select {
				case renderSlots <- struct{}{}:
					defer func() { <-renderSlots }()
				case <-t.Context().Done():
					t.Fatal(t.Context().Err())
				}
				prepared, err := prepareAudioWithOptions(t.Context(), source, t.TempDir(), renderOptions{
					Speed: speed, Gain: gain,
				})
				if err != nil {
					t.Fatal(err)
				}
				defer prepared.remove()
				duration, err := processor.Duration(t.Context(), prepared.path)
				if err != nil || math.Abs(duration-4/speed) > 0.08 {
					t.Fatalf("duração da cópia: %.4f, esperado %.4f: %v", duration, 4/speed, err)
				}
				pcm := playbackPCM(t, processor, prepared.path)
				fundamental := toneAmplitude(pcm, 0.2, 0.3, 440)
				// Route mono and stereo private copies through the same stereo
				// output matrix before measuring gain. Mono upmix contributes
				// sqrt(1/2) per channel, independent of speed or volume.
				want := 6500.0 / 32768 * gain / math.Sqrt2
				if math.Abs(fundamental-want) > want*0.06 {
					t.Fatalf("ganho efetivo ou tom mudou: 440 Hz=%.4f, esperado %.4f", fundamental, want)
				}
				if speed != 1 && toneAmplitude(pcm, 0.2, 0.3, 440*speed) > 0.02 {
					t.Fatal("acelerar elevou a frequência da voz")
				}
				original, err := os.ReadFile(source)
				if err != nil || !bytes.Equal(original, wav) {
					t.Fatal("velocidade ou volume alterou o WAV original", err)
				}
			})
		}
	}
}
