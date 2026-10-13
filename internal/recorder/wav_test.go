package recorder

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

type memoryRecordingFile struct {
	data     []byte
	position int64
	closed   bool
	seekErr  error
	syncErr  error
	closeErr error
}

func (f *memoryRecordingFile) Write(data []byte) (int, error) {
	end := f.position + int64(len(data))
	if end > int64(len(f.data)) {
		f.data = append(f.data, make([]byte, int(end)-len(f.data))...)
	}
	copy(f.data[int(f.position):int(end)], data)
	f.position = end
	return len(data), nil
}

func (f *memoryRecordingFile) Seek(offset int64, whence int) (int64, error) {
	if f.seekErr != nil {
		return 0, f.seekErr
	}
	switch whence {
	case io.SeekStart:
		f.position = offset
	case io.SeekCurrent:
		f.position += offset
	case io.SeekEnd:
		f.position = int64(len(f.data)) + offset
	}
	return f.position, nil
}

func (f *memoryRecordingFile) Sync() error { return f.syncErr }

func (f *memoryRecordingFile) Close() error {
	f.closed = true
	return f.closeErr
}

func TestWaveWriterStreamsPCMAndFinalizesLengths(t *testing.T) {
	t.Parallel()
	file := new(memoryRecordingFile)
	writer, err := newWaveWriter(file, 16000)
	if err != nil {
		t.Fatal(err)
	}
	pcm := pcmSamples(-32768, 32767, 0, 123)
	if err := writer.writePCM(pcm[:4]); err != nil {
		t.Fatal(err)
	}
	if err := writer.writePCM(pcm[4:]); err != nil {
		t.Fatal(err)
	}
	if len(file.data) != waveHeaderBytes+len(pcm) || !bytes.Equal(file.data[waveHeaderBytes:], pcm) {
		t.Fatal("PCM was retained rather than streamed to the file")
	}
	if err := writer.close(); err != nil {
		t.Fatal(err)
	}
	if !file.closed || binary.LittleEndian.Uint32(file.data[4:8]) != 36+uint32(len(pcm)) ||
		binary.LittleEndian.Uint32(file.data[40:44]) != uint32(len(pcm)) {
		t.Fatal("final WAV sizes or file lifecycle are incorrect")
	}
	if err := writer.writePCM(pcm); err == nil {
		t.Fatal("closed writer accepted PCM")
	}
}

func TestWaveWriterRIFFLimitFailsBeforeSizeWrap(t *testing.T) {
	t.Parallel()
	file := new(memoryRecordingFile)
	writer, err := newWaveWriter(file, 16000)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a near-full streaming file without allocating a 4 GiB fixture.
	writer.dataBytes = maxRIFFDataBytes - 2
	if err := writer.writePCM(pcmSamples(7)); err != nil {
		t.Fatal(err)
	}
	before := append([]byte(nil), file.data...)
	if err := writer.writePCM(pcmSamples(8)); !errors.Is(err, ErrRIFFLimit) {
		t.Fatalf("overflow = %v, want ErrRIFFLimit", err)
	}
	if !bytes.Equal(file.data, before) || writer.dataBytes != maxRIFFDataBytes {
		t.Fatal("overflow wrote data or silently wrapped its length")
	}
	if err := writer.close(); err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint32(file.data[40:44]) != uint32(maxRIFFDataBytes) ||
		uint64(binary.LittleEndian.Uint32(file.data[4:8])) != 36+maxRIFFDataBytes {
		t.Fatal("RIFF limit produced truncated uint32 lengths")
	}
}

func TestWaveWriterFinalizationErrorsStillCloseFile(t *testing.T) {
	t.Parallel()
	seekFailure := errors.New("seek failure")
	syncFailure := errors.New("sync failure")
	closeFailure := errors.New("close failure")
	for _, tc := range []struct {
		name string
		seek error
		sync error
	}{
		{name: "seek failure", seek: seekFailure},
		{name: "sync failure", sync: syncFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			file := &memoryRecordingFile{seekErr: tc.seek, syncErr: tc.sync, closeErr: closeFailure}
			writer, err := newWaveWriter(file, 16000)
			if err != nil {
				t.Fatal(err)
			}
			if err := writer.writePCM(pcmSamples(10)); err != nil {
				t.Fatal(err)
			}
			err = writer.close()
			if !errors.Is(err, closeFailure) || (tc.seek != nil && !errors.Is(err, tc.seek)) ||
				(tc.sync != nil && !errors.Is(err, tc.sync)) || !file.closed {
				t.Fatalf("finalization = %v, closed=%t", err, file.closed)
			}
			if second := writer.close(); second != err {
				t.Fatal("repeated finalization did not return the original result")
			}
		})
	}
}

func TestWaveWriterRejectsInvalidFormatAndPCM(t *testing.T) {
	t.Parallel()
	if _, err := newWaveWriter(nil, 16000); err == nil {
		t.Fatal("nil file was accepted")
	}
	if _, err := newWaveWriter(new(memoryRecordingFile), 0); err == nil {
		t.Fatal("zero sample rate was accepted")
	}
	file := new(memoryRecordingFile)
	writer, err := newWaveWriter(file, 16000)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.writePCM([]byte{1}); err == nil || len(file.data) != waveHeaderBytes {
		t.Fatal("incomplete s16 PCM was written")
	}
	if err := writer.close(); err != nil {
		t.Fatal(err)
	}
}
