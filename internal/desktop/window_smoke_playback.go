//go:build windows && windowsmoke

package desktop

import (
	"context"
	"sync"
	"time"

	"ai-talker/internal/playback"
	"ai-talker/internal/recorder"
)

// This player follows real elapsed time but emits no sound. Native device
// playback is verified separately with a silent WAV in internal/playback.
type windowSmokePlayer struct {
	mu     sync.Mutex
	state  playback.Snapshot
	volume int
	speed  float64
}

func (p *windowSmokePlayer) Run(ctx context.Context, source string) error {
	sourceAudio, err := recorder.Inspect(source)
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.state = playback.Snapshot{Ready: true, Active: true, Duration: sourceAudio.Duration}
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.state.Active = false
		p.mu.Unlock()
	}()
	last := time.Now()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case now := <-ticker.C:
			p.mu.Lock()
			if !p.state.Paused {
				p.state.Position = min(p.state.Duration, p.state.Position+now.Sub(last).Seconds()*p.speed)
			}
			done := p.state.Position >= p.state.Duration
			p.mu.Unlock()
			last = now
			if done {
				return nil
			}
		}
	}
}

func (p *windowSmokePlayer) Snapshot() playback.Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
}

func (p *windowSmokePlayer) SetPaused(paused bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.state.Paused = paused
}

func (p *windowSmokePlayer) SetVolume(volume int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.volume = volume
}

func (p *windowSmokePlayer) SetSpeed(speed float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.speed = speed
}

func (p *windowSmokePlayer) Seek(seconds float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.state.Position = max(0, min(p.state.Duration, seconds))
}

func (p *windowSmokePlayer) outputVolume() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.volume
}

func (p *windowSmokePlayer) playbackSpeed() float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.speed
}
