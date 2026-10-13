// Package playback plays the loaded recording without changing its sources.
package playback

import (
	"context"
	"errors"
	"fmt"
	"math"
	"runtime"
	"sync"
	"time"
)

// Snapshot is safe to read from the UI while Run owns the audio device.
type Snapshot struct {
	Ready, Active, Paused bool
	Position, Duration    float64
}

type output interface {
	Play() error
	Pause() error
	SetVolume(int) error
	Status() (position, duration float64, playing bool, err error)
	Close() error
}

// Player owns one playback attempt. Run must execute outside the UI thread.
// SetPaused only signals its owner; native calls remain on the Run thread.
type Player struct {
	directory string
	mu        sync.Mutex
	state     Snapshot
	running   bool
	volume    int
	speed     float64
	seek      *float64
	wake      chan struct{}
	prepare   func(context.Context, string, string, renderOptions) (preparedAudio, error)
	open      func(string) (output, error)
}

func New(directory string) *Player {
	return &Player{
		directory: directory, wake: make(chan struct{}, 1), volume: 100, speed: 1,
		prepare: prepareAudioWithOptions, open: openOutput,
	}
}

func (p *Player) Snapshot() Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
}

func (p *Player) SetPaused(paused bool) {
	p.mu.Lock()
	if !p.state.Active || !p.state.Ready {
		p.mu.Unlock()
		return
	}
	p.state.Paused = paused
	p.mu.Unlock()
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// SetVolume changes this player's private playback copy. Concurrent players
// therefore retain independent levels, including while either is paused.
func (p *Player) SetVolume(volume int) {
	p.mu.Lock()
	requested := max(0, min(200, volume))
	if p.volume == requested {
		p.mu.Unlock()
		return
	}
	p.volume = requested
	p.mu.Unlock()
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// SetSpeed selects a pitch-preserving playback speed. Run rebuilds the private
// playback copy and resumes at the same position in the original recording.
func (p *Player) SetSpeed(speed float64) {
	if !validPlaybackSpeed(speed) {
		return
	}
	p.mu.Lock()
	if p.speed == speed {
		p.mu.Unlock()
		return
	}
	p.speed = speed
	p.mu.Unlock()
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

func validPlaybackSpeed(speed float64) bool {
	switch speed {
	case 0.5, 0.75, 1, 1.25, 1.5, 2, 2.25, 2.5:
		return true
	default:
		return false
	}
}

func renderGain(volume int) float64 {
	return float64(volume) / 100
}

// MCI uses the process's default CoreAudio session. Every player keeps that
// shared session neutral and applies its own attenuation to its private PCM.
func requestedOutputVolume(int) int { return 100 }

func (p *Player) requestedSettings() (int, float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.volume, p.speed
}

// Seek coalesces UI requests. Native positioning stays on Run's owning thread.
func (p *Player) Seek(seconds float64) {
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return
	}
	p.mu.Lock()
	if !p.state.Ready || !p.state.Active {
		p.mu.Unlock()
		return
	}
	position := max(0, min(p.state.Duration, seconds))
	p.seek = &position
	p.mu.Unlock()
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

func (p *Player) takeSeek() *float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	position := p.seek
	p.seek = nil
	return position
}

// Run prepares a private WAV, plays it, and releases the device and temporary
// file before returning. Cancellation also stops paused playback.
func (p *Player) Run(ctx context.Context, source string) (err error) {
	if ctx == nil {
		return errors.New("o contexto de reprodução está ausente")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	if p.running {
		p.mu.Unlock()
		return errors.New("este áudio já está sendo reproduzido")
	}
	p.running, p.state = true, Snapshot{}
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.running, p.state.Active = false, false
		p.mu.Unlock()
	}()

	// MCI device commands and their teardown stay on one background thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var prepared preparedAudio
	var device output
	var activeSpeed, activeGain float64
	var activeOutputVolume int
	var logicalDuration float64
	paused := false
	closeCurrent := func() error {
		var closeErr error
		if device != nil {
			closeErr = device.Close()
			device = nil
		}
		removeErr := prepared.remove()
		prepared = preparedAudio{}
		return errors.Join(closeErr, removeErr)
	}
	defer func() { err = errors.Join(err, closeCurrent()) }()
	openCurrent := func(speed float64, volume int, position float64) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		gain := renderGain(volume)
		prepared, err = p.prepare(ctx, source, p.directory, renderOptions{Speed: speed, Gain: gain})
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		device, err = p.open(prepared.path)
		if err != nil {
			return err
		}
		_, duration, _, err := device.Status()
		if err != nil {
			return err
		}
		if duration <= 0 {
			return errors.New("o arquivo não contém áudio para reproduzir")
		}
		activeOutputVolume = requestedOutputVolume(volume)
		if err := device.SetVolume(activeOutputVolume); err != nil {
			return err
		}
		logicalDuration = duration * speed
		position = max(0, min(position, logicalDuration))
		if position > 0 {
			seeker, ok := device.(interface{ Seek(float64) error })
			if !ok {
				return errors.New("a saída de áudio não permite retomar a posição")
			}
			if err := seeker.Seek(position / speed); err != nil {
				return err
			}
		}
		requestedPause := p.Snapshot().Paused
		if !requestedPause {
			if err := device.Play(); err != nil {
				return err
			}
		}
		paused = requestedPause
		activeSpeed, activeGain = speed, gain
		p.mu.Lock()
		// Paused is the latest UI request, including changes made while
		// preparing this copy. Only the owning loop updates device state.
		p.state.Ready, p.state.Active = true, true
		p.state.Position, p.state.Duration = position, logicalDuration
		p.mu.Unlock()
		return nil
	}
	initialVolume, initialSpeed := p.requestedSettings()
	if err := openCurrent(initialSpeed, initialVolume, 0); err != nil {
		return err
	}

	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-p.wake:
		case <-ticker.C:
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		requestedVolume, requestedSpeed := p.requestedSettings()
		requested := p.Snapshot().Paused
		position, _, _, err := device.Status()
		if err != nil {
			return err
		}
		logicalPosition := min(logicalDuration, position*activeSpeed)
		nextGain := renderGain(requestedVolume)
		if requestedSpeed != activeSpeed || nextGain != activeGain {
			if err := closeCurrent(); err != nil {
				return err
			}
			if err := openCurrent(requestedSpeed, requestedVolume, logicalPosition); err != nil {
				return err
			}
			continue
		}
		if nextVolume := requestedOutputVolume(requestedVolume); nextVolume != activeOutputVolume {
			if err := device.SetVolume(nextVolume); err != nil {
				return err
			}
			activeOutputVolume = nextVolume
		}
		if position := p.takeSeek(); position != nil {
			seeker, ok := device.(interface{ Seek(float64) error })
			if !ok {
				return errors.New("a saída de áudio não permite mudar a posição")
			}
			if err := seeker.Seek(*position / activeSpeed); err != nil {
				return err
			}
			if !requested {
				if err := device.Play(); err != nil {
					return err
				}
			}
			// Seeking stops MCI. Keep that stopped position as a logical
			// pause instead of issuing Pause against an already stopped device.
			paused = requested
		}
		if requested != paused {
			action := device.Play
			if requested {
				action = device.Pause
			}
			if err := action(); err != nil {
				return err
			}
			paused = requested
		}
		position, duration, playing, err := device.Status()
		if err != nil {
			return err
		}
		logicalPosition = min(duration*activeSpeed, position*activeSpeed)
		logicalDuration = duration * activeSpeed
		p.mu.Lock()
		p.state.Position, p.state.Duration = logicalPosition, logicalDuration
		p.mu.Unlock()
		if !paused && !playing {
			if logicalPosition+0.1 < logicalDuration {
				return fmt.Errorf("a reprodução foi interrompida em %.1f de %.1f segundos", logicalPosition, logicalDuration)
			}
			return nil
		}
	}
}
