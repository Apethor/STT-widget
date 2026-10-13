package desktop

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// voiceAudioFile keeps a valid header after every received block. A crash or
// interrupted response leaves the received portion available for replay.
type voiceAudioFile struct {
	file *os.File
	path string
	size uint32
	rate int
}

func newVoiceAudioFile(folder string, rate int) (*voiceAudioFile, error) {
	if rate != 24000 && rate != 16000 && rate != 8000 {
		return nil, errors.New("taxa de áudio da resposta não suportada")
	}
	if err := os.MkdirAll(folder, 0o700); err != nil {
		return nil, err
	}
	file, err := os.CreateTemp(folder, "resposta-*.wav")
	if err != nil {
		return nil, err
	}
	writer := &voiceAudioFile{file: file, path: file.Name(), rate: rate}
	if err := writer.write(nil); err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return nil, err
	}
	return writer, nil
}

func voiceWAVHeader(rate int, size uint32) []byte {
	header := make([]byte, 44)
	copy(header, "RIFF")
	binary.LittleEndian.PutUint32(header[4:], 36+size)
	copy(header[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(header[16:], 16)
	binary.LittleEndian.PutUint16(header[20:], 1)
	binary.LittleEndian.PutUint16(header[22:], 1)
	binary.LittleEndian.PutUint32(header[24:], uint32(rate))
	binary.LittleEndian.PutUint32(header[28:], uint32(rate*2))
	binary.LittleEndian.PutUint16(header[32:], 2)
	binary.LittleEndian.PutUint16(header[34:], 16)
	copy(header[36:], "data")
	binary.LittleEndian.PutUint32(header[40:], size)
	return header
}

func (w *voiceAudioFile) write(pcm []byte) error {
	if len(pcm)%2 != 0 || uint64(w.size)+uint64(len(pcm)) > uint64(w.rate)*2*60*30 {
		return errors.New("a resposta contém áudio inválido ou excedeu 30 minutos")
	}
	if count, err := w.file.WriteAt(pcm, int64(44+w.size)); err != nil {
		return err
	} else if count != len(pcm) {
		return io.ErrShortWrite
	}
	w.size += uint32(len(pcm))
	_, err := w.file.WriteAt(voiceWAVHeader(w.rate, w.size), 0)
	return err
}

func readVoiceAudio(path string) ([]byte, int, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, 0, err
	}
	if !info.Mode().IsRegular() || info.Size() > 48000*2*60*30+44 {
		return nil, 0, errors.New("o arquivo da resposta excede o limite de áudio")
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	if len(payload) < 44 || string(payload[:4]) != "RIFF" ||
		string(payload[8:16]) != "WAVEfmt " || string(payload[36:40]) != "data" ||
		binary.LittleEndian.Uint32(payload[16:20]) != 16 ||
		binary.LittleEndian.Uint16(payload[20:22]) != 1 ||
		binary.LittleEndian.Uint16(payload[22:24]) != 1 ||
		binary.LittleEndian.Uint16(payload[32:34]) != 2 ||
		binary.LittleEndian.Uint16(payload[34:36]) != 16 {
		return nil, 0, errors.New("arquivo de resposta WAV inválido")
	}
	size := int(binary.LittleEndian.Uint32(payload[40:44]))
	if size%2 != 0 || size > len(payload)-44 {
		return nil, 0, errors.New("arquivo de resposta incompleto")
	}
	rate := int(binary.LittleEndian.Uint32(payload[24:28]))
	if rate != 24000 && rate != 16000 && rate != 8000 {
		return nil, 0, errors.New("taxa do arquivo de resposta inválida")
	}
	if binary.LittleEndian.Uint32(payload[28:32]) != uint32(rate*2) ||
		uint64(binary.LittleEndian.Uint32(payload[4:8])) < uint64(36+size) {
		return nil, 0, errors.New("cabeçalho do arquivo de resposta inválido")
	}
	return payload[44 : 44+size], rate, nil
}

// relayVoiceCapture reads only PCM already confirmed written by Recorder.
// Network backpressure never delays the recorder's file writer. The 48 kHz
// fallback is downsampled in complete groups. Each block keeps the live cadence;
// a paused microphone sends transport silence without changing the saved WAV.
func relayVoiceCapture(ctx context.Context, r recording, send func(context.Context, []byte) error) error {
	var file *os.File
	defer func() {
		if file != nil {
			_ = file.Close()
		}
	}()
	var samples uint64
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		snapshot := r.Snapshot()
		if snapshot.Path != "" && file == nil {
			var err error
			file, err = os.Open(snapshot.Path)
			if err != nil {
				return err
			}
		}
		rate := snapshot.SampleRate
		if file != nil && rate != 0 {
			if rate != 16000 && rate != 48000 {
				return errors.New("o microfone entregou uma taxa incompatível com a conversa")
			}
			factor := uint64(rate / 16000)
			if snapshot.WrittenSamples < samples {
				return errors.New("a gravação da conversa recuou inesperadamente")
			}
			available := snapshot.WrittenSamples - samples
			count := min(available/factor*factor, uint64(rate/50))
			if !snapshot.Active && count == 0 {
				count = available
			}
			if count > 0 {
				pcm := make([]byte, count*2)
				if _, err := file.ReadAt(pcm, int64(44+samples*2)); err != nil {
					return fmt.Errorf("ler áudio da conversa: %w", err)
				}
				if factor == 3 {
					reduced := make([]byte, (count+2)/3*2)
					for index := 0; index < len(reduced)/2; index++ {
						var total int32
						remaining := min(3, len(pcm)/2-index*3)
						for offset := 0; offset < remaining; offset++ {
							total += int32(int16(binary.LittleEndian.Uint16(pcm[(index*3+offset)*2:])))
						}
						binary.LittleEndian.PutUint16(reduced[index*2:], uint16(int16(total/int32(remaining))))
					}
					pcm = reduced
				}
				if err := send(ctx, pcm); err != nil {
					return err
				}
				samples += count
				ticker.Reset(20 * time.Millisecond)
			} else if snapshot.Active && snapshot.Paused {
				if err := send(ctx, make([]byte, 16000/50*2)); err != nil {
					return err
				}
				ticker.Reset(20 * time.Millisecond)
			}
		}
		if !snapshot.Active && file != nil && samples == snapshot.WrittenSamples {
			if snapshot.Error != "" {
				return errors.New(snapshot.Error)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

type responseFiles struct {
	mu      sync.Mutex
	pathsMu sync.RWMutex
	folder  string
	files   map[string]*voiceAudioFile
	paths   map[string]struct{}
	closed  bool
}

func (f *responseFiles) append(id string, pcm []byte, rate int) (string, error) {
	path, _, err := f.appendBlock(id, pcm, rate)
	return path, err
}

// appendBlock returns the committed cumulative byte count with its path.
// UI callbacks use this offset even when manual playback loads ahead of them.
func (f *responseFiles) appendBlock(id string, pcm []byte, rate int) (string, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return "", 0, errors.New("o armazenamento desta conversa já foi encerrado")
	}
	if f.files == nil {
		f.files = make(map[string]*voiceAudioFile)
	}
	writer := f.files[id]
	if writer == nil {
		var err error
		writer, err = newVoiceAudioFile(f.folder, rate)
		if err != nil {
			return "", 0, err
		}
		f.files[id] = writer
		f.pathsMu.Lock()
		if f.paths == nil {
			f.paths = make(map[string]struct{})
		}
		f.paths[strings.ToLower(filepath.Clean(writer.path))] = struct{}{}
		f.pathsMu.Unlock()
	}
	if writer.rate != rate {
		return writer.path, int(writer.size), errors.New("a taxa de áudio mudou durante a mesma resposta")
	}
	err := writer.write(pcm)
	return writer.path, int(writer.size), err
}

func (f *responseFiles) read(id string) ([]byte, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	writer := f.files[id]
	if writer == nil {
		return nil, 0, os.ErrNotExist
	}
	if f.closed {
		return readVoiceAudio(writer.path)
	}
	pcm := make([]byte, int(writer.size))
	if _, err := writer.file.ReadAt(pcm, 44); err != nil {
		return nil, 0, err
	}
	return pcm, writer.rate, nil
}

func (f *responseFiles) path(id string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if writer := f.files[id]; writer != nil {
		return writer.path
	}
	return ""
}

// containsPath reads only metadata, so cleanup on the UI thread never waits
// for PCM writes, manual file loading, or Sync held under the file mutex.
func (f *responseFiles) containsPath(path string) bool {
	if path == "" {
		return false
	}
	f.pathsMu.RLock()
	defer f.pathsMu.RUnlock()
	_, found := f.paths[strings.ToLower(filepath.Clean(path))]
	return found
}

func (f *responseFiles) close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil
	}
	f.closed = true
	var failures error
	for _, writer := range f.files {
		failures = errors.Join(failures, writer.file.Sync(), writer.file.Close())
	}
	return failures
}

func responseFolder(directory string, now time.Time) string {
	return filepath.Join(recordingFolder(directory), "respostas", now.Format("2006-01-02"))
}
