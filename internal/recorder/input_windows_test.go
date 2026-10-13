package recorder

import (
	"bytes"
	"reflect"
	"testing"
	"unsafe"
)

func TestWinMMSystemAPIAvailableWithoutOpeningMicrophone(t *testing.T) {
	t.Parallel()
	if err := loadWinMM(); err != nil {
		t.Fatal(err)
	}
}

func TestWinMMABIStructuresContainNoGoPointers(t *testing.T) {
	t.Parallel()
	for _, typ := range []reflect.Type{
		reflect.TypeFor[waveHeader](),
		reflect.TypeFor[waveFormat](),
		reflect.TypeFor[waveInCaps](),
	} {
		for field := range typ.NumField() {
			kind := typ.Field(field).Type.Kind()
			if kind == reflect.Pointer || kind == reflect.UnsafePointer || kind == reflect.Slice ||
				kind == reflect.Map || kind == reflect.String || kind == reflect.Interface {
				t.Fatalf("%s.%s contains a Go pointer", typ.Name(), typ.Field(field).Name)
			}
		}
	}
	wantHeaderSize := uintptr(48)
	wantFlagsOffset := uintptr(24)
	if unsafe.Sizeof(uintptr(0)) == 4 {
		wantHeaderSize = 32
		wantFlagsOffset = 16
	}
	if unsafe.Sizeof(waveHeader{}) != wantHeaderSize || unsafe.Offsetof(waveHeader{}.Flags) != wantFlagsOffset {
		t.Fatal("WAVEHDR layout differs from the Win32 ABI")
	}
	if unsafe.Sizeof(waveInCaps{}) != 80 ||
		unsafe.Offsetof(waveFormat{}.SamplesPerSec) != 4 ||
		unsafe.Offsetof(waveFormat{}.ExtraSize) != 16 {
		t.Fatal("WAVEINCAPSW or WAVEFORMATEX layout differs from the Win32 ABI")
	}
}

func TestWinMMReturnedBuffersKeepQueueOrderAcrossWrap(t *testing.T) {
	t.Parallel()
	s := &winMMSession{next: 2}
	for index := range 4 {
		s.buffers = append(s.buffers, &nativeBuffer{
			header: &waveHeader{Flags: waveHeaderDone, BytesRecorded: 2},
			data:   pcmSamples(int16(index + 1)),
			queued: true,
		})
	}
	var received []byte
	err := s.deliverReady(func(pcm []byte, err error) {
		if err != nil {
			t.Fatal(err)
		}
		received = append(received, pcm...)
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(received, pcmSamples(3, 4, 1, 2)) {
		t.Fatalf("coalesced notifications reordered PCM: %x", received)
	}
	for _, buffer := range s.buffers {
		if buffer.queued {
			t.Fatal("completed buffer was delivered twice or left pending")
		}
	}
}

func TestWinMMRejectsInvalidReturnedBufferLengths(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		size uint32
	}{
		{name: "partial sample", size: 1},
		{name: "outside pinned buffer", size: 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := &winMMSession{buffers: []*nativeBuffer{{
				header: &waveHeader{Flags: waveHeaderDone, BytesRecorded: tc.size},
				data:   make([]byte, 2),
				queued: true,
			}}}
			err := s.deliverReady(func([]byte, error) {
				t.Fatal("invalid native bytes were delivered")
			}, false)
			if err == nil {
				t.Fatal("invalid native length was silently accepted")
			}
		})
	}
}
