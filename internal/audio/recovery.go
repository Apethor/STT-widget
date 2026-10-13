package audio

import (
	"context"
	"errors"
	"math"
)

// Subdivide cuts the owned PCM rather than decoding compressed audio again.
// A one-second overlap protects words at the middle boundary. Both children
// retain exact sample-derived coverage through the parent's final sample.
func (p *Processor) Subdivide(ctx context.Context, parent Chunk) ([]Chunk, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	samples := len(parent.PCM) / SampleBytes
	if samples < 2*SampleRate || len(parent.PCM)%SampleBytes != 0 ||
		math.Abs(float64(samples)/SampleRate-parent.Duration) > 1.0/SampleRate {
		return nil, errors.New("as amostras desta parte não estão disponíveis para subdividir")
	}
	middle := samples / 2
	overlap := min(SampleRate/2, middle/4)
	ranges := [][2]int{{0, middle + overlap}, {middle - overlap, samples}}
	children := make([]Chunk, 0, 2)
	for index, span := range ranges {
		pcm := parent.PCM[span[0]*SampleBytes : span[1]*SampleBytes]
		activity, err := DetectSpeech(pcm)
		if err != nil {
			return nil, err
		}
		child := Chunk{
			Index: index, Start: parent.Start + float64(span[0])/SampleRate,
			Duration: float64(span[1]-span[0]) / SampleRate, Format: parent.Format,
			PCM: pcm, HasSpeech: activity.HasSpeech(), SpeechSeconds: activity.VoicedSeconds,
		}
		if child.HasSpeech {
			encode := p.Encode
			if parent.Format == FormatWAV {
				encode = EncodeWAV
			}
			child.Audio, err = encode(ctx, pcm)
			if err != nil {
				return nil, err
			}
		}
		children = append(children, child)
	}
	return children, nil
}

func (p *Processor) RecoveryWAV(ctx context.Context, chunk Chunk) (Chunk, error) {
	if len(chunk.PCM) == 0 {
		if chunk.Format == FormatWAV {
			return chunk, ctx.Err()
		}
		return chunk, errors.New("as amostras desta parte não estão disponíveis para o Whisper")
	}
	wav, err := EncodeWAV(ctx, chunk.PCM)
	if err != nil {
		return chunk, err
	}
	chunk.Audio, chunk.Format = wav, FormatWAV
	return chunk, nil
}
