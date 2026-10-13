package platform

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// OpenFile asks the Windows shell to open a regular local file with its
// registered application. It does not invoke a command interpreter.
func OpenFile(path string) error {
	absolute, err := filepath.Abs(path)
	if err != nil || path == "" {
		return errors.New("o caminho do arquivo é inválido")
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("o arquivo não está disponível")
	}
	encoded, err := windows.UTF16FromString(absolute)
	if err != nil || len(encoded) > maxWindowsPathUnits {
		return errors.New("o caminho do arquivo excede o tamanho aceito pelo Windows")
	}
	verb := [...]uint16{'o', 'p', 'e', 'n', 0}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	err = windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED|windows.COINIT_DISABLE_OLE1DDE)
	initialized := err == nil || errors.Is(err, syscall.Errno(1))
	if initialized {
		defer windows.CoUninitialize()
	} else if !errors.Is(err, syscall.Errno(0x80010106)) {
		return fmt.Errorf("inicializar a abertura do arquivo: %w", err)
	}
	result, _, callErr := procShellExecute.Call(
		0, uintptr(unsafe.Pointer(&verb[0])), uintptr(unsafe.Pointer(&encoded[0])), 0, 0, 1,
	)
	runtime.KeepAlive(&verb)
	runtime.KeepAlive(encoded)
	if result <= 32 {
		return fmt.Errorf("abrir o arquivo (resultado %d): %w", result, windowsCallError("ShellExecuteW", callErr))
	}
	return nil
}
