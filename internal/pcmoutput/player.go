// Package pcmoutput plays growing mono PCM responses on a selected output.
package pcmoutput

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"runtime"
	"slices"
	"sync"
	"time"
)

type Snapshot struct {
	Position, Duration float64
	Paused, Active     bool
}

type device interface {
	Write([]byte) (int, error)
	Reset() error
	Pause(bool) error
	Close() error
}

// Player retains the original PCM for seeking; attenuation affects only the
// device copy. Run owns every native call on one thread and exits on context.
type Player struct {
	mu         sync.Mutex
	pcm        []byte
	rate       int
	endpoint   string
	position   int
	pending    int
	volume     int
	paused     bool
	active     bool
	finished   bool
	generation uint64
	wake       chan struct{}
	open       func(string, int) (device, error)
}

func New(endpoint string, sampleRate, volume int) *Player {
	return &Player{
		endpoint: endpoint, rate: sampleRate, volume: max(0, min(200, volume)),
		wake: make(chan struct{}, 1), open: openDevice,
	}
}

func (p *Player) signal() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

func (p *Player) Append(pcm []byte) error {
	if len(pcm)%2 != 0 {
		return errors.New("a resposta contém uma amostra PCM incompleta")
	}
	p.mu.Lock()
	if p.finished {
		p.mu.Unlock()
		return errors.New("o áudio desta resposta já foi concluído")
	}
	// Guard memory even if a remote peer never ends a response.
	if len(p.pcm)+len(pcm) > p.rate*2*60*30 {
		p.mu.Unlock()
		return errors.New("a resposta excedeu o limite de 30 minutos de áudio")
	}
	p.pcm = append(p.pcm, pcm...)
	p.mu.Unlock()
	p.signal()
	return nil
}

func (p *Player) SetPaused(paused bool) {
	p.mu.Lock()
	p.paused = paused
	p.mu.Unlock()
	p.signal()
}

// Finish closes the growing input. Run drains native queued frames before
// releasing the output; manual replay uses another Player with preserved PCM.
func (p *Player) Finish() {
	p.mu.Lock()
	p.finished = true
	p.mu.Unlock()
	p.signal()
}

func (p *Player) SetVolume(volume int) {
	p.mu.Lock()
	p.volume = max(0, min(200, volume))
	p.mu.Unlock()
	p.signal()
}

func (p *Player) Seek(seconds float64) {
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return
	}
	p.mu.Lock()
	target := max(0, min(float64(len(p.pcm)/2), seconds*float64(p.rate)))
	p.position = int(target) * 2
	p.pending = 0
	p.generation++
	p.mu.Unlock()
	p.signal()
}

func (p *Player) Snapshot() Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.rate <= 0 {
		return Snapshot{Paused: p.paused, Active: p.active}
	}
	return Snapshot{
		Position: float64(max(0, p.position-p.pending)) / float64(p.rate*2),
		Duration: float64(len(p.pcm)) / float64(p.rate*2),
		Paused:   p.paused, Active: p.active,
	}
}

func (p *Player) Run(ctx context.Context) (err error) {
	if ctx == nil || p.rate < 8000 || p.rate > 48000 {
		return errors.New("formato ou contexto de reprodução inválido")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	if p.active {
		p.mu.Unlock()
		return errors.New("a resposta já está sendo reproduzida")
	}
	p.active = true
	p.mu.Unlock()
	defer func() { p.mu.Lock(); p.active = false; p.mu.Unlock() }()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	output, err := p.open(p.endpoint, p.rate)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, output.Close()) }()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	var generation uint64
	var wasPaused bool
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		p.mu.Lock()
		position, nextGeneration, paused, finished := p.position, p.generation, p.paused, p.finished
		size := min(len(p.pcm)-position, p.rate/50*2)
		payload := slices.Clone(p.pcm[position : position+size])
		volume := p.volume
		p.mu.Unlock()
		if generation != nextGeneration {
			if err := output.Reset(); err != nil {
				return err
			}
			generation = nextGeneration
		}
		if paused != wasPaused {
			if err := output.Pause(paused); err != nil {
				return err
			}
			wasPaused = paused
		}
		pending := 0
		if output, ok := output.(interface{ Pending() (int, error) }); ok {
			var pendingErr error
			pending, pendingErr = output.Pending()
			if pendingErr != nil {
				return pendingErr
			}
			if pending < 0 {
				return errors.New("o dispositivo informou uma fila de áudio inválida")
			}
		}
		p.mu.Lock()
		if p.generation == generation {
			p.pending = pending
		}
		p.mu.Unlock()
		if finished && len(payload) == 0 && pending == 0 {
			return nil
		}
		if !paused && len(payload) > 0 {
			applyVolume(payload, volume)
			written, writeErr := output.Write(payload)
			if writeErr != nil {
				return writeErr
			}
			if written < 0 || written > len(payload) || written%2 != 0 {
				return errors.New("o dispositivo confirmou um tamanho PCM inválido")
			}
			p.mu.Lock()
			if p.generation == generation && p.position == position {
				p.position += written
				if _, ok := output.(interface{ Pending() (int, error) }); ok {
					p.pending += written
				}
			}
			p.mu.Unlock()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-p.wake:
		case <-ticker.C:
		}
	}
}

func applyVolume(pcm []byte, volume int) {
	if volume == 100 {
		return
	}
	gain := float64(volume) / 100
	for index := 0; index < len(pcm); index += 2 {
		value := float64(int16(binary.LittleEndian.Uint16(pcm[index:]))) / 32768
		value *= gain
		if gain > 1 {
			// Leave quiet speech linear; compress only the upper range.
			sign := math.Copysign(1, value)
			magnitude := math.Abs(value)
			if magnitude > 0.8 {
				value = sign * (0.8 + 0.2*math.Tanh((magnitude-0.8)/0.2))
			}
		}
		sample := int16(math.Round(max(-1, min(32767.0/32768, value)) * 32768))
		binary.LittleEndian.PutUint16(pcm[index:], uint16(sample))
	}
}
