package recorder

/*
#cgo LDFLAGS: -lole32 -lwinmm -luuid
#include "wasapi_windows.h"
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"math"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// SharedMicrophones preserves legacy WinMM IDs and also exposes stable endpoint
// IDs. Enumeration neither opens a capture stream nor reads microphone samples.
func SharedMicrophones() ([]Device, error) { return sharedDevices(false) }
func PlaybackDevices() ([]Device, error)   { return sharedDevices(true) }

func sharedDevices(playback bool) ([]Device, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var items *C.TalkerAudioDevice
	var count C.int
	flag := C.int(0)
	if playback {
		flag = 1
	}
	if err := wasapiError("listar dispositivos", C.talkerAudioDevices(flag, &items, &count)); err != nil {
		return nil, err
	}
	defer C.talkerAudioDevicesFree(items)
	result := make([]Device, 0, int(count))
	for _, item := range unsafe.Slice(items, int(count)) {
		id := unsafe.Slice((*uint16)(unsafe.Pointer(&item.id[0])), len(item.id))
		name := unsafe.Slice((*uint16)(unsafe.Pointer(&item.name[0])), len(item.name))
		result = append(result, Device{ID: int(item.legacyID), Name: windows.UTF16ToString(name), EndpointID: windows.UTF16ToString(id)})
	}
	return result, nil
}

type sharedInput struct {
	control  sync.Mutex
	current  *sharedSession
	endpoint string
	loopback bool
}

type sharedStart struct {
	receive func([]byte, error)
	result  chan error
}

type sharedSession struct {
	start     chan sharedStart
	stop      chan struct{}
	done      chan struct{}
	stopOnce  sync.Once
	mu        sync.Mutex
	started   bool
	startedAt time.Time
	closeErr  error
}

// NewShared records a microphone in explicit shared mode.
func NewShared(device int, endpoint, directory string) *Recorder {
	return NewWithInput(device, directory, &sharedInput{endpoint: endpoint})
}

// NewSystem records the chosen playback endpoint through shared-mode loopback.
func NewSystem(endpoint, directory string) *Recorder {
	return NewWithInput(-1, directory, &sharedInput{endpoint: endpoint, loopback: true})
}

func (in *sharedInput) Open(ctx context.Context, device, sampleRate int) error {
	in.control.Lock()
	defer in.control.Unlock()
	if in.current != nil {
		return ErrAlreadyRecording
	}
	if ctx == nil {
		return errors.New("contexto de captura ausente")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if device < -1 || sampleRate <= 0 || sampleRate > 48000 {
		return ErrUnsupportedFormat
	}
	endpoint, err := windows.UTF16FromString(in.endpoint)
	if err != nil {
		return fmt.Errorf("identificar dispositivo: %w", err)
	}
	session := &sharedSession{start: make(chan sharedStart, 1), stop: make(chan struct{}), done: make(chan struct{})}
	ready := make(chan error, 1)
	go in.capture(ctx, session, endpoint, device, sampleRate, ready)
	if err := <-ready; err != nil {
		<-session.done
		return err
	}
	in.current = session
	return nil
}

func (in *sharedInput) Start(ctx context.Context, receive func([]byte, error)) error {
	in.control.Lock()
	defer in.control.Unlock()
	if in.current == nil || receive == nil {
		return ErrNotRecording
	}
	session := in.current
	session.mu.Lock()
	if session.started {
		session.mu.Unlock()
		return ErrAlreadyRecording
	}
	session.started = true
	session.mu.Unlock()
	start := sharedStart{receive: receive, result: make(chan error, 1)}
	select {
	case session.start <- start:
	case <-session.done:
		return ErrNotRecording
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-start.result:
		return err
	case <-session.done:
		return ErrNotRecording
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (in *sharedInput) Close() error {
	in.control.Lock()
	defer in.control.Unlock()
	if in.current == nil {
		return nil
	}
	session := in.current
	session.stopOnce.Do(func() { close(session.stop) })
	<-session.done
	in.current = nil
	return session.closeErr
}

func (in *sharedInput) StartedAt() time.Time {
	in.control.Lock()
	defer in.control.Unlock()
	if in.current == nil {
		return time.Time{}
	}
	in.current.mu.Lock()
	defer in.current.mu.Unlock()
	return in.current.startedAt
}

func (in *sharedInput) capture(ctx context.Context, session *sharedSession, endpoint []uint16, device, sampleRate int, ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(session.done)
	var native *C.TalkerAudioInput
	loopback := C.int(0)
	if in.loopback {
		loopback = 1
	}
	err := wasapiError("abrir dispositivo compartilhado", C.talkerAudioOpen(
		(*C.wchar_t)(unsafe.Pointer(&endpoint[0])), C.int(device), loopback, C.int(sampleRate), &native,
	))
	runtime.KeepAlive(endpoint)
	if err != nil {
		ready <- err
		return
	}
	defer C.talkerAudioFree(native)
	if err := ctx.Err(); err != nil {
		ready <- err
		return
	}
	ready <- nil
	var start sharedStart
	select {
	case start = <-session.start:
	case <-session.stop:
		return
	case <-ctx.Done():
		return
	}
	err = wasapiError("iniciar captura compartilhada", C.talkerAudioStart(native))
	startedAt := time.Now()
	session.mu.Lock()
	session.startedAt = startedAt
	session.mu.Unlock()
	start.result <- err
	if err != nil {
		return
	}
	receive := start.receive
	buffer := make([]byte, maxPCMBlockBytes)
	emitted := uint64(0)
	pad := func(target uint64) {
		for target > emitted {
			frames := min(target-emitted, uint64(len(buffer)/2))
			payload := buffer[:int(frames)*2]
			clear(payload)
			receive(payload, nil)
			emitted += frames
		}
	}
	read := func() error {
		for {
			var length C.int
			var position C.uint64_t
			code := C.talkerAudioRead(native, (*C.uchar)(unsafe.Pointer(&buffer[0])), C.int(len(buffer)), &length, &position)
			if err := wasapiError("receber áudio compartilhado", code); err != nil {
				return err
			}
			if length == 0 {
				return nil
			}
			payload := buffer[:int(length)]
			if in.loopback && uint64(position) != math.MaxUint64 {
				offset := uint64(position)
				if offset > emitted {
					// Keep the actual packet independent from the silence buffer.
					pcm := append([]byte(nil), payload...)
					pad(offset)
					copy(buffer, pcm)
				} else if offset < emitted {
					skip := min(emitted-offset, uint64(len(payload)/2))
					payload = payload[int(skip)*2:]
				}
			}
			if len(payload) > 0 {
				receive(payload, nil)
				emitted += uint64(len(payload) / 2)
			}
		}
	}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-session.stop:
			session.closeErr = wasapiError("encerrar captura compartilhada", C.talkerAudioStop(native))
			if err := read(); err != nil {
				receive(nil, err)
				session.closeErr = errors.Join(session.closeErr, err)
			}
			if in.loopback {
				pad(uint64(time.Since(startedAt).Seconds() * float64(sampleRate)))
			}
			return
		case <-ctx.Done():
			receive(nil, ctx.Err())
			return
		case <-ticker.C:
			if err := read(); err != nil {
				receive(nil, err)
				return
			}
			if in.loopback {
				elapsed := time.Since(startedAt) - 40*time.Millisecond
				if elapsed > 0 {
					pad(uint64(elapsed.Seconds() * float64(sampleRate)))
				}
			}
		}
	}
}

func wasapiError(operation string, result C.int32_t) error {
	code := uint32(result)
	if code&0x80000000 == 0 {
		return nil
	}
	switch code {
	case 0x88890008:
		return fmt.Errorf("%s: %w", operation, ErrUnsupportedFormat)
	case 0x8889000a:
		return fmt.Errorf("%s: outro programa usa o dispositivo em modo exclusivo; encerre esse acesso ou desative o uso exclusivo nas propriedades de som do Windows", operation)
	case 0x88890004:
		return fmt.Errorf("%s: o dispositivo foi desconectado ou deixou de estar disponível", operation)
	case 0x80070005:
		return fmt.Errorf("%s: o Windows negou acesso ao áudio; confira a permissão do microfone para aplicativos de desktop", operation)
	default:
		return fmt.Errorf("%s: WASAPI retornou 0x%08x", operation, code)
	}
}
