package recorder

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type syntheticInput struct {
	mu sync.Mutex

	rates         []int
	devices       []int
	openError     func(int) error
	openHook      func(context.Context)
	startError    error
	closeError    error
	closeFailures int
	startPCM      []byte
	finalPCM      []byte
	receive       func([]byte, error)
	closed        chan struct{}
	closeCalls    int
	open          bool
	startCalls    int
}

func (in *syntheticInput) Open(ctx context.Context, device, rate int) error {
	in.mu.Lock()
	in.rates = append(in.rates, rate)
	in.devices = append(in.devices, device)
	in.open = true
	in.closed = make(chan struct{})
	hook := in.openHook
	in.mu.Unlock()
	if hook != nil {
		hook(ctx)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if in.openError != nil {
		return in.openError(rate)
	}
	return nil
}

func (in *syntheticInput) Start(ctx context.Context, receive func([]byte, error)) error {
	in.mu.Lock()
	in.startCalls++
	in.receive = receive
	pcm, err := in.startPCM, in.startError
	in.mu.Unlock()
	if len(pcm) != 0 {
		receive(pcm, nil)
	}
	if err != nil {
		return err
	}
	return ctx.Err()
}

func (in *syntheticInput) Close() error {
	in.mu.Lock()
	defer in.mu.Unlock()
	if !in.open {
		return nil
	}
	in.closeCalls++
	if in.closeFailures > 0 {
		in.closeFailures--
		return in.closeError
	}
	in.open = false
	if in.receive != nil && len(in.finalPCM) != 0 {
		in.receive(in.finalPCM, nil)
	}
	in.receive = nil
	close(in.closed)
	return in.closeError
}

func (in *syntheticInput) feed(t *testing.T, pcm []byte, err error) {
	t.Helper()
	in.mu.Lock()
	receive := in.receive
	in.mu.Unlock()
	if receive == nil {
		t.Fatal("synthetic input is not capturing")
	}
	receive(pcm, err)
}

func (in *syntheticInput) callback(t *testing.T) func([]byte, error) {
	t.Helper()
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.receive == nil {
		t.Fatal("synthetic input is not capturing")
	}
	return in.receive
}

func (in *syntheticInput) waitClosed(t *testing.T) {
	t.Helper()
	in.mu.Lock()
	closed := in.closed
	in.mu.Unlock()
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("input resources were not released")
	}
}

func pcmSamples(values ...int16) []byte {
	pcm := make([]byte, len(values)*2)
	for index, value := range values {
		binary.LittleEndian.PutUint16(pcm[index*2:], uint16(value))
	}
	return pcm
}

func readRecording(t *testing.T, path string) (int, []byte) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < waveHeaderBytes {
		t.Fatalf("WAV header is incomplete: %d bytes", len(data))
	}
	if string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" ||
		string(data[12:16]) != "fmt " || string(data[36:40]) != "data" {
		t.Fatalf("invalid PCM WAV header: %x", data[:waveHeaderBytes])
	}
	if binary.LittleEndian.Uint32(data[16:20]) != 16 ||
		binary.LittleEndian.Uint16(data[20:22]) != 1 ||
		binary.LittleEndian.Uint16(data[22:24]) != 1 ||
		binary.LittleEndian.Uint16(data[32:34]) != 2 ||
		binary.LittleEndian.Uint16(data[34:36]) != 16 {
		t.Fatalf("unexpected mono s16 PCM format: %x", data[:waveHeaderBytes])
	}
	rate := binary.LittleEndian.Uint32(data[24:28])
	if binary.LittleEndian.Uint32(data[28:32]) != rate*2 {
		t.Fatal("incorrect PCM byte rate")
	}
	size := binary.LittleEndian.Uint32(data[40:44])
	if uint64(size)+waveHeaderBytes > uint64(len(data)) ||
		size%2 != 0 || binary.LittleEndian.Uint32(data[4:8]) != size+36 {
		t.Fatalf("incorrect RIFF/data lengths: RIFF=%d, PCM=%d, file=%d",
			binary.LittleEndian.Uint32(data[4:8]), size, len(data))
	}
	return int(rate), data[waveHeaderBytes : waveHeaderBytes+int(size)]
}

func assertStopped(t *testing.T, r *Recorder, in *syntheticInput) {
	t.Helper()
	state := r.Snapshot()
	if state.Active || state.Paused || state.Level != 0 {
		t.Fatalf("recorder is still active: %+v", state)
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.open || in.receive != nil {
		t.Fatal("input was not released")
	}
}

func TestRecorderStartAndPausePreserveExactPCM(t *testing.T) {
	t.Parallel()
	in := new(syntheticInput)
	r := NewWithInput(-1, t.TempDir(), in)
	path, err := r.Start(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = r.Stop() })
	in.feed(t, pcmSamples(100, -100, 200), nil)
	r.Pause()
	in.feed(t, pcmSamples(1000, -1000), nil)
	state := r.Snapshot()
	if !state.Active || !state.Paused || state.Level != 0 ||
		math.Abs(state.Duration-3.0/16000) > 1e-12 {
		t.Fatalf("paused samples changed state: %+v", state)
	}
	r.Resume()
	in.feed(t, pcmSamples(500, -500), nil)
	if state := r.Snapshot(); state.Level != 500.0/32768 {
		t.Fatalf("incorrect peak amplitude: %v", state.Level)
	}
	gotPath, err := r.Stop()
	if err != nil || gotPath != path {
		t.Fatalf("Stop = %q, %v; want %q", gotPath, err, path)
	}
	rate, pcm := readRecording(t, path)
	if rate != 16000 || !bytes.Equal(pcm, pcmSamples(100, -100, 200, 500, -500)) {
		t.Fatalf("paused PCM was changed: rate=%d, pcm=%x", rate, pcm)
	}
	if math.Abs(r.Snapshot().Duration-5.0/16000) > 1e-12 {
		t.Fatal("duration includes paused audio")
	}
	assertStopped(t, r, in)
}

func TestRecorderStartUsesFallbackOnlyForUnsupportedFormat(t *testing.T) {
	t.Parallel()
	deviceFailure := errors.New("device unavailable")
	closeFailure := errors.New("partial open could not be closed")
	for _, tc := range []struct {
		name        string
		firstError  error
		secondError error
		closeError  error
		wantRates   []int
		wantRate    int
		wantError   error
	}{
		{
			name:      "preferred format",
			wantRates: []int{16000},
			wantRate:  16000,
		},
		{
			name:       "48 kHz fallback",
			firstError: fmt.Errorf("format: %w", ErrUnsupportedFormat),
			wantRates:  []int{16000, 48000},
			wantRate:   48000,
		},
		{
			name:       "device failure is not retried",
			firstError: deviceFailure,
			wantRates:  []int{16000},
			wantError:  deviceFailure,
		},
		{
			name:        "both formats rejected",
			firstError:  ErrUnsupportedFormat,
			secondError: ErrUnsupportedFormat,
			wantRates:   []int{16000, 48000},
			wantError:   ErrUnsupportedFormat,
		},
		{
			name:       "fallback requires successful cleanup",
			firstError: ErrUnsupportedFormat,
			closeError: closeFailure,
			wantRates:  []int{16000},
			wantError:  closeFailure,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := &syntheticInput{
				openError: func(rate int) error {
					if rate == 16000 {
						return tc.firstError
					}
					return tc.secondError
				},
				closeError: tc.closeError,
			}
			r := NewWithInput(7, t.TempDir(), in)
			path, err := r.Start(t.Context())
			t.Cleanup(func() { _, _ = r.Stop() })
			if tc.wantError != nil {
				if !errors.Is(err, tc.wantError) || path != "" {
					t.Fatalf("Start = %q, %v", path, err)
				}
				assertStopped(t, r, in)
			} else {
				if err != nil {
					t.Fatal(err)
				}
				in.feed(t, pcmSamples(7, 8), nil)
				if tc.wantRate == 48000 && !strings.Contains(r.Snapshot().Warning, "48000") {
					t.Fatal("fallback is not visible in the recorder state")
				}
				if _, err := r.Stop(); err != nil {
					t.Fatal(err)
				}
				rate, pcm := readRecording(t, path)
				if rate != tc.wantRate || !bytes.Equal(pcm, pcmSamples(7, 8)) {
					t.Fatalf("fallback changed PCM: rate=%d, PCM=%x", rate, pcm)
				}
			}
			if fmt.Sprint(in.rates) != fmt.Sprint(tc.wantRates) {
				t.Fatalf("requested rates = %v, want %v", in.rates, tc.wantRates)
			}
			for _, device := range in.devices {
				if device != 7 {
					t.Fatalf("wrong input device: %d", device)
				}
			}
		})
	}
}

func TestRecorderStartFailureReleasesInputAndPreservesFile(t *testing.T) {
	t.Parallel()
	startFailure := errors.New("driver could not start")
	in := &syntheticInput{
		startError: startFailure,
		startPCM:   pcmSamples(11, -11),
		finalPCM:   pcmSamples(12),
	}
	r := NewWithInput(-1, t.TempDir(), in)
	path, err := r.Start(t.Context())
	if !errors.Is(err, startFailure) || path == "" || !strings.Contains(err.Error(), path) {
		t.Fatalf("Start failure = %q, %v", path, err)
	}
	_, pcm := readRecording(t, path)
	if !bytes.Equal(pcm, pcmSamples(11, -11, 12)) {
		t.Fatalf("PCM delivered before start failure was lost: %x", pcm)
	}
	assertStopped(t, r, in)
	if in.closeCalls != 1 {
		t.Fatalf("input Close called %d times", in.closeCalls)
	}
	if gotPath, stopErr := r.Stop(); gotPath != path || !errors.Is(stopErr, startFailure) {
		t.Fatalf("Stop after start failure = %q, %v", gotPath, stopErr)
	}
}

func TestRecorderStopRetriesCleanupAfterFailedOpen(t *testing.T) {
	t.Parallel()
	openFailure := errors.New("partial input open failure")
	closeFailure := errors.New("driver cleanup failed")
	in := &syntheticInput{
		openError:     func(int) error { return openFailure },
		closeError:    closeFailure,
		closeFailures: 1,
	}
	r := NewWithInput(-1, t.TempDir(), in)
	if path, err := r.Start(t.Context()); path != "" ||
		!errors.Is(err, openFailure) || !errors.Is(err, closeFailure) {
		t.Fatalf("Start = %q, %v", path, err)
	}
	in.mu.Lock()
	held := in.open
	in.mu.Unlock()
	if !held {
		t.Fatal("synthetic driver did not retain resources for the cleanup retry")
	}
	if _, err := r.Stop(); !errors.Is(err, openFailure) || !errors.Is(err, closeFailure) {
		t.Fatalf("Stop lost the original startup failure: %v", err)
	}
	assertStopped(t, r, in)
	if in.closeCalls != 2 {
		t.Fatalf("Stop did not retry cleanup: Close attempts = %d", in.closeCalls)
	}
}

func TestRecorderCopiesBorrowedPCMBeforeInputReusesBuffer(t *testing.T) {
	t.Parallel()
	in := new(syntheticInput)
	r := NewWithInput(-1, t.TempDir(), in)
	path, err := r.Start(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = r.Stop() })
	pcm := pcmSamples(123, -32768)
	in.feed(t, pcm, nil)
	if r.Snapshot().Level != 1 {
		t.Fatal("the minimum s16 value did not produce the full peak level")
	}
	clear(pcm) // The native driver is allowed to overwrite its returned buffer.
	if _, err := r.Stop(); err != nil {
		t.Fatal(err)
	}
	_, saved := readRecording(t, path)
	if !bytes.Equal(saved, pcmSamples(123, -32768)) {
		t.Fatalf("input buffer reuse changed queued audio: %x", saved)
	}
}

func TestRecorderStartFailureBeforeInputCreatesNoResources(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		ctx      func() context.Context
		device   int
		input    bool
		typedNil bool
	}{
		{name: "canceled context", ctx: func() context.Context {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx
		}, device: -1, input: true},
		{name: "nil context", ctx: func() context.Context { return nil }, device: -1, input: true},
		{name: "invalid device", ctx: context.Background, device: -2, input: true},
		{name: "missing input", ctx: context.Background, device: -1},
		{name: "typed nil input", ctx: context.Background, device: -1, typedNil: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := new(syntheticInput)
			var input Input
			if tc.input {
				input = in
			}
			if tc.typedNil {
				input = (*syntheticInput)(nil)
			}
			r := NewWithInput(tc.device, t.TempDir(), input)
			if path, err := r.Start(tc.ctx()); err == nil || path != "" {
				t.Fatalf("Start = %q, %v; want failure without a path", path, err)
			}
			if len(in.rates) != 0 || in.closeCalls != 0 {
				t.Fatal("input was accessed before validating Start")
			}
			if state := r.Snapshot(); state.Error == "" || state.Active {
				t.Fatalf("startup error is not visible: %+v", state)
			}
		})
	}
}

func TestRecorderStartCancellationDuringOpenReleasesInput(t *testing.T) {
	t.Parallel()
	entered := make(chan struct{})
	in := &syntheticInput{openHook: func(ctx context.Context) {
		close(entered)
		<-ctx.Done()
	}}
	r := NewWithInput(-1, t.TempDir(), in)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := r.Start(ctx)
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("input did not receive the startup context")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Start = %v, want context.Canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Start did not observe cancellation")
	}
	assertStopped(t, r, in)
	if in.closeCalls != 1 {
		t.Fatalf("partial Open resources were not closed: %d", in.closeCalls)
	}
}

func TestRecorderFileCreationAndHeaderFailuresReleaseInput(t *testing.T) {
	t.Parallel()
	fileFailure := errors.New("file creation failed")
	for _, tc := range []struct {
		name       string
		failHeader bool
	}{
		{name: "file creation failure"},
		{name: "header write failure", failHeader: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := new(syntheticInput)
			r := NewWithInput(-1, t.TempDir(), in)
			var file *faultFile
			r.createFile = func(directory string) (recordingFile, string, error) {
				if !tc.failHeader {
					return nil, "", fileFailure
				}
				raw, path, err := createRecordingFile(directory)
				if err != nil {
					t.Fatal(err)
				}
				file = &faultFile{recordingFile: raw, failWriteAt: 1, writeError: fileFailure}
				return file, path, nil
			}
			path, err := r.Start(t.Context())
			if !errors.Is(err, fileFailure) {
				t.Fatalf("Start = %v, want file failure", err)
			}
			assertStopped(t, r, in)
			if in.closeCalls != 1 || in.startCalls != 0 {
				t.Fatal("failed writer still started or retained input")
			}
			if file != nil {
				if !file.closed.Load() || path == "" {
					t.Fatal("failed header writer was not closed or its path was lost")
				}
				if _, err := os.Stat(path); err != nil {
					t.Fatalf("partial file was deleted: %v", err)
				}
			}
		})
	}
}

func TestRecorderCancellationFinalizesCapturedAudio(t *testing.T) {
	t.Parallel()
	in := &syntheticInput{finalPCM: pcmSamples(400)}
	r := NewWithInput(-1, t.TempDir(), in)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	path, err := r.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = r.Stop() })
	in.feed(t, pcmSamples(200, -200, 300), nil)
	cancel()
	in.waitClosed(t)
	if gotPath, err := r.Stop(); gotPath != path || !errors.Is(err, context.Canceled) {
		t.Fatalf("Stop after cancellation = %q, %v", gotPath, err)
	}
	_, pcm := readRecording(t, path)
	if !bytes.Equal(pcm, pcmSamples(200, -200, 300, 400)) {
		t.Fatalf("cancellation lost captured audio: %x", pcm)
	}
	assertStopped(t, r, in)
}

func TestRecorderCaptureFailureFinalizesCapturedAudio(t *testing.T) {
	t.Parallel()
	captureFailure := errors.New("microphone disconnected")
	in := &syntheticInput{finalPCM: pcmSamples(70)}
	r := NewWithInput(-1, t.TempDir(), in)
	path, err := r.Start(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = r.Stop() })
	in.feed(t, pcmSamples(40, 50), nil)
	in.feed(t, pcmSamples(60), captureFailure)
	in.waitClosed(t)
	if gotPath, err := r.Stop(); gotPath != path || !errors.Is(err, captureFailure) {
		t.Fatalf("Stop = %q, %v", gotPath, err)
	}
	_, pcm := readRecording(t, path)
	if !bytes.Equal(pcm, pcmSamples(40, 50, 60, 70)) {
		t.Fatalf("capture failure lost complete audio: %x", pcm)
	}
	assertStopped(t, r, in)
}

func TestRecorderRejectsMalformedAndOversizedBlocks(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		pcm  []byte
	}{
		{name: "incomplete s16 sample", pcm: []byte{1}},
		{name: "unbounded capture block", pcm: make([]byte, maxPCMBlockBytes+2)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := new(syntheticInput)
			r := NewWithInput(-1, t.TempDir(), in)
			path, err := r.Start(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _, _ = r.Stop() })
			in.feed(t, pcmSamples(12), nil)
			in.feed(t, tc.pcm, nil)
			if _, err := r.Stop(); err == nil {
				t.Fatal("invalid capture was silently accepted")
			}
			_, pcm := readRecording(t, path)
			if !bytes.Equal(pcm, pcmSamples(12)) {
				t.Fatalf("earlier valid PCM was lost: %x", pcm)
			}
			assertStopped(t, r, in)
		})
	}
}

type faultFile struct {
	recordingFile
	writeCalls  int
	failWriteAt int
	writeError  error
	partial     int
	entered     chan struct{}
	release     chan struct{}
	blockOnce   sync.Once
	closed      atomic.Bool
}

func (f *faultFile) Write(data []byte) (int, error) {
	f.writeCalls++
	if f.entered != nil && f.writeCalls > 1 {
		f.blockOnce.Do(func() {
			close(f.entered)
			<-f.release
		})
	}
	if f.writeCalls == f.failWriteAt {
		if f.partial != 0 {
			n, err := f.recordingFile.Write(data[:min(f.partial, len(data))])
			return n, errors.Join(f.writeError, err)
		}
		return 0, f.writeError
	}
	return f.recordingFile.Write(data)
}

func (f *faultFile) Close() error {
	f.closed.Store(true)
	return f.recordingFile.Close()
}

func TestRecorderWriterFailurePreservesCompleteSamplesAndClosesResources(t *testing.T) {
	t.Parallel()
	diskFailure := errors.New("disk full")
	in := new(syntheticInput)
	r := NewWithInput(-1, t.TempDir(), in)
	var file *faultFile
	r.createFile = func(directory string) (recordingFile, string, error) {
		raw, path, err := createRecordingFile(directory)
		file = &faultFile{
			recordingFile: raw,
			failWriteAt:   2,
			writeError:    diskFailure,
			partial:       3,
		}
		return file, path, err
	}
	path, err := r.Start(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = r.Stop() })
	in.feed(t, pcmSamples(500, 501, 502), nil)
	if gotPath, err := r.Stop(); gotPath != path || !errors.Is(err, diskFailure) {
		t.Fatalf("Stop = %q, %v", gotPath, err)
	}
	_, pcm := readRecording(t, path)
	if !bytes.Equal(pcm, pcmSamples(500)) {
		t.Fatalf("complete samples before disk failure were lost: %x", pcm)
	}
	if !file.closed.Load() || math.Abs(r.Snapshot().Duration-1.0/16000) > 1e-12 {
		t.Fatal("writer resources or saved duration are incorrect after error")
	}
	assertStopped(t, r, in)
}

func TestRecorderSlowDiskHasBoundedQueueAndStopsCapture(t *testing.T) {
	t.Parallel()
	in := new(syntheticInput)
	r := NewWithInput(-1, t.TempDir(), in)
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }); _, _ = r.Stop() })
	r.createFile = func(directory string) (recordingFile, string, error) {
		raw, path, err := createRecordingFile(directory)
		return &faultFile{recordingFile: raw, entered: entered, release: release}, path, err
	}
	path, err := r.Start(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	in.feed(t, pcmSamples(1), nil)
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("writer did not receive initial PCM")
	}
	for range pendingPCMBlocks {
		in.feed(t, pcmSamples(1), nil)
	}
	in.feed(t, pcmSamples(2), nil)
	if state := r.Snapshot(); state.Error == "" || state.Active {
		t.Fatalf("queue overflow was not visible: %+v", state)
	}
	// Capture must be released even though disk IO is still blocked.
	in.waitClosed(t)
	releaseOnce.Do(func() { close(release) })
	if _, err := r.Stop(); err == nil || !strings.Contains(err.Error(), "disco") {
		t.Fatalf("Stop = %v, want visible queue overflow", err)
	}
	_, pcm := readRecording(t, path)
	want := bytes.Repeat(pcmSamples(1), pendingPCMBlocks+1)
	if !bytes.Equal(pcm, want) {
		t.Fatalf("overflow did not preserve queued audio: got %d bytes, want %d", len(pcm), len(want))
	}
	assertStopped(t, r, in)
}

func TestRecorderStopFlushesFinalPartialBlockAndKeepsPause(t *testing.T) {
	t.Parallel()
	for _, paused := range []bool{false, true} {
		t.Run(fmt.Sprintf("paused=%t", paused), func(t *testing.T) {
			t.Parallel()
			in := &syntheticInput{finalPCM: pcmSamples(30, 40)}
			r := NewWithInput(-1, t.TempDir(), in)
			path, err := r.Start(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _, _ = r.Stop() })
			in.feed(t, pcmSamples(10, 20), nil)
			want := pcmSamples(10, 20, 30, 40)
			if paused {
				r.Pause()
				want = pcmSamples(10, 20)
			}
			if _, err := r.Stop(); err != nil {
				t.Fatal(err)
			}
			_, pcm := readRecording(t, path)
			if !bytes.Equal(pcm, want) {
				t.Fatalf("incorrect final block: got %x, want %x", pcm, want)
			}
			if secondPath, err := r.Stop(); err != nil || secondPath != path || in.closeCalls != 1 {
				t.Fatalf("Stop is not idempotent: %q, %v, closes=%d", secondPath, err, in.closeCalls)
			}
		})
	}
}

func TestRecorderStopWithoutAudioReportsErrorAndPreservesEmptyWAV(t *testing.T) {
	t.Parallel()
	in := new(syntheticInput)
	r := NewWithInput(-1, t.TempDir(), in)
	if _, err := r.Stop(); !errors.Is(err, ErrNotRecording) {
		t.Fatalf("Stop before Start = %v", err)
	}
	path, err := r.Start(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if gotPath, err := r.Stop(); gotPath != path || !errors.Is(err, ErrNoAudio) {
		t.Fatalf("empty Stop = %q, %v", gotPath, err)
	}
	_, pcm := readRecording(t, path)
	if len(pcm) != 0 || r.Snapshot().Error == "" {
		t.Fatal("empty WAV or its visible error is incorrect")
	}
	assertStopped(t, r, in)
}

func TestRecorderCloseErrorPreservesAudio(t *testing.T) {
	t.Parallel()
	closeFailure := errors.New("driver close failure")
	in := &syntheticInput{closeError: closeFailure}
	r := NewWithInput(-1, t.TempDir(), in)
	path, err := r.Start(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	in.feed(t, pcmSamples(7), nil)
	if _, err := r.Stop(); !errors.Is(err, closeFailure) {
		t.Fatalf("Stop = %v, want close failure", err)
	}
	_, pcm := readRecording(t, path)
	if !bytes.Equal(pcm, pcmSamples(7)) {
		t.Fatalf("input close failure lost audio: %x", pcm)
	}
	assertStopped(t, r, in)
}

func TestRecorderRestartIsolatesCallbacksAndKeepsEarlierFiles(t *testing.T) {
	t.Parallel()
	in := new(syntheticInput)
	r := NewWithInput(-1, t.TempDir(), in)
	first, err := r.Start(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = r.Stop() })
	oldReceive := in.callback(t)
	in.feed(t, pcmSamples(1), nil)
	if _, err := r.Start(t.Context()); !errors.Is(err, ErrAlreadyRecording) {
		t.Fatalf("second simultaneous Start = %v", err)
	}
	if _, err := r.Stop(); err != nil {
		t.Fatal(err)
	}
	second, err := r.Start(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if second == first || r.Snapshot().Duration != 0 || r.Snapshot().Error != "" {
		t.Fatal("new recording reused earlier state or path")
	}
	oldReceive(pcmSamples(900), errors.New("old generation"))
	in.feed(t, pcmSamples(2), nil)
	if _, err := r.Stop(); err != nil {
		t.Fatal(err)
	}
	_, firstPCM := readRecording(t, first)
	_, secondPCM := readRecording(t, second)
	if !bytes.Equal(firstPCM, pcmSamples(1)) || !bytes.Equal(secondPCM, pcmSamples(2)) {
		t.Fatalf("generation isolation failed: first=%x second=%x", firstPCM, secondPCM)
	}
}

func TestRecorderConcurrentControlsAndStop(t *testing.T) {
	t.Parallel()
	in := new(syntheticInput)
	r := NewWithInput(-1, t.TempDir(), in)
	path, err := r.Start(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = r.Stop() })
	receive := in.callback(t)
	receive(pcmSamples(5), nil)
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 100 {
				r.Pause()
				r.Resume()
				_ = r.Snapshot()
				receive(pcmSamples(6), nil)
			}
		})
	}
	// Exercise callback admission concurrently with queue closure. A temporary
	// overload may report the bounded-queue error, but must still save valid PCM.
	if gotPath, err := r.Stop(); gotPath != path || (err != nil && !strings.Contains(err.Error(), "disco")) {
		t.Fatalf("concurrent Stop = %q, %v", gotPath, err)
	}
	wg.Wait()
	_, _ = readRecording(t, path)
	assertStopped(t, r, in)
}

func TestRecorderDefaultDirectoryUsesIsolatedApplicationData(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AI_TALKER_DATA_DIR", root)
	r := NewWithInput(-1, "", new(syntheticInput))
	if r.directory != filepath.Join(root, "gravacoes") {
		t.Fatalf("default directory = %q", r.directory)
	}
}
