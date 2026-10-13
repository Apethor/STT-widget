package playback

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"ai-talker/internal/audio"
	"ai-talker/internal/recorder"
	"ai-talker/internal/store"
)

func TestPrepareRecorderAudioNeedsNoCodecAndKeepsOriginal(t *testing.T) {
	t.Setenv("AI_TALKER_FFMPEG", "ffmpeg-inexistente-para-este-teste")
	directory := t.TempDir()
	source := filepath.Join(directory, "original.wav")
	before := silenceFixture(t, source, 2)
	prepared, err := prepareAudio(t.Context(), source, directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prepared.remove() })
	copy, err := os.ReadFile(prepared.path)
	if err != nil || prepared.path == source || !bytes.Equal(copy, before) {
		t.Fatal("o WAV do gravador foi decodificado ou alterado")
	}
	if err := prepared.remove(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("a limpeza removeu o original")
	}
}

func TestPrepareIndependentQuietPlayersChangesOnlyTheirPrivatePCMWithoutCodec(t *testing.T) {
	t.Setenv("AI_TALKER_FFMPEG", "ffmpeg-inexistente-para-este-teste")
	root := t.TempDir()
	source := filepath.Join(root, "original.wav")
	pcm := make([]byte, 6)
	for index, sample := range []int16{12000, -12000, 0} {
		binary.LittleEndian.PutUint16(pcm[index*2:], uint16(sample))
	}
	wav, err := audio.EncodeWAV(t.Context(), pcm)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, wav, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		name string
		gain float64
		want int16
	}{
		{name: "player a 25%", gain: 0.25, want: 3000},
		{name: "efeito a 80%", gain: 0.8, want: 9600},
		{name: "silêncio", gain: 0, want: 0},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			prepared, err := prepareAudioWithOptions(t.Context(), source, t.TempDir(), renderOptions{
				Speed: 1, Gain: scenario.gain,
			})
			if err != nil {
				t.Fatal(err)
			}
			defer prepared.remove()
			payload, err := os.ReadFile(prepared.path)
			if err != nil {
				t.Fatal(err)
			}
			got := int16(binary.LittleEndian.Uint16(payload[44:]))
			if got != scenario.want || !bytes.Equal(wav[:44], payload[:44]) {
				t.Fatalf("o volume privado alterou a duração ou não foi aplicado: amostra=%d", got)
			}
			original, err := os.ReadFile(source)
			if err != nil || !bytes.Equal(original, wav) {
				t.Fatal("o controle privado alterou o WAV original", err)
			}
		})
	}
}

func TestPrepareQuietCopyPreservesTrailingWAVMetadata(t *testing.T) {
	t.Setenv("AI_TALKER_FFMPEG", "ffmpeg-inexistente-para-este-teste")
	directory := t.TempDir()
	source := filepath.Join(directory, "importado-com-metadados.wav")
	wav, err := audio.EncodeWAV(t.Context(), []byte{0x10, 0x27, 0xf0, 0xd8})
	if err != nil {
		t.Fatal(err)
	}
	trailer := []byte{'L', 'I', 'S', 'T', 4, 0, 0, 0, 'I', 'N', 'F', 'O'}
	before := append(wav, trailer...)
	binary.LittleEndian.PutUint32(before[4:8], uint32(len(before)-8))
	if err := os.WriteFile(source, before, 0o600); err != nil {
		t.Fatal(err)
	}
	prepared, err := prepareAudioWithOptions(t.Context(), source, directory, renderOptions{Speed: 1, Gain: 0.5})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prepared.remove() })
	private, err := os.ReadFile(prepared.path)
	if err != nil || len(private) != len(before) ||
		!bytes.Equal(private[:44], before[:44]) || !bytes.Equal(private[len(wav):], trailer) {
		t.Fatal("a cópia privada descartou metadados ou perdeu o tamanho RIFF válido", err)
	}
	if sample := int16(binary.LittleEndian.Uint16(private[44:])); sample != 5000 {
		t.Fatal("preservar os metadados deixou de aplicar o volume privado:", sample)
	}
	after, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("ajustar o volume modificou o WAV importado original", err)
	}
}

func TestPrepareCancellationAndDirectoryFailureLeaveNoAudio(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "original.wav")
	before := silenceFixture(t, source, 1)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := prepareAudio(ctx, source, directory); !errors.Is(err, context.Canceled) {
		t.Fatal("o preparo ignorou o cancelamento")
	}
	blocked := filepath.Join(directory, "arquivo-em-vez-de-pasta")
	if err := os.WriteFile(blocked, []byte("preservar"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareAudio(t.Context(), source, blocked); err == nil {
		t.Fatal("a pasta indisponível não retornou uma falha recuperável")
	}
	after, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(after, before) {
		t.Fatal("a falha de preparo modificou o original")
	}
}

func TestRecordingInputsRetainBothTeamSourcesAndOffsets(t *testing.T) {
	directory := t.TempDir()
	first, second := filepath.Join(directory, "mic.wav"), filepath.Join(directory, "system.wav")
	silenceFixture(t, first, 2)
	silenceFixture(t, second, 1)
	manifest := recorder.TeamManifest{Version: 1, Tracks: []recorder.Track{
		{ID: "microphone", Snapshot: recorder.Snapshot{Path: first}},
		{ID: "system", Offset: 0.250013, Snapshot: recorder.Snapshot{Path: second}},
	}}
	if err := store.WriteJSON(first+".team.json", manifest); err != nil {
		t.Fatal(err)
	}
	inputs, err := recordingInputs(first)
	if err != nil || len(inputs) != 2 || inputs[0].path != first || inputs[1].path != second ||
		inputs[1].offset != 0.250013 {
		t.Fatal("ouvir Equipe perdeu uma fonte ou o alinhamento:", inputs, err)
	}
}

func TestRecordingInputsRejectForeignOrInvalidTeamMetadata(t *testing.T) {
	for _, scenario := range []string{"foreign", "duplicate", "negative-offset", "other-microphone", "broken-json"} {
		t.Run(scenario, func(t *testing.T) {
			directory := t.TempDir()
			first, second := filepath.Join(directory, "mic.wav"), filepath.Join(directory, "system.wav")
			silenceFixture(t, first, 1)
			silenceFixture(t, second, 1)
			manifest := recorder.TeamManifest{Version: 1, Tracks: []recorder.Track{
				{ID: "microphone", Snapshot: recorder.Snapshot{Path: first}},
				{ID: "system", Snapshot: recorder.Snapshot{Path: second}},
			}}
			switch scenario {
			case "foreign":
				manifest.Tracks[1].Snapshot.Path = filepath.Join(t.TempDir(), "outside.wav")
				silenceFixture(t, manifest.Tracks[1].Snapshot.Path, 1)
			case "duplicate":
				manifest.Tracks[1].ID = "microphone"
			case "negative-offset":
				manifest.Tracks[1].Offset = -1
			case "other-microphone":
				manifest.Tracks[0].Snapshot.Path = second
			}
			if err := store.WriteJSON(first+".team.json", manifest); err != nil {
				t.Fatal(err)
			}
			if scenario == "broken-json" {
				if err := os.WriteFile(first+".team.json", []byte("{"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := recordingInputs(first); err == nil {
				t.Fatal("metadados inválidos foram aceitos para reprodução")
			}
		})
	}
}
