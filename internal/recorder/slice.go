package recorder

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
)

// ExportSlice copies only complete, already written PCM samples to a finalized
// WAV. It also works while the source recorder is still writing its WAV header.
// Bounds are samples in the recording, excluding paused intervals.
func ExportSlice(ctx context.Context, snapshot Snapshot, start, end uint64, destination string) (err error) {
	if ctx == nil {
		return errors.New("contexto do trecho ausente")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if snapshot.Path == "" || snapshot.SampleRate <= 0 || snapshot.SampleRate > 48000 ||
		start >= end || end > snapshot.WrittenSamples || end > (math.MaxInt64-waveHeaderBytes)/2 {
		return errors.New("o trecho solicitado ainda não foi salvo ou seus limites são inválidos")
	}
	source, err := os.Open(snapshot.Path)
	if err != nil {
		return fmt.Errorf("abrir a trilha de áudio: %w", err)
	}
	defer func() { err = errors.Join(err, source.Close()) }()
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("criar o trecho de áudio: %w", err)
	}
	writer, err := newWaveWriter(file, snapshot.SampleRate)
	if err != nil {
		return errors.Join(err, file.Close(), os.Remove(destination))
	}
	defer func() {
		err = errors.Join(err, writer.close())
		if err != nil {
			err = errors.Join(err, os.Remove(destination))
		}
	}()
	reader := io.NewSectionReader(source, waveHeaderBytes+int64(start*2), int64((end-start)*2))
	buffer := make([]byte, maxPCMBlockBytes)
	remaining := (end - start) * 2
	for remaining > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		size := min(uint64(len(buffer)), remaining)
		if _, err := io.ReadFull(reader, buffer[:int(size)]); err != nil {
			return fmt.Errorf("ler as amostras do trecho: %w", err)
		}
		if err := writer.writePCM(buffer[:int(size)]); err != nil {
			return fmt.Errorf("salvar o trecho: %w", err)
		}
		remaining -= size
	}
	return nil
}
