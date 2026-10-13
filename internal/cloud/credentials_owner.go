package cloud

import (
	"context"
	"sync"
)

// credentialOwner outlives the temporary factory context. SDK credential caches
// suppress the caller's cancellation; this separate owner still cancels every
// active process/role retrieval when the cloud client closes.
type credentialOwner struct {
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	closed bool
	active sync.WaitGroup
}

func newCredentialOwner(ctx context.Context) *credentialOwner {
	ctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	return &credentialOwner{ctx: ctx, cancel: cancel}
}

func (o *credentialOwner) begin(ctx context.Context) (context.Context, func(), error) {
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return nil, nil, context.Canceled
	}
	o.active.Add(1)
	o.mu.Unlock()

	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(o.ctx, cancel)
	done := func() {
		stop()
		cancel()
		o.active.Done()
	}
	return ctx, done, nil
}

func (o *credentialOwner) close() {
	o.mu.Lock()
	o.closed = true
	o.cancel()
	o.mu.Unlock()
	// begin cannot add work after closed is set, including a lazy cache
	// retrieval that was queued just before shutdown.
	o.active.Wait()
}
