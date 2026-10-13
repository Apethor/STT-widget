package audio

import (
	"context"
	"encoding/binary"
	"errors"
)

const (
	FormatMP3 = "mp3"
	FormatWAV = "wav"
)

// EncodeWAV preserves the decoded PCM for local inference instead of applying
// the compression used by Bedrock. The caller bounds a part to 300 seconds.
func EncodeWAV(ctx context.Context, pcm []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(pcm) == 0 || len(pcm)%SampleBytes != 0 || len(pcm) > 300*SampleRate*SampleBytes {
		return nil, errors.New("as amostras WAV devem cobrir até 300 segundos de áudio mono")
	}
	wav := make([]byte, 44+len(pcm))
	copy(wav, "RIFF")
	binary.LittleEndian.PutUint32(wav[4:], uint32(36+len(pcm)))
	copy(wav[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(wav[16:], 16)
	binary.LittleEndian.PutUint16(wav[20:], 1)
	binary.LittleEndian.PutUint16(wav[22:], 1)
	binary.LittleEndian.PutUint32(wav[24:], SampleRate)
	binary.LittleEndian.PutUint32(wav[28:], SampleRate*SampleBytes)
	binary.LittleEndian.PutUint16(wav[32:], SampleBytes)
	binary.LittleEndian.PutUint16(wav[34:], SampleBytes*8)
	copy(wav[36:], "data")
	binary.LittleEndian.PutUint32(wav[40:], uint32(len(pcm)))
	copy(wav[44:], pcm)
	return wav, nil
}
