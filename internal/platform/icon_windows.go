package platform

import (
	"encoding/binary"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"ai-talker/internal/branding"

	"golang.org/x/sys/windows"
)

var (
	procCreateApplicationIcon = user32.NewProc("CreateIconFromResourceEx")
	procSendIconMessage       = user32.NewProc("SendMessageW")
	applicationIcons          = sync.OnceValues(loadApplicationIcons)
)

type nativeIcons struct{ small, large uintptr }

// These two handles remain valid for the process lifetime. Setting WM_SETICON
// again after GLFW has created the integrated frame keeps the taskbar artwork
// independent of the hidden native caption and GLFW's single-size PNG icon.
func loadApplicationIcons() (nativeIcons, error) {
	small, err := createApplicationIcon(16)
	if err != nil {
		return nativeIcons{}, err
	}
	large, err := createApplicationIcon(32)
	if err != nil {
		user32.NewProc("DestroyIcon").Call(small)
		return nativeIcons{}, err
	}
	return nativeIcons{small: small, large: large}, nil
}

func createApplicationIcon(size int) (uintptr, error) {
	ico := branding.ICO
	if len(ico) < 6 || binary.LittleEndian.Uint16(ico[2:4]) != 1 {
		return 0, errors.New("o ícone original Windows é inválido")
	}
	count := int(binary.LittleEndian.Uint16(ico[4:6]))
	if count > (len(ico)-6)/16 {
		return 0, errors.New("o catálogo do ícone original está incompleto")
	}
	for index := range count {
		entry := ico[6+index*16 : 6+(index+1)*16]
		if int(entry[0]) != size || int(entry[1]) != size {
			continue
		}
		length := uint64(binary.LittleEndian.Uint32(entry[8:12]))
		offset := uint64(binary.LittleEndian.Uint32(entry[12:16]))
		if length == 0 || offset+length > uint64(len(ico)) {
			return 0, errors.New("os pixels do ícone original estão incompletos")
		}
		pixels := ico[offset : offset+length]
		handle, _, err := procCreateApplicationIcon.Call(
			uintptr(unsafe.Pointer(&pixels[0])), uintptr(length), 1, 0x00030000,
			uintptr(size), uintptr(size), 0,
		)
		runtime.KeepAlive(pixels)
		if handle == 0 {
			return 0, windowsCallError("criar ícone original da janela", err)
		}
		return handle, nil
	}
	return 0, fmt.Errorf("o ícone original não contém a dimensão %d", size)
}

// RestoreApplicationIcon sets both native window icons from the original ICO.
// It leaves the independently rendered title artwork untouched.
func RestoreApplicationIcon() error {
	window, err := currentWindow()
	if err != nil {
		return err
	}
	return restoreWindowIcon(window)
}

func restoreWindowIcon(window windows.HWND) error {
	icons, err := applicationIcons()
	if err != nil {
		return err
	}
	const wmSetIcon, wmGetIcon = 0x0080, 0x007f
	for kind, handle := range []uintptr{icons.small, icons.large} {
		procSendIconMessage.Call(uintptr(window), wmSetIcon, uintptr(kind), handle)
		actual, _, _ := procSendIconMessage.Call(uintptr(window), wmGetIcon, uintptr(kind), 0)
		if actual != handle {
			return errors.New("o Windows não confirmou o ícone original da janela")
		}
	}
	return nil
}
