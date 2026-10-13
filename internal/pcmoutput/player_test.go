package pcmoutput

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"sync"
	"testing"
	"time"
)

type testDevice struct {
	mu      sync.Mutex
	payload []byte
	closed  bool
}

func (d *testDevice) Write(pcm []byte) (int, error) {
	d.mu.Lock()
	d.payload = append(d.payload, pcm...)
	d.mu.Unlock()
	return len(pcm), nil
}
func (*testDevice) Reset() error     { return nil }
func (*testDevice) Pause(bool) error { return nil }
func (d *testDevice) Close() error   { d.mu.Lock(); d.closed = true; d.mu.Unlock(); return nil }

func TestGrowingResponsePausesSeeksAndCancelsWithoutChangingOriginalPCM(t *testing.T) {
	p := New("chosen-output", 24000, 200)
	outputDevice := &testDevice{}
	opened := make(chan struct{})
	p.open = func(endpoint string, rate int) (device, error) {
		if endpoint != "chosen-output" || rate != 24000 {
			t.Errorf("format lost: %s/%d", endpoint, rate)
		}
		close(opened)
		return outputDevice, nil
	}
	p.SetPaused(true)
	pcm := []byte{0xff, 0x7f, 0x00, 0x80, 0, 0}
	original := bytes.Clone(pcm)
	if err := p.Append(pcm); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() { result <- p.Run(ctx) }()
	<-opened
	if snapshot := p.Snapshot(); !snapshot.Paused || snapshot.Position != 0 || snapshot.Duration <= 0 {
		t.Fatal(snapshot)
	}
	p.SetPaused(false)
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for p.Snapshot().Position == 0 {
		select {
		case <-deadline.C:
			t.Fatal("prepared PCM was not delivered")
		case <-time.After(time.Millisecond):
		}
	}
	p.SetPaused(true)
	p.Seek(0)
	if snapshot := p.Snapshot(); snapshot.Position != 0 || !snapshot.Paused {
		t.Fatal(snapshot)
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	outputDevice.mu.Lock()
	defer outputDevice.mu.Unlock()
	if !outputDevice.closed || len(outputDevice.payload) != len(pcm) || !bytes.Equal(pcm, original) {
		t.Fatal("device leaked or input PCM changed")
	}
	if sample := int16(binary.LittleEndian.Uint16(outputDevice.payload)); sample <= 0 {
		t.Fatal("high gain wrapped positive samples")
	}
}

func TestResponseRejectsOddPCMAndUnsafeSeek(t *testing.T) {
	p := New("", 24000, 100)
	if err := p.Append([]byte{1}); err == nil {
		t.Fatal("odd sample accepted")
	}
}

func TestCanceledResponseDoesNotOpenNativeDevice(t *testing.T) {
	p := New("", 24000, 100)
	opened := false
	p.open = func(string, int) (device, error) { opened = true; return &testDevice{}, nil }
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := p.Run(ctx); !errors.Is(err, context.Canceled) || opened {
		t.Fatalf("canceled playback opened native output: opened=%v err=%v", opened, err)
	}
}

type drainingTestDevice struct {
	testDevice
	pendingCalls int
}

func (d *drainingTestDevice) Pending() (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pendingCalls++
	if d.pendingCalls < 3 {
		return 2, nil
	}
	return 0, nil
}

func TestCompletedResponseDrainsAndReleasesNativeOutput(t *testing.T) {
	p := New("", 24000, 100)
	output := &drainingTestDevice{}
	p.open = func(string, int) (device, error) { return output, nil }
	if err := p.Append([]byte{1, 0, 2, 0}); err != nil {
		t.Fatal(err)
	}
	finisher, ok := any(p).(interface{ Finish() })
	if !ok {
		t.Fatal("completed response has no end-of-input signal")
	}
	finisher.Finish()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := p.Run(ctx); err != nil {
		t.Fatalf("completed response remained active: %v", err)
	}
	output.mu.Lock()
	defer output.mu.Unlock()
	if !output.closed || output.pendingCalls < 3 {
		t.Fatalf("output closed before draining: closed=%v pendingCalls=%d", output.closed, output.pendingCalls)
	}
}
