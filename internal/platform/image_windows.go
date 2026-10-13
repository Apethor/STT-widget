package platform

import (
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// OpenImage opens an existing PNG in the user's default image viewer.
func OpenImage(path string) error {
	absolute, err := imagePath(path)
	if err != nil {
		return err
	}
	encoded, err := windows.UTF16FromString(absolute)
	if err != nil {
		return err
	}
	if len(encoded) > maxWindowsPathUnits {
		return fmt.Errorf("o caminho da screenshot é muito longo")
	}
	verb := [...]uint16{'o', 'p', 'e', 'n', 0}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	err = windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED|windows.COINIT_DISABLE_OLE1DDE)
	initialized := err == nil || errors.Is(err, syscall.Errno(1))
	if initialized {
		defer windows.CoUninitialize()
	} else if !errors.Is(err, syscall.Errno(0x80010106)) {
		return fmt.Errorf("inicializar abertura da imagem: %w", err)
	}
	result, _, callErr := procShellExecute.Call(
		0, uintptr(unsafe.Pointer(&verb[0])), uintptr(unsafe.Pointer(&encoded[0])), 0, 0, 1,
	)
	runtime.KeepAlive(&verb)
	runtime.KeepAlive(encoded)
	if result <= 32 {
		return fmt.Errorf("abrir screenshot (resultado %d): %w", result, windowsCallError("ShellExecuteW", callErr))
	}
	return nil
}
