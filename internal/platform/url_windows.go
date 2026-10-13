package platform

import (
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// OpenURL asks the user's default browser to open a web page via the Windows
// shell, without a console or a command interpreter.
func OpenURL(address string) error {
	target, err := WebURL(address)
	if err != nil {
		return err
	}
	encoded, err := windows.UTF16FromString(target.String())
	if err != nil {
		return fmt.Errorf("preparar o endereço: %w", err)
	}
	if len(encoded) > maxWindowsPathUnits {
		return errors.New("o endereço excede o tamanho aceito pelo Windows")
	}
	verb := [...]uint16{'o', 'p', 'e', 'n', 0}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	err = windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED|windows.COINIT_DISABLE_OLE1DDE)
	initialized := err == nil || errors.Is(err, syscall.Errno(1))
	if initialized {
		defer windows.CoUninitialize()
	} else if !errors.Is(err, syscall.Errno(0x80010106)) {
		return fmt.Errorf("inicializar abertura do navegador: %w", err)
	}
	result, _, callErr := procShellExecute.Call(
		0, uintptr(unsafe.Pointer(&verb[0])), uintptr(unsafe.Pointer(&encoded[0])), 0, 0, 1,
	)
	runtime.KeepAlive(&verb)
	runtime.KeepAlive(encoded)
	if result <= 32 {
		return fmt.Errorf("abrir página no navegador (resultado %d): %w", result, windowsCallError("ShellExecuteW", callErr))
	}
	return nil
}
