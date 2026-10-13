//go:build windows && cgo

package pcmoutput

/*
#cgo LDFLAGS: -lole32 -luuid
#include "output_windows.h"
*/
import "C"

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

type nativeDevice struct{ handle *C.TalkerResponseOutput }

func responseError(hr C.HRESULT) error {
	if int32(hr) < 0 {
		return fmt.Errorf("reproduzir resposta WASAPI: HRESULT 0x%08x", uint32(hr))
	}
	return nil
}

func openDevice(endpoint string, rate int) (device, error) {
	encoded, err := windows.UTF16FromString(endpoint)
	if err != nil {
		return nil, err
	}
	var handle *C.TalkerResponseOutput
	err = responseError(C.talkerResponseOpen((*C.wchar_t)(unsafe.Pointer(&encoded[0])), C.int(rate), &handle))
	if err != nil {
		return nil, err
	}
	return &nativeDevice{handle: handle}, nil
}

func (d *nativeDevice) Write(pcm []byte) (int, error) {
	if len(pcm) == 0 {
		return 0, nil
	}
	var written C.int
	err := responseError(C.talkerResponseWrite(d.handle, (*C.BYTE)(unsafe.Pointer(&pcm[0])), C.int(len(pcm)), &written))
	return int(written), err
}

func (d *nativeDevice) Reset() error { return responseError(C.talkerResponseReset(d.handle)) }
func (d *nativeDevice) Pending() (int, error) {
	var bytes C.int
	err := responseError(C.talkerResponsePending(d.handle, &bytes))
	return int(bytes), err
}
func (d *nativeDevice) Pause(paused bool) error {
	flag := C.int(0)
	if paused {
		flag = 1
	}
	return responseError(C.talkerResponsePause(d.handle, flag))
}
func (d *nativeDevice) Close() error {
	handle := d.handle
	d.handle = nil
	return responseError(C.talkerResponseClose(handle))
}
