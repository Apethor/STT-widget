package recorder

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"
)

const (
	waveMapper        = uintptr(math.MaxUint32)
	waveFormatPCM     = 1
	waveBadFormat     = 32
	callbackEvent     = 0x00050000
	waveHeaderDone    = 0x00000001
	nativeBufferCount = 8
	nativeBlockFrames = 1024
	eventWaitMillis   = 50
)

var (
	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	procGetSystemDirectory = kernel32.NewProc("GetSystemDirectoryW")
	procCreateEvent        = kernel32.NewProc("CreateEventW")
	procResetEvent         = kernel32.NewProc("ResetEvent")

	winMMOnce               sync.Once
	winMMLoadError          error
	procWaveInGetNumDevs    *syscall.LazyProc
	procWaveInGetDevCaps    *syscall.LazyProc
	procWaveInGetErrorText  *syscall.LazyProc
	procWaveInOpen          *syscall.LazyProc
	procWaveInPrepareHeader *syscall.LazyProc
	procWaveInAddBuffer     *syscall.LazyProc
	procWaveInStart         *syscall.LazyProc
	procWaveInReset         *syscall.LazyProc
	procWaveInUnprepare     *syscall.LazyProc
	procWaveInClose         *syscall.LazyProc
)

// These ABI structures deliberately contain no Go pointers. The driver retains
// WAVEHDR and lpData, so the header and PCM backing array are pinned separately.
// WAVEFORMATEX and WAVEINCAPSW are used only by synchronous calls.
type waveFormat struct {
	FormatTag      uint16
	Channels       uint16
	SamplesPerSec  uint32
	AvgBytesPerSec uint32
	BlockAlign     uint16
	BitsPerSample  uint16
	ExtraSize      uint16
}

type waveHeader struct {
	Data          uintptr
	BufferLength  uint32
	BytesRecorded uint32
	User          uintptr
	Flags         uint32
	Loops         uint32
	Next          uintptr
	Reserved      uintptr
}

type waveInCaps struct {
	Manufacturer uint16
	Product      uint16
	Version      uint32
	Name         [32]uint16
	Formats      uint32
	Channels     uint16
	Reserved     uint16
}

type nativeBuffer struct {
	header   *waveHeader
	data     []byte
	pin      runtime.Pinner
	pinned   bool
	prepared bool
	queued   bool
}

type winMMSession struct {
	handle   uintptr
	event    syscall.Handle
	buffers  []*nativeBuffer
	next     int
	started  bool
	stop     chan struct{}
	stopOnce sync.Once
	done     chan struct{}
	closeErr error
}

type winMMInput struct {
	mu      sync.Mutex
	current *winMMSession
}

// A broken driver may reject Reset/Close while it still owns Go memory. Keep
// that session reachable rather than unpin memory prematurely (or let Pinner's
// leak finalizer panic). A later Close retries cleanup. Healthy shutdown removes
// the session and releases all pins; the registry is never used for dispatch.
var liveNativeInputs = struct {
	sync.Mutex
	sessions map[*winMMSession]struct{}
}{sessions: make(map[*winMMSession]struct{})}

// Microphones returns WinMM input devices using their Unicode product names.
// The default device is selected with -1 and is not an extra enumerated device.
func Microphones() ([]Device, error) {
	if err := loadWinMM(); err != nil {
		return nil, err
	}
	count, _, _ := procWaveInGetNumDevs.Call()
	devices := make([]Device, 0)
	for id := uint32(0); id < uint32(count); id++ {
		var caps waveInCaps
		result, _, _ := procWaveInGetDevCaps.Call(
			uintptr(id), uintptr(unsafe.Pointer(&caps)), unsafe.Sizeof(caps),
		)
		runtime.KeepAlive(&caps)
		if err := winMMError("waveInGetDevCapsW", result); err != nil {
			return nil, err
		}
		if caps.Channels != 0 {
			devices = append(devices, Device{
				ID:   int(id),
				Name: syscall.UTF16ToString(caps.Name[:]),
			})
		}
	}
	return devices, nil
}

func newNativeInput() Input {
	return &winMMInput{}
}

func loadWinMM() error {
	winMMOnce.Do(func() { winMMLoadError = initializeWinMM() })
	return winMMLoadError
}

func initializeWinMM() error {
	for _, proc := range []*syscall.LazyProc{procGetSystemDirectory, procCreateEvent, procResetEvent} {
		if err := proc.Find(); err != nil {
			return fmt.Errorf("não foi possível carregar a API de captura: %w", err)
		}
	}
	var directory [32768]uint16
	n, _, callErr := procGetSystemDirectory.Call(
		uintptr(unsafe.Pointer(&directory[0])), uintptr(len(directory)),
	)
	runtime.KeepAlive(&directory)
	if n == 0 {
		return win32Error("GetSystemDirectoryW", callErr)
	}
	if n >= uintptr(len(directory)) {
		return errors.New("o caminho da biblioteca do sistema excede o limite de captura")
	}
	// Load WinMM by its absolute system path, without searching the working
	// directory for an audio DLL. This implementation needs only the stdlib.
	dll := syscall.NewLazyDLL(filepath.Join(syscall.UTF16ToString(directory[:n]), "winmm.dll"))
	procWaveInGetNumDevs = dll.NewProc("waveInGetNumDevs")
	procWaveInGetDevCaps = dll.NewProc("waveInGetDevCapsW")
	procWaveInGetErrorText = dll.NewProc("waveInGetErrorTextW")
	procWaveInOpen = dll.NewProc("waveInOpen")
	procWaveInPrepareHeader = dll.NewProc("waveInPrepareHeader")
	procWaveInAddBuffer = dll.NewProc("waveInAddBuffer")
	procWaveInStart = dll.NewProc("waveInStart")
	procWaveInReset = dll.NewProc("waveInReset")
	procWaveInUnprepare = dll.NewProc("waveInUnprepareHeader")
	procWaveInClose = dll.NewProc("waveInClose")
	for _, proc := range []*syscall.LazyProc{
		procWaveInGetNumDevs, procWaveInGetDevCaps, procWaveInGetErrorText,
		procWaveInOpen, procWaveInPrepareHeader, procWaveInAddBuffer,
		procWaveInStart, procWaveInReset, procWaveInUnprepare, procWaveInClose,
	} {
		if err := proc.Find(); err != nil {
			return fmt.Errorf("não foi possível carregar WinMM: %w", err)
		}
	}
	return nil
}

func (in *winMMInput) Open(ctx context.Context, device, sampleRate int) error {
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.current != nil {
		return ErrAlreadyRecording
	}
	if ctx == nil {
		return errors.New("contexto de captura ausente")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if device < -1 || (device >= 0 && uint64(device) > math.MaxUint32) {
		return fmt.Errorf("identificador de microfone inválido: %d", device)
	}
	if sampleRate <= 0 || uint64(sampleRate) > math.MaxUint32/2 {
		return fmt.Errorf("taxa de amostragem inválida: %d", sampleRate)
	}
	if err := loadWinMM(); err != nil {
		return err
	}

	// A manual-reset event coalesces notifications. No Go callback, pointer to
	// callback state, or function on a driver thread crosses the WinMM boundary.
	eventValue, _, callErr := procCreateEvent.Call(0, 1, 0, 0)
	if eventValue == 0 {
		return win32Error("CreateEventW", callErr)
	}
	event := syscall.Handle(eventValue)
	format := waveFormat{
		FormatTag:      waveFormatPCM,
		Channels:       1,
		SamplesPerSec:  uint32(sampleRate),
		AvgBytesPerSec: uint32(sampleRate) * 2,
		BlockAlign:     2,
		BitsPerSample:  16,
	}
	deviceID := waveMapper
	if device >= 0 {
		deviceID = uintptr(device)
	}
	// The returned handle lives in its own pointer-free scalar, rather than
	// passing the address of a field of a struct containing Go pointers.
	var handle uintptr
	result, _, _ := procWaveInOpen.Call(
		uintptr(unsafe.Pointer(&handle)),
		deviceID,
		uintptr(unsafe.Pointer(&format)),
		uintptr(event),
		0,
		callbackEvent,
	)
	runtime.KeepAlive(&format)
	runtime.KeepAlive(&handle)
	if err := winMMError("waveInOpen", result); err != nil {
		return errors.Join(err, syscall.CloseHandle(event))
	}

	s := &winMMSession{
		handle: handle,
		event:  event,
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
	in.current = s
	liveNativeInputs.Lock()
	liveNativeInputs.sessions[s] = struct{}{}
	liveNativeInputs.Unlock()

	for range nativeBufferCount {
		if err := ctx.Err(); err != nil {
			return err // Recorder calls Close even after a failed Open.
		}
		buffer := &nativeBuffer{
			data:   make([]byte, nativeBlockFrames*2),
			header: new(waveHeader),
		}
		buffer.pin.Pin(&buffer.data[0])
		buffer.pin.Pin(buffer.header)
		buffer.pinned = true
		buffer.header.Data = uintptr(unsafe.Pointer(&buffer.data[0]))
		buffer.header.BufferLength = uint32(len(buffer.data))
		s.buffers = append(s.buffers, buffer)
		result, _, _ := procWaveInPrepareHeader.Call(
			handle, uintptr(unsafe.Pointer(buffer.header)), unsafe.Sizeof(*buffer.header),
		)
		runtime.KeepAlive(buffer)
		if err := winMMError("waveInPrepareHeader", result); err != nil {
			return err
		}
		buffer.prepared = true
		if err := s.addBuffer(buffer); err != nil {
			return err
		}
	}
	return nil
}

func (in *winMMInput) Start(ctx context.Context, receive func([]byte, error)) error {
	in.mu.Lock()
	defer in.mu.Unlock()
	s := in.current
	if s == nil {
		return ErrNotRecording
	}
	if s.started {
		return ErrAlreadyRecording
	}
	if ctx == nil || receive == nil {
		return errors.New("contexto ou destino de captura ausente")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	result, _, _ := procWaveInStart.Call(s.handle)
	if err := winMMError("waveInStart", result); err != nil {
		return err
	}
	s.started = true
	go s.run(ctx, receive)
	return nil
}

func (in *winMMInput) Close() error {
	in.mu.Lock()
	s := in.current
	if s == nil {
		in.mu.Unlock()
		return nil
	}
	if !s.started {
		err := s.shutdown(nil)
		if !s.resourcesHeld() {
			in.current = nil
		}
		in.mu.Unlock()
		return err
	}
	s.stopOnce.Do(func() { close(s.stop) })
	in.mu.Unlock()
	// The bounded native wait observes stop within 50 ms. Waiting here avoids
	// closing an event or touching driver headers concurrently with run.
	<-s.done

	in.mu.Lock()
	defer in.mu.Unlock()
	err := s.closeErr
	if in.current == s {
		if s.resourcesHeld() {
			err = errors.Join(err, s.shutdown(nil))
		}
		if !s.resourcesHeld() {
			in.current = nil
		}
	}
	return err
}

func (s *winMMSession) run(ctx context.Context, receive func([]byte, error)) {
	defer close(s.done)
	for {
		select {
		case <-ctx.Done():
			s.closeErr = s.shutdown(receive)
			return
		case <-s.stop:
			s.closeErr = s.shutdown(receive)
			return
		default:
		}
		// Reset before scanning headers: a completion concurrent with the scan
		// either appears as DONE or leaves the event signaled for the next wait.
		reset, _, resetErr := procResetEvent.Call(uintptr(s.event))
		if reset == 0 {
			receive(nil, win32Error("ResetEvent", resetErr))
			s.closeErr = s.shutdown(receive)
			return
		}
		if err := s.deliverReady(receive, true); err != nil {
			receive(nil, err)
			s.closeErr = s.shutdown(receive)
			return
		}
		result, err := syscall.WaitForSingleObject(s.event, eventWaitMillis)
		if err != nil {
			receive(nil, fmt.Errorf("a espera pela captura falhou: %w", err))
			s.closeErr = s.shutdown(receive)
			return
		}
		if result != syscall.WAIT_OBJECT_0 && result != uint32(syscall.WAIT_TIMEOUT) {
			receive(nil, fmt.Errorf("a espera pela captura retornou um estado inválido: %d", result))
			s.closeErr = s.shutdown(receive)
			return
		}
	}
}

func (s *winMMSession) addBuffer(buffer *nativeBuffer) error {
	buffer.header.BytesRecorded = 0
	result, _, _ := procWaveInAddBuffer.Call(
		s.handle, uintptr(unsafe.Pointer(buffer.header)), unsafe.Sizeof(*buffer.header),
	)
	runtime.KeepAlive(buffer)
	if err := winMMError("waveInAddBuffer", result); err != nil {
		return err
	}
	buffer.queued = true
	return nil
}

func (s *winMMSession) deliverReady(receive func([]byte, error), requeue bool) error {
	// Scan in queue order, including after a wrap. Scanning from buffer zero
	// would reorder samples when multiple coalesced notifications are pending.
	for range len(s.buffers) {
		buffer := s.buffers[s.next]
		if !buffer.queued || atomic.LoadUint32(&buffer.header.Flags)&waveHeaderDone == 0 {
			break
		}
		buffer.queued = false
		s.next = (s.next + 1) % len(s.buffers)
		n := atomic.LoadUint32(&buffer.header.BytesRecorded)
		if uint64(n) > uint64(len(buffer.data)) || n%2 != 0 {
			return errors.New("WinMM devolveu um tamanho de bloco PCM inválido")
		}
		if n != 0 && receive != nil {
			receive(buffer.data[:int(n)], nil)
		}
		if requeue {
			if err := s.addBuffer(buffer); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *winMMSession) shutdown(receive func([]byte, error)) error {
	var errs []error
	if s.handle != 0 {
		// Reset returns every pending buffer, including the final partial one.
		// Only after that ownership barrier may data be read or unprepared.
		result, _, _ := procWaveInReset.Call(s.handle)
		if err := winMMError("waveInReset", result); err != nil {
			errs = append(errs, err)
		} else if err := s.deliverReady(receive, false); err != nil {
			errs = append(errs, err)
		}
		for _, buffer := range s.buffers {
			if !buffer.prepared {
				continue
			}
			result, _, _ := procWaveInUnprepare.Call(
				s.handle, uintptr(unsafe.Pointer(buffer.header)), unsafe.Sizeof(*buffer.header),
			)
			runtime.KeepAlive(buffer)
			if err := winMMError("waveInUnprepareHeader", result); err != nil {
				errs = append(errs, err)
			} else {
				buffer.prepared = false
				buffer.queued = false
			}
		}
		result, _, _ = procWaveInClose.Call(s.handle)
		if err := winMMError("waveInClose", result); err != nil {
			errs = append(errs, err)
		} else {
			s.handle = 0
		}
	}
	for _, buffer := range s.buffers {
		if buffer.pinned && (!buffer.prepared || s.handle == 0) {
			// A successful Close is the final driver ownership barrier even
			// when an earlier Unprepare call reported a driver error.
			buffer.pin.Unpin()
			buffer.pinned = false
		}
	}
	if s.handle == 0 && s.event != 0 {
		if err := syscall.CloseHandle(s.event); err != nil {
			errs = append(errs, err)
		} else {
			s.event = 0
		}
	}
	if !s.resourcesHeld() {
		liveNativeInputs.Lock()
		delete(liveNativeInputs.sessions, s)
		liveNativeInputs.Unlock()
	}
	runtime.KeepAlive(s)
	return errors.Join(errs...)
}

func (s *winMMSession) resourcesHeld() bool {
	if s.handle != 0 || s.event != 0 {
		return true
	}
	for _, buffer := range s.buffers {
		if buffer.pinned {
			return true
		}
	}
	return false
}

func winMMError(operation string, result uintptr) error {
	if result == 0 {
		return nil
	}
	var text [256]uint16
	textResult, _, _ := procWaveInGetErrorText.Call(
		result, uintptr(unsafe.Pointer(&text[0])), uintptr(len(text)),
	)
	runtime.KeepAlive(&text)
	message := "erro do driver"
	if textResult == 0 {
		message = syscall.UTF16ToString(text[:])
	}
	err := fmt.Errorf("%s: %s (WinMM %d)", operation, message, result)
	if result == waveBadFormat {
		return errors.Join(ErrUnsupportedFormat, err)
	}
	return err
}

func win32Error(operation string, err error) error {
	if err == nil || err == syscall.Errno(0) {
		err = syscall.EINVAL
	}
	return fmt.Errorf("%s: %w", operation, err)
}
