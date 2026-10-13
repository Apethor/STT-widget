//go:build windows && cgo

package playback

/*
#cgo LDFLAGS: -lole32
#include "volume_windows.h"
*/
import "C"

import "fmt"

// These calls share Run's locked native thread with the MCI output.
type sessionVolume struct{ control *C.TalkerSessionVolume }

func openSessionVolume() (*sessionVolume, error) {
	var control *C.TalkerSessionVolume
	if code := C.talkerVolumeOpen(&control); code < 0 {
		return nil, fmt.Errorf("abrir o volume de reprodução: Windows 0x%08x", uint32(code))
	}
	return &sessionVolume{control: control}, nil
}

func (v *sessionVolume) set(volume int) error {
	if code := C.talkerVolumeSet(v.control, C.float(float32(max(0, min(100, volume)))/100)); code < 0 {
		return fmt.Errorf("ajustar o volume de reprodução: Windows 0x%08x", uint32(code))
	}
	return nil
}

func (v *sessionVolume) get() (float32, error) {
	var volume C.float
	if code := C.talkerVolumeGet(v.control, &volume); code < 0 {
		return 0, fmt.Errorf("ler o volume de reprodução: Windows 0x%08x", uint32(code))
	}
	return float32(volume), nil
}

func (v *sessionVolume) close() error {
	if v.control != nil {
		C.talkerVolumeClose(v.control)
		v.control = nil
	}
	return nil
}
