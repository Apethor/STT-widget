package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"testing"
)

func TestEncodeWAVPreservesMonoPCMAndMetadata(t *testing.T) {
	pcm := []byte{0, 0, 1, 0, 255, 127, 0, 128}
	wav, err := EncodeWAV(t.Context(), pcm)
	if err != nil {
		t.Fatal(err)
	}
	if string(wav[:4]) != "RIFF" || string(wav[8:16]) != "WAVEfmt " || string(wav[36:40]) != "data" ||
		binary.LittleEndian.Uint32(wav[4:]) != uint32(len(wav)-8) ||
		binary.LittleEndian.Uint16(wav[20:]) != 1 || binary.LittleEndian.Uint16(wav[22:]) != 1 ||
		binary.LittleEndian.Uint32(wav[24:]) != SampleRate || binary.LittleEndian.Uint16(wav[34:]) != 16 ||
		binary.LittleEndian.Uint32(wav[40:]) != uint32(len(pcm)) || !bytes.Equal(wav[44:], pcm) {
		t.Fatal("o WAV perdeu amostras ou não descreve PCM mono de 16 kHz e 16 bits")
	}
	wav[44] = 7
	if pcm[0] != 0 {
		t.Fatal("editar o WAV alterou as amostras de origem")
	}
}

func TestEncodeWAVRejectsInvalidAndCancelledParts(t *testing.T) {
	for _, pcm := range [][]byte{nil, {0}, make([]byte, 300*SampleRate*SampleBytes+2)} {
		if _, err := EncodeWAV(t.Context(), pcm); err == nil {
			t.Fatal("uma parte vazia, incompleta ou grande demais foi aceita")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := EncodeWAV(ctx, []byte{0, 0}); !errors.Is(err, context.Canceled) {
		t.Fatalf("preparar WAV ignorou o cancelamento: %v", err)
	}
}
