// Package recorder records mono, little-endian signed 16-bit PCM to a WAV file.
// Pausing discards incoming samples; it never writes silence in their place.
package recorder

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"time"
)

const (
	preferredSampleRate = 16000
	fallbackSampleRate  = 48000
	maxPCMBlockBytes    = 64 * 1024
	pendingPCMBlocks    = 64
)

var (
	ErrAlreadyRecording    = errors.New("já existe uma gravação em andamento")
	ErrNotRecording        = errors.New("nenhuma gravação foi iniciada")
	ErrNoAudio             = errors.New("nenhum áudio foi gravado")
	ErrUnsupportedFormat   = errors.New("formato de captura não suportado")
	ErrUnsupportedPlatform = errors.New("a captura de microfone requer Windows")
	ErrRIFFLimit           = errors.New("a gravação atingiu o limite do formato WAV RIFF; o áudio já salvo foi preservado")
)

// Device identifies a WinMM input device. Use ID -1 for the system default.
type Device struct {
	ID         int
	Name       string
	EndpointID string
}

// Snapshot is a consistent copy of the recorder's current state. Duration is in
// seconds and excludes paused samples; Level is a peak amplitude between 0 and 1.
// After shutdown, Duration describes the PCM actually saved in the file.
type Snapshot struct {
	Path           string
	Duration       float64
	Active         bool
	Paused         bool
	Level          float64
	Error          string
	Warning        string
	SampleRate     int
	WrittenSamples uint64
	StartedAt      time.Time
}

// Input supplies mono, little-endian signed 16-bit PCM.
//
// Open prepares the requested format without starting capture. It may return
// ErrUnsupportedFormat to request the 48 kHz fallback. Start may call receive
// synchronously or concurrently and must not retain its PCM after receive returns.
// A capture failure is delivered through receive's error argument.
//
// Close must be safe after a failed Open or Start, unblock capture, flush any
// final partial block, and wait until all receive calls have finished. Close
// must be idempotent. An input can be opened again after a successful Close.
type Input interface {
	Open(ctx context.Context, device, sampleRate int) error
	Start(ctx context.Context, receive func(pcm []byte, err error)) error
	Close() error
}

// Recorder owns one input and its file writer. Its methods are safe to call
// concurrently. Start and Stop serialize lifecycle changes; Snapshot, Pause,
// and Resume remain available while the file is being finalized.
type Recorder struct {
	control sync.Mutex
	mu      sync.Mutex

	device     int
	directory  string
	input      Input
	createFile func(directory string) (recordingFile, string, error)

	state      Snapshot
	current    *recording
	startupErr error
	gain       float64
}

type recording struct {
	input      Input
	writer     *waveWriter
	sampleRate int
	samples    uint64
	accepting  bool
	discardPCM bool
	err        error

	queue      chan []byte
	stop       chan struct{}
	stopOnce   sync.Once
	writerDone chan struct{}
	done       chan struct{}
}

// New records from the selected native microphone. Device -1 selects the
// system default. An empty directory uses the application's recording folder.
func New(device int, directory string) *Recorder {
	return NewWithInput(device, directory, newNativeInput())
}

// NewWithInput permits synthetic capture or another implementation of Input.
// The caller transfers ownership of input to the recorder. A nil input,
// including a typed-nil pointer, makes Start return an error.
func NewWithInput(device int, directory string, input Input) *Recorder {
	if directory == "" {
		directory = defaultDirectory()
	}
	if input != nil {
		value := reflect.ValueOf(input)
		if value.Kind() == reflect.Pointer && value.IsNil() {
			input = nil
		}
	}
	return &Recorder{
		device:     device,
		directory:  directory,
		input:      input,
		createFile: createRecordingFile,
		gain:       1,
	}
}

// Start opens the microphone and returns the unique recording path. The
// context controls the whole recording: cancellation shuts down capture and
// finalizes the file. A failure may return a nonempty path to preserved audio.
func (r *Recorder) Start(ctx context.Context) (string, error) {
	r.control.Lock()
	defer r.control.Unlock()

	r.mu.Lock()
	if r.current != nil {
		select {
		case <-r.current.done:
		default:
			r.mu.Unlock()
			return "", ErrAlreadyRecording
		}
	}
	r.current = nil
	r.startupErr = nil
	r.state = Snapshot{}
	r.mu.Unlock()

	if ctx == nil {
		return r.startFailure("", errors.New("o contexto de gravação não pode ser nil"))
	}
	if err := ctx.Err(); err != nil {
		return r.startFailure("", err)
	}
	if r.input == nil {
		return r.startFailure("", errors.New("nenhuma entrada de áudio foi fornecida"))
	}
	if r.device < -1 {
		return r.startFailure("", fmt.Errorf("identificador de microfone inválido: %d", r.device))
	}
	if err := os.MkdirAll(r.directory, 0o700); err != nil {
		return r.startFailure("", fmt.Errorf("não foi possível criar a pasta de gravações: %w", err))
	}

	sampleRate, warning, err := r.openInput(ctx)
	if err != nil {
		return r.startFailure("", err)
	}
	file, path, err := r.createFile(r.directory)
	if err != nil {
		if file != nil {
			err = errors.Join(err, file.Close())
		}
		err = errors.Join(err, r.input.Close())
		return r.startFailure(path, fmt.Errorf("não foi possível criar a gravação: %w", err))
	}
	writer, err := newWaveWriter(file, sampleRate)
	if err != nil {
		err = errors.Join(err, file.Close(), r.input.Close())
		return r.startFailure(path, fmt.Errorf("não foi possível iniciar o arquivo WAV: %w", err))
	}

	s := &recording{
		input:      r.input,
		writer:     writer,
		sampleRate: sampleRate,
		accepting:  true,
		queue:      make(chan []byte, pendingPCMBlocks),
		stop:       make(chan struct{}),
		writerDone: make(chan struct{}),
		done:       make(chan struct{}),
	}
	r.mu.Lock()
	r.current = s
	r.state = Snapshot{Path: path, Active: true, Warning: warning, SampleRate: sampleRate, StartedAt: time.Now()}
	r.mu.Unlock()

	go r.writeAudio(s)
	if err := s.input.Start(ctx, func(pcm []byte, err error) {
		r.capture(s, pcm, err)
	}); err != nil {
		r.fail(s, fmt.Errorf("não foi possível iniciar o microfone: %w", err))
	}
	if source, ok := s.input.(interface{ StartedAt() time.Time }); ok {
		started := source.StartedAt()
		r.mu.Lock()
		r.state.StartedAt = started
		r.mu.Unlock()
	}
	if err := ctx.Err(); err != nil {
		r.fail(s, err)
	}
	go r.finish(ctx, s)

	r.mu.Lock()
	startErr := s.err
	r.mu.Unlock()
	if startErr != nil {
		<-s.done
		return path, withPath(path, s.err)
	}
	return path, nil
}

func (r *Recorder) openInput(ctx context.Context) (int, string, error) {
	err := r.input.Open(ctx, r.device, preferredSampleRate)
	if err == nil {
		return preferredSampleRate, "", nil
	}
	closeErr := r.input.Close()
	if !errors.Is(err, ErrUnsupportedFormat) || closeErr != nil {
		return 0, "", fmt.Errorf("não foi possível abrir o microfone: %w", errors.Join(err, closeErr))
	}
	if err := ctx.Err(); err != nil {
		return 0, "", err
	}
	if err := r.input.Open(ctx, r.device, fallbackSampleRate); err != nil {
		return 0, "", fmt.Errorf("não foi possível abrir o microfone a 16000 ou 48000 Hz: %w",
			errors.Join(err, r.input.Close()))
	}
	return fallbackSampleRate, "O microfone usa 48000 Hz porque não aceita 16000 Hz.", nil
}

func (r *Recorder) startFailure(path string, err error) (string, error) {
	r.mu.Lock()
	r.startupErr = err
	r.state.Path = path
	r.state.Error = withPath(path, err).Error()
	r.mu.Unlock()
	return path, withPath(path, err)
}

func (r *Recorder) capture(s *recording, pcm []byte, captureErr error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.current != s || !s.accepting || s.discardPCM {
		return
	}
	if len(pcm) != 0 && !r.state.Paused {
		switch {
		case len(pcm)%2 != 0:
			s.discardPCM = true
			r.failLocked(s, errors.New("o microfone entregou um bloco PCM incompleto"))
		case len(pcm) > maxPCMBlockBytes:
			s.discardPCM = true
			r.failLocked(s, errors.New("o microfone entregou um bloco PCM maior que o limite de captura"))
		default:
			payload := append([]byte(nil), pcm...)
			scalePCM(payload, r.gain)
			select {
			case s.queue <- payload:
				s.samples += uint64(len(payload) / 2)
				r.state.Duration = float64(s.samples) / float64(s.sampleRate)
				if r.state.Active {
					r.state.Level = pcmLevel(payload)
				}
			default:
				s.discardPCM = true
				r.failLocked(s, errors.New("o disco não acompanhou a gravação; o áudio já capturado foi preservado"))
			}
		}
	}
	if captureErr != nil {
		r.failLocked(s, fmt.Errorf("a captura do microfone falhou: %w", captureErr))
	}
}

func pcmLevel(pcm []byte) float64 {
	peak := 0
	for offset := 0; offset+1 < len(pcm); offset += 2 {
		value := int(int16(binary.LittleEndian.Uint16(pcm[offset:])))
		if value < 0 {
			value = -value
		}
		if value > peak {
			peak = value
		}
	}
	return float64(peak) / 32768
}

func (r *Recorder) fail(s *recording, err error) {
	if err == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failLocked(s, err)
}

func (r *Recorder) failLocked(s *recording, err error) {
	if r.current != s {
		return
	}
	if errors.Is(s.err, err) {
		return
	}
	s.err = errors.Join(s.err, err)
	r.state.Error = withPath(r.state.Path, s.err).Error()
	r.state.Active = false
	r.state.Level = 0
	s.stopOnce.Do(func() { close(s.stop) })
}

func (r *Recorder) rejectPCM(s *recording, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.current != s {
		return
	}
	s.discardPCM = true
	r.failLocked(s, err)
}

func (r *Recorder) writeAudio(s *recording) {
	defer close(s.writerDone)
	var writeErr error
	for pcm := range s.queue {
		if writeErr != nil {
			continue
		}
		if err := s.writer.writePCM(pcm); err != nil {
			writeErr = err
			r.rejectPCM(s, fmt.Errorf("não foi possível salvar a gravação: %w", err))
		}
		r.mu.Lock()
		r.state.WrittenSamples = s.writer.dataBytes / 2
		r.mu.Unlock()
	}
	if err := s.writer.close(); err != nil {
		r.fail(s, fmt.Errorf("não foi possível finalizar a gravação: %w", err))
	}
}

func (r *Recorder) finish(ctx context.Context, s *recording) {
	select {
	case <-ctx.Done():
		r.fail(s, ctx.Err())
	case <-s.stop:
	}
	r.mu.Lock()
	r.state.Active = false
	r.state.Level = 0
	r.mu.Unlock()

	// Close delivers a final partial block before callback admission is closed.
	// The paused flag must remain intact until that block has been considered.
	if err := s.input.Close(); err != nil {
		r.fail(s, fmt.Errorf("não foi possível encerrar o microfone: %w", err))
	}
	r.mu.Lock()
	s.accepting = false
	close(s.queue)
	r.mu.Unlock()
	<-s.writerDone

	r.mu.Lock()
	r.state.Duration = float64(s.writer.dataBytes/2) / float64(s.sampleRate)
	r.state.Active = false
	r.state.Paused = false
	r.state.Level = 0
	if s.writer.dataBytes == 0 && s.err == nil {
		r.failLocked(s, ErrNoAudio)
	}
	close(s.done)
	r.mu.Unlock()
}

// Pause discards incoming PCM until Resume is called.
func (r *Recorder) Pause() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state.Active {
		r.state.Paused = true
		r.state.Level = 0
	}
}

// Resume accepts incoming PCM again.
func (r *Recorder) Resume() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state.Active {
		r.state.Paused = false
	}
}

// Stop releases the input, drains queued audio, and finalizes the WAV header.
// It returns the preserved path even on error and is idempotent after Start.
// Repeated calls retry input cleanup if a driver could not release resources.
func (r *Recorder) Stop() (string, error) {
	r.control.Lock()
	defer r.control.Unlock()

	r.mu.Lock()
	s := r.current
	path := r.state.Path
	if s != nil {
		r.state.Active = false
		r.state.Level = 0
		s.stopOnce.Do(func() { close(s.stop) })
	}
	r.mu.Unlock()
	if s != nil {
		<-s.done
	}
	// Close is idempotent. Retrying also covers an Open failure that happened
	// before a recording session (and its shutdown goroutine) could be created.
	if r.input != nil {
		if closeErr := r.input.Close(); closeErr != nil {
			r.mu.Lock()
			if s != nil {
				if !errors.Is(s.err, closeErr) {
					r.failLocked(s, fmt.Errorf("não foi possível encerrar o microfone: %w", closeErr))
				}
			} else if !errors.Is(r.startupErr, closeErr) {
				r.startupErr = errors.Join(r.startupErr,
					fmt.Errorf("não foi possível encerrar o microfone: %w", closeErr))
				r.state.Error = withPath(path, r.startupErr).Error()
			}
			r.mu.Unlock()
		}
	}
	r.mu.Lock()
	err := r.startupErr
	if s != nil {
		err = s.err
	} else if err == nil {
		err = ErrNotRecording
	}
	r.mu.Unlock()
	return path, withPath(path, err)
}

// Snapshot returns state without exposing mutable recorder internals.
func (r *Recorder) Snapshot() Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state
}

func withPath(path string, err error) error {
	if err == nil || path == "" {
		return err
	}
	return fmt.Errorf("%w\nArquivo: %s", err, path)
}

func defaultDirectory() string {
	if root := os.Getenv("AI_TALKER_DATA_DIR"); root != "" {
		return filepath.Join(root, "gravacoes")
	}
	if root := os.Getenv("LOCALAPPDATA"); root != "" {
		return filepath.Join(root, "AI-Talker", "gravacoes")
	}
	root, err := os.UserConfigDir()
	if err != nil {
		root = os.TempDir()
	}
	return filepath.Join(root, "AI-Talker", "gravacoes")
}

func createRecordingFile(directory string) (recordingFile, string, error) {
	return createSourceRecordingFile(directory, "")
}

func createSourceRecordingFile(directory, source string) (recordingFile, string, error) {
	stem := time.Now().Format("20060102-150405")
	if source != "" {
		stem += "-" + source
	}
	for collision := 1; collision <= 10000; collision++ {
		name := stem
		if collision > 1 {
			name += fmt.Sprintf("-%d", collision)
		}
		path := filepath.Join(directory, name+".wav")
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return nil, "", err
		}
		return file, path, nil
	}
	return nil, "", errors.New("não foi possível reservar um nome para a gravação")
}
