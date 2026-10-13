package realtime

import (
	"encoding/binary"
	"errors"
	"slices"
)

// EncodeWAV wraps received response PCM without resampling or changing its
// samples. Its rate is independent from the recorder's 16 kHz WAV format.
func EncodeWAV(pcm []byte, sampleRate int) ([]byte, error) {
	if len(pcm)%2 != 0 || !slices.Contains([]int{8000, 16000, 24000}, sampleRate) || uint64(len(pcm)) > 0xffffffff-36 {
		return nil, errors.New("PCM de resposta inválido para WAV mono de 16 bits")
	}
	wav := make([]byte, 44+len(pcm))
	copy(wav, "RIFF")
	binary.LittleEndian.PutUint32(wav[4:], uint32(len(wav)-8))
	copy(wav[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(wav[16:], 16)
	binary.LittleEndian.PutUint16(wav[20:], 1)
	binary.LittleEndian.PutUint16(wav[22:], 1)
	binary.LittleEndian.PutUint32(wav[24:], uint32(sampleRate))
	binary.LittleEndian.PutUint32(wav[28:], uint32(sampleRate*2))
	binary.LittleEndian.PutUint16(wav[32:], 2)
	binary.LittleEndian.PutUint16(wav[34:], 16)
	copy(wav[36:], "data")
	binary.LittleEndian.PutUint32(wav[40:], uint32(len(pcm)))
	copy(wav[44:], pcm)
	return wav, nil
}
