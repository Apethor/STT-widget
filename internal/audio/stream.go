package audio

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
)

const StreamPreparationVersion = 2
const maxStreamSeconds = 15

// PrepareLive accepts only the bounded PCM WAVs exported by the recorder.
// Their format is already decoded, so neither ffprobe nor ffmpeg is needed.
// Imported audio continues to use the general codec path.
func (p *Processor) PrepareLive(ctx context.Context, source string) (chunk Chunk, err error) {
	return p.PrepareLiveWithContext(ctx, source, 0)
}

// PrepareLiveWithContext sends preceding audio as recognition context, but
// only new samples can trigger speech detection. Otherwise a pause repeatedly
// transcribes the previous words and can produce invented continuations.
func (p *Processor) PrepareLiveWithContext(ctx context.Context, source string, contextSamples uint64) (chunk Chunk, err error) {
	if err := ctx.Err(); err != nil {
		return chunk, err
	}
	file, err := os.Open(source)
	if err != nil {
		return chunk, fmt.Errorf("abrir o trecho gravado: %w", err)
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	header := make([]byte, 44)
	if _, err := io.ReadFull(file, header); err != nil {
		return chunk, fmt.Errorf("ler o cabeçalho do trecho gravado: %w", err)
	}
	dataBytes := binary.LittleEndian.Uint32(header[40:])
	if string(header[:4]) != "RIFF" || string(header[8:16]) != "WAVEfmt " ||
		string(header[36:40]) != "data" || binary.LittleEndian.Uint32(header[16:]) != 16 ||
		binary.LittleEndian.Uint16(header[20:]) != 1 || binary.LittleEndian.Uint16(header[22:]) != 1 ||
		binary.LittleEndian.Uint32(header[24:]) != SampleRate ||
		binary.LittleEndian.Uint32(header[28:]) != SampleRate*SampleBytes ||
		binary.LittleEndian.Uint16(header[32:]) != SampleBytes ||
		binary.LittleEndian.Uint16(header[34:]) != 16 ||
		dataBytes == 0 || dataBytes%SampleBytes != 0 ||
		dataBytes > maxStreamSeconds*SampleRate*SampleBytes ||
		binary.LittleEndian.Uint32(header[4:]) != 36+dataBytes {
		return chunk, errors.New("o trecho do gravador precisa ser WAV PCM mono, 16 kHz, com até 15 segundos")
	}
	details, err := file.Stat()
	if err != nil {
		return chunk, fmt.Errorf("verificar o trecho gravado: %w", err)
	}
	if details.Size() != 44+int64(dataBytes) {
		return chunk, errors.New("o tamanho do trecho gravado não corresponde ao cabeçalho WAV")
	}
	if contextSamples >= uint64(dataBytes/SampleBytes) {
		return chunk, errors.New("o contexto do trecho precisa deixar amostras novas para transcrever")
	}
	pcm := make([]byte, int(dataBytes))
	if _, err := io.ReadFull(file, pcm); err != nil {
		return chunk, fmt.Errorf("ler as amostras do trecho gravado: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return chunk, err
	}
	filterStreamSpeech(pcm)
	activity, err := DetectSpeech(pcm[int(contextSamples)*SampleBytes:])
	if err != nil {
		return chunk, err
	}
	chunk = Chunk{
		Duration:  float64(dataBytes) / (SampleRate * SampleBytes),
		HasSpeech: activity.HasSpeech(), SpeechSeconds: activity.VoicedSeconds, Format: FormatWAV,
	}
	if chunk.HasSpeech {
		chunk.Audio, err = EncodeWAV(ctx, pcm)
	}
	return chunk, err
}

type speechBiquad struct {
	b0, b1, b2, a1, a2 float64
	first, second      float64
}

// The codec path uses the same two-pole Butterworth highpass/lowpass defaults.
// Resetting at each closed clip also matches a fresh decoder's filter state.
func newSpeechBiquad(frequency float64, highpass bool) speechBiquad {
	omega := 2 * math.Pi * frequency / SampleRate
	cosine := math.Cos(omega)
	alpha := math.Sin(omega) / math.Sqrt2
	b0, b1 := (1-cosine)/2, 1-cosine
	if highpass {
		b0, b1 = (1+cosine)/2, -(1 + cosine)
	}
	a0 := 1 + alpha
	return speechBiquad{
		b0: b0 / a0, b1: b1 / a0, b2: b0 / a0,
		a1: -2 * cosine / a0, a2: (1 - alpha) / a0,
	}
}

func (f *speechBiquad) sample(input float64) float64 {
	output := f.b0*input + f.first
	f.first = f.b1*input - f.a1*output + f.second
	f.second = f.b2*input - f.a2*output
	return output
}

func filterStreamSpeech(pcm []byte) {
	high, low := newSpeechBiquad(80, true), newSpeechBiquad(7000, false)
	for index := 0; index < len(pcm); index += SampleBytes {
		input := float64(int16(binary.LittleEndian.Uint16(pcm[index:])))
		output := low.sample(high.sample(input))
		sample := int16(math.Round(max(-32768, min(32767, output))))
		binary.LittleEndian.PutUint16(pcm[index:], uint16(sample))
	}
}
