package transcript

import (
	"context"
	"errors"
	"sync"
)

type concurrencyGate struct {
	mu     sync.Mutex
	limit  int
	active int
	wake   chan struct{}
}

func newConcurrencyGate(limit int) *concurrencyGate {
	return &concurrencyGate{limit: limit, wake: make(chan struct{})}
}

func (g *concurrencyGate) acquire(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		g.mu.Lock()
		if g.active < g.limit {
			g.active++
			g.mu.Unlock()
			return nil
		}
		wake := g.wake
		g.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-wake:
		}
	}
}

func (g *concurrencyGate) release() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.active--
	close(g.wake)
	g.wake = make(chan struct{})
}

func (g *concurrencyGate) set(limit int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.limit = limit
	close(g.wake)
	g.wake = make(chan struct{})
}

// SetConcurrency changes future admissions without cancelling calls already
// sent or replacing the lifetime of a transcription/cache owner.
func (s *Service) SetConcurrency(limit int) error {
	if limit < 1 || limit > 4 {
		return errors.New("use entre uma e quatro transcrições simultâneas")
	}
	s.slots.set(limit)
	return nil
}
