package soundboard

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"slices"
	"sync"
	"time"

	"ai-talker/internal/audio"
	"ai-talker/internal/platform"
)

const (
	outputRate     = 48000
	outputChannels = 2
	maxPrepared    = 128 * 1024 * 1024
)

type renderDevice interface {
	Wait() error
	Write(func([]int16)) error
	Close() error
	Latency() time.Duration
}

// Timing measures the click request to first PCM submission. DeviceLatency is
// reported by WASAPI; acoustic latency requires a separate physical measurement.
type Timing struct {
	ID            string
	Requested     time.Time
	Submitted     time.Time
	DeviceLatency time.Duration
}

// Engine keeps one shared output open and all effects decoded in memory.
// Repeated clicks replace one pending/current effect; they never enter a queue.
// Its owning worker terminates and releases the device when its context ends.
type Engine struct {
	mu       sync.Mutex
	prepared map[string][]int16
	used     int
	current  []int16
	offset   int
	volume   int
	timing   Timing
	latency  time.Duration
	cancel   context.CancelFunc
	done     chan struct{}
	ready    bool
	failure  error
	open     func() (renderDevice, error)
	decode   func(context.Context, string) ([]int16, error)
}

func NewEngine() *Engine {
	return &Engine{
		prepared: make(map[string][]int16), volume: 100,
		open: openRenderDevice, decode: decodeEffect,
	}
}

// Start completes only after the persistent device is ready for the first click.
func (e *Engine) Start(ctx context.Context) error {
	e.mu.Lock()
	if e.done != nil {
		e.mu.Unlock()
		return errors.New("o motor do painel de sons já foi iniciado")
	}
	workerCtx, cancel := context.WithCancel(ctx)
	e.cancel, e.done = cancel, make(chan struct{})
	done := e.done
	e.mu.Unlock()
	result := make(chan error, 1)
	go func() {
		defer close(done)
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		device, err := e.open()
		if err != nil {
			result <- err
			return
		}
		defer device.Close()
		e.mu.Lock()
		e.ready, e.latency = true, device.Latency()
		e.mu.Unlock()
		result <- nil
		defer func() {
			e.mu.Lock()
			e.ready = false
			e.mu.Unlock()
		}()
		for {
			if workerCtx.Err() != nil {
				return
			}
			err := device.Wait()
			if err == nil {
				err = device.Write(e.fill)
			}
			if err != nil {
				e.mu.Lock()
				e.failure = err
				e.mu.Unlock()
				return
			}
		}
	}()
	select {
	case err := <-result:
		if err != nil {
			cancel()
		}
		return err
	case <-ctx.Done():
		cancel()
		return ctx.Err()
	}
}

func (e *Engine) Close() {
	e.mu.Lock()
	cancel, done := e.cancel, e.done
	e.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

// Prepare runs only at application startup or after importing a sound.
func (e *Engine) Prepare(ctx context.Context, id, path string) error {
	e.mu.Lock()
	_, exists := e.prepared[id]
	e.mu.Unlock()
	if exists {
		return nil
	}
	pcm, err := e.decode(ctx, path)
	if err != nil {
		return err
	}
	if len(pcm) == 0 || len(pcm)%outputChannels != 0 {
		return errors.New("o efeito não contém áudio estéreo válido")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, exists := e.prepared[id]; exists {
		return nil
	}
	if e.used+len(pcm)*2 > maxPrepared {
		return errors.New("os efeitos preparados excedem 128 MB; remova sons longos do painel")
	}
	e.prepared[id] = pcm
	e.used += len(pcm) * 2
	return nil
}

func (e *Engine) Remove(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.used -= len(e.prepared[id]) * 2
	delete(e.prepared, id)
	if e.timing.ID == id {
		e.current, e.offset = nil, 0
	}
}

func (e *Engine) Ready(id string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.ready && e.failure == nil && len(e.prepared[id]) > 0
}

func (e *Engine) Play(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.failure != nil {
		return e.failure
	}
	if !e.ready || len(e.prepared[id]) == 0 {
		return errors.New("o som ainda está sendo preparado")
	}
	e.current, e.offset = e.prepared[id], 0
	e.timing = Timing{ID: id, Requested: time.Now(), DeviceLatency: e.latency}
	return nil
}

func (e *Engine) Stop() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.current, e.offset = nil, 0
}

func (e *Engine) SetVolume(volume int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.volume = max(0, min(100, volume))
}

func (e *Engine) Active() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.ready && e.failure == nil && len(e.current) > 0 && e.offset < len(e.current)
}

func (e *Engine) Err() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.failure
}

func (e *Engine) Timing() Timing {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.timing
}

func (e *Engine) fill(target []int16) {
	e.mu.Lock()
	defer e.mu.Unlock()
	clear(target)
	if len(e.current) == 0 {
		return
	}
	count := min(len(target), len(e.current)-e.offset)
	for index, sample := range e.current[e.offset : e.offset+count] {
		target[index] = int16(int(sample) * e.volume / 100)
		if sample != 0 && e.timing.Submitted.IsZero() {
			e.timing.Submitted = time.Now()
		}
	}
	e.offset += count
	if e.offset == len(e.current) {
		e.current, e.offset = nil, 0
	}
}

func decodeEffect(ctx context.Context, path string) ([]int16, error) {
	processor, err := audio.New()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, processor.FFmpeg,
		"-v", "error", "-nostdin", "-protocol_whitelist", "file,pipe",
		"-i", path, "-map", "0:a:0", "-vn", "-ac", "2", "-ar", "48000",
		"-f", "s16le", "pipe:1",
	)
	platform.Hide(command)
	command.WaitDelay = 3 * time.Second
	var diagnostic bytes.Buffer
	command.Stderr = &diagnostic
	output, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := command.Start(); err != nil {
		return nil, err
	}
	bytes, readErr := io.ReadAll(io.LimitReader(output, maxPrepared+1))
	if len(bytes) > maxPrepared || readErr != nil {
		cancel()
	}
	waitErr := command.Wait()
	if readErr != nil {
		return nil, fmt.Errorf("ler o efeito preparado: %w", readErr)
	}
	if len(bytes) > maxPrepared {
		return nil, errors.New("o efeito decodificado excede 128 MB")
	}
	if waitErr != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		message := diagnostic.String()
		if len(message) > 2048 {
			message = message[:2048]
		}
		return nil, fmt.Errorf("preparar o efeito: %s: %w", message, waitErr)
	}
	if len(bytes)%4 != 0 {
		return nil, errors.New("o efeito preparado contém amostras incompletas")
	}
	pcm := make([]int16, len(bytes)/2)
	for index := range pcm {
		pcm[index] = int16(binary.LittleEndian.Uint16(bytes[index*2:]))
	}
	return trimEffectSilence(pcm), nil
}

func trimEffectSilence(pcm []int16) []int16 {
	// Leading file silence also counts as click-to-sound delay. Keep at most
	// 2 ms before the first sample above -60 dB, without touching source files.
	for frame := 0; frame < len(pcm)/outputChannels; frame++ {
		left, right := int(pcm[frame*2]), int(pcm[frame*2+1])
		if left > 32 || left < -32 || right > 32 || right < -32 {
			start := max(0, frame-96) * outputChannels
			if start > 0 {
				// Cache accounting uses the remaining sample length. Compact
				// its storage so a short effect cannot retain a long prefix.
				return slices.Clone(pcm[start:])
			}
			break
		}
	}
	return pcm
}
