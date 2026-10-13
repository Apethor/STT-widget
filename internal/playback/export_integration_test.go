//go:build integration

package playback

import (
	"bytes"
	"context"
	"encoding/json"
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

func TestTeamExportIsOne192KMP3WithBothSourcesAndOriginalOffsets(t *testing.T) {
	processor, err := audio.New()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	mic, system := filepath.Join(directory, "mic.wav"), filepath.Join(directory, "sis.wav")
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
	if err := store.WriteJSON(mic+".team.json", recorder.TeamManifest{Version: 1, Tracks: []recorder.Track{
		{ID: "microphone", Snapshot: recorder.Snapshot{Path: mic}},
		{ID: "system", Offset: 0.25, Snapshot: recorder.Snapshot{Path: system}},
	}}); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(directory, "exportado.mp3")
	if err := ExportMP3(t.Context(), mic, destination, directory); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), processor.FFprobe, "-v", "error", "-show_entries",
		"stream=codec_name,bit_rate,sample_rate,channels", "-of", "json", destination)
	platform.Hide(command)
	payload, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	var probe struct {
		Streams []struct {
			Codec    string `json:"codec_name"`
			BitRate  string `json:"bit_rate"`
			Rate     string `json:"sample_rate"`
			Channels int    `json:"channels"`
		} `json:"streams"`
	}
	if json.Unmarshal(payload, &probe) != nil || len(probe.Streams) != 1 ||
		probe.Streams[0].Codec != "mp3" || probe.Streams[0].BitRate != "192000" ||
		probe.Streams[0].Channels != 2 {
		t.Fatal("o arquivo não é um único MP3 de 192 kb/s", string(payload))
	}
	duration, err := processor.Duration(t.Context(), destination)
	if err != nil || math.Abs(duration-3.25) > 0.12 {
		t.Fatal("o MP3 perdeu o final da fonte mais longa", duration, err)
	}
	pcm := playbackPCM(t, processor, destination)
	microphoneBefore := toneAmplitude(pcm, 0.05, 0.1, 440)
	if microphoneBefore < 0.04 || toneAmplitude(pcm, 0.05, 0.1, 880) > microphoneBefore*0.1 ||
		toneAmplitude(pcm, 0.5, 0.5, 440) < 0.04 || toneAmplitude(pcm, 0.5, 0.5, 880) < 0.04 ||
		toneAmplitude(pcm, 2.5, 0.5, 880) < 0.08 {
		t.Fatal("a exportação adiantou, truncou ou descartou uma fonte")
	}
	for path, original := range originals {
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(original, after) {
			t.Fatal("exportar alterou uma faixa original", path)
		}
	}
	previous, _ := os.ReadFile(destination)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := ExportMP3(ctx, mic, destination, directory); err == nil {
		t.Fatal("exportar prosseguiu após cancelamento")
	}
	after, _ := os.ReadFile(destination)
	if !bytes.Equal(previous, after) {
		t.Fatal("uma exportação cancelada apagou o destino existente")
	}
}
