package recorder

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
)

const (
	waveHeaderBytes = 44
	// Both the RIFF size (36 + PCM bytes) and PCM length are uint32. Keep
	// complete s16 samples and reject the next block before either can wrap.
	maxRIFFDataBytes = uint64(math.MaxUint32-36) &^ uint64(1)
)

type recordingFile interface {
	io.Writer
	io.Seeker
	io.Closer
	Sync() error
}

type waveWriter struct {
	file       recordingFile
	sampleRate int
	dataBytes  uint64
	closed     bool
	closeErr   error
}

func newWaveWriter(file recordingFile, sampleRate int) (*waveWriter, error) {
	if file == nil {
		return nil, errors.New("arquivo de gravação ausente")
	}
	if sampleRate <= 0 || uint64(sampleRate) > math.MaxUint32/2 {
		return nil, fmt.Errorf("taxa de amostragem inválida: %d", sampleRate)
	}
	w := &waveWriter{file: file, sampleRate: sampleRate}
	if err := w.writeHeader(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *waveWriter) writePCM(pcm []byte) error {
	if w.closed {
		return errors.New("o arquivo de gravação já foi encerrado")
	}
	if len(pcm)%2 != 0 {
		return errors.New("bloco PCM com amostra s16 incompleta")
	}
	if uint64(len(pcm)) > maxRIFFDataBytes-w.dataBytes {
		return ErrRIFFLimit
	}
	n, err := w.file.Write(pcm)
	if n < 0 || n > len(pcm) {
		return errors.New("o arquivo retornou uma contagem de bytes inválida")
	}
	w.dataBytes += uint64(n)
	if err != nil {
		return err
	}
	if n != len(pcm) {
		return io.ErrShortWrite
	}
	return nil
}

func (w *waveWriter) writeHeader() error {
	header := make([]byte, waveHeaderBytes)
	copy(header[0:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], uint32(36+w.dataBytes))
	copy(header[8:12], "WAVE")
	copy(header[12:16], "fmt ")
	binary.LittleEndian.PutUint32(header[16:20], 16)
	binary.LittleEndian.PutUint16(header[20:22], 1)
	binary.LittleEndian.PutUint16(header[22:24], 1)
	binary.LittleEndian.PutUint32(header[24:28], uint32(w.sampleRate))
	binary.LittleEndian.PutUint32(header[28:32], uint32(w.sampleRate)*2)
	binary.LittleEndian.PutUint16(header[32:34], 2)
	binary.LittleEndian.PutUint16(header[34:36], 16)
	copy(header[36:40], "data")
	binary.LittleEndian.PutUint32(header[40:44], uint32(w.dataBytes))
	n, err := w.file.Write(header)
	if err != nil {
		return err
	}
	if n != len(header) {
		return io.ErrShortWrite
	}
	return nil
}

func (w *waveWriter) close() error {
	if w.closed {
		return w.closeErr
	}
	w.closed = true
	// A failed write can end in half a sample. Keep every complete sample and
	// describe only those bytes in the header; the orphan byte remains local.
	w.dataBytes &^= 1
	if _, err := w.file.Seek(0, io.SeekStart); err != nil {
		w.closeErr = fmt.Errorf("não foi possível reposicionar o cabeçalho WAV: %w", err)
	} else if err := w.writeHeader(); err != nil {
		w.closeErr = fmt.Errorf("não foi possível atualizar o cabeçalho WAV: %w", err)
	}
	w.closeErr = errors.Join(w.closeErr, w.file.Sync(), w.file.Close())
	return w.closeErr
}
