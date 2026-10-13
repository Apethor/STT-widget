package recorder

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

// Inspect reads the finalized mono s16 WAV format produced by Recorder.
func Inspect(path string) (snapshot Snapshot, err error) {
	file, err := os.Open(path)
	if err != nil {
		return snapshot, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	var header [waveHeaderBytes]byte
	if _, err := io.ReadFull(file, header[:]); err != nil {
		return snapshot, err
	}
	if string(header[:4]) != "RIFF" || string(header[8:16]) != "WAVEfmt " ||
		binary.LittleEndian.Uint32(header[16:20]) != 16 ||
		binary.LittleEndian.Uint16(header[20:22]) != 1 ||
		binary.LittleEndian.Uint16(header[22:24]) != 1 ||
		binary.LittleEndian.Uint16(header[32:34]) != 2 ||
		binary.LittleEndian.Uint16(header[34:36]) != 16 ||
		string(header[36:40]) != "data" {
		return snapshot, errors.New("a trilha não é um WAV mono PCM s16 do gravador")
	}
	rate := int(binary.LittleEndian.Uint32(header[24:28]))
	length := uint64(binary.LittleEndian.Uint32(header[40:44]))
	if rate != preferredSampleRate && rate != fallbackSampleRate || length%2 != 0 {
		return snapshot, ErrUnsupportedFormat
	}
	details, err := file.Stat()
	if err != nil {
		return snapshot, err
	}
	if uint64(details.Size()) < waveHeaderBytes+length {
		return snapshot, fmt.Errorf("a trilha contém menos amostras que seu cabeçalho: %d", details.Size())
	}
	return Snapshot{Path: path, SampleRate: rate, WrittenSamples: length / 2, Duration: float64(length/2) / float64(rate)}, nil
}
