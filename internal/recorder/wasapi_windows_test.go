//go:build windows

package recorder

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"ai-talker/internal/audio"

	"golang.org/x/sys/windows"
)

// This opt-in check opens physical devices but retains no PCM or audio files.
func TestWASAPISharedIntegration(t *testing.T) {
	if os.Getenv("AI_TALKER_SHARED_AUDIO_TEST") != "1" {
		t.Skip("physical audio integration is opt-in")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	microphones, err := SharedMicrophones()
	if err != nil || len(microphones) == 0 {
		t.Fatalf("enumerate microphones: %v", err)
	}
	outputs, err := PlaybackDevices()
	if err != nil || len(outputs) == 0 {
		t.Fatalf("enumerate playback endpoints: %v", err)
	}
	var samples [3]atomic.Uint64
	failures := make(chan error, 8)
	for index := range 3 {
		input := &sharedInput{loopback: index == 2}
		if err := input.Open(ctx, -1, 16000); err != nil {
			t.Fatalf("open source %d alongside other clients: %v", index, err)
		}
		t.Cleanup(func() {
			if err := input.Close(); err != nil {
				t.Errorf("release source %d: %v", index, err)
			}
		})
		if err := input.Start(ctx, func(pcm []byte, err error) {
			samples[index].Add(uint64(len(pcm) / 2))
			if err != nil && !errors.Is(err, context.Canceled) {
				select {
				case failures <- err:
				default:
				}
			}
		}); err != nil {
			t.Fatalf("start source %d: %v", index, err)
		}
		if err := input.Start(ctx, func([]byte, error) {}); !errors.Is(err, ErrAlreadyRecording) {
			t.Fatal("second Start did not reject an already started input")
		}
	}
	timer := time.NewTimer(800 * time.Millisecond)
	defer timer.Stop()
	select {
	case err := <-failures:
		t.Fatal(err)
	case <-timer.C:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	for index := range 3 {
		if samples[index].Load() < 1000 {
			t.Fatalf("source %d did not deliver samples alongside other clients: %d", index, samples[index].Load())
		}
	}
	t.Logf("two shared microphones and loopback at 16000 Hz: %d / %d / %d samples; %d microphones, %d outputs",
		samples[0].Load(), samples[1].Load(), samples[2].Load(), len(microphones), len(outputs))
}

// Sample counts alone cannot prove loopback works: silence is deliberately
// padded to preserve time. Detect the frequency of an actually played tone.
// No captured samples are retained or sent to a recognition service.
func TestWASAPILoopbackReceivesPlayedSyntheticTone(t *testing.T) {
	if os.Getenv("AI_TALKER_LOOPBACK_PLAYBACK_TEST") != "1" {
		t.Skip("playing a short synthetic signal is opt-in")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	const rate, frequency = 16000, 1000
	pcm := make([]byte, rate*2*6/10)
	for index := range len(pcm) / 2 {
		value := int16(1200 * math.Sin(2*math.Pi*frequency*float64(index)/rate))
		binary.LittleEndian.PutUint16(pcm[index*2:], uint16(value))
	}
	wav, err := audio.EncodeWAV(ctx, pcm)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "synthetic-tone.wav")
	if err := os.WriteFile(path, wav, 0o600); err != nil {
		t.Fatal(err)
	}
	input := &sharedInput{loopback: true}
	if err := input.Open(ctx, -1, rate); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := input.Close(); err != nil {
			t.Error(err)
		}
	})
	failures := make(chan error, 1)
	var matched, total atomic.Uint64
	if err := input.Start(ctx, func(pcm []byte, err error) {
		if err != nil && !errors.Is(err, context.Canceled) {
			select {
			case failures <- err:
			default:
			}
		}
		frames := len(pcm) / 2
		total.Add(uint64(frames))
		var real, imaginary, energy float64
		for index := range frames {
			value := float64(int16(binary.LittleEndian.Uint16(pcm[index*2:])))
			sine, cosine := math.Sincos(2 * math.Pi * frequency * float64(index) / rate)
			real += value * cosine
			imaginary += value * sine
			energy += value * value
		}
		if frames > 0 && energy > float64(frames)*16 &&
			2*(real*real+imaginary*imaginary)/(float64(frames)*energy) > 0.75 {
			matched.Add(uint64(frames))
		}
	}); err != nil {
		t.Fatal(err)
	}
	play := windows.NewLazySystemDLL("winmm.dll").NewProc("PlaySoundW")
	filename, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	// SND_FILENAME | SND_ASYNC | SND_NODEFAULT; normal default output.
	ok, _, _ := play.Call(uintptr(unsafe.Pointer(filename)), 0, 0x20003)
	if ok == 0 {
		t.Fatal("Windows could not play the synthetic WAV")
	}
	t.Cleanup(func() {
		play.Call(0, 0, 0)
		runtime.KeepAlive(filename)
	})
	timer := time.NewTimer(1100 * time.Millisecond)
	defer timer.Stop()
	select {
	case err := <-failures:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	case <-timer.C:
	}
	if matched.Load() < 1600 {
		t.Fatalf("loopback returned samples but not the played 1 kHz signal: matched=%d total=%d", matched.Load(), total.Load())
	}
	t.Logf("the default system output delivered the played 1 kHz signal: matched=%d total=%d at %d Hz",
		matched.Load(), total.Load(), rate)
}
