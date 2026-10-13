package platform

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const maxWindowsPathUnits = 32768

var procShellExecute = windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteW")

// OpenFolder opens an existing directory using ShellExecuteW, without launching
// a console or interpreting the path as shell command text.
// Callers should run this outside the UI loop.
func OpenFolder(path string) error {
	absolute, err := folderPath(path)
	if err != nil {
		return err
	}
	file, err := windows.UTF16FromString(absolute)
	if err != nil {
		return fmt.Errorf("encode folder path: %w", err)
	}
	if len(file) > maxWindowsPathUnits {
		return fmt.Errorf("open folder: path exceeds %d utf-16 units: %w", maxWindowsPathUnits-1, os.ErrInvalid)
	}
	verb := [...]uint16{'o', 'p', 'e', 'n', 0}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Shell extensions may need COM. Both S_OK and S_FALSE require a matching
	// CoUninitialize; RPC_E_CHANGED_MODE means this thread already uses another
	// COM apartment, which must not be uninitialized by this operation.
	err = windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED|windows.COINIT_DISABLE_OLE1DDE)
	initialized := err == nil || errors.Is(err, syscall.Errno(1))
	if initialized {
		defer windows.CoUninitialize()
	} else if !errors.Is(err, syscall.Errno(0x80010106)) {
		return fmt.Errorf("initialize shell com: %w", err)
	}

	result, _, callErr := procShellExecute.Call(
		0,
		uintptr(unsafe.Pointer(&verb[0])),
		uintptr(unsafe.Pointer(&file[0])),
		0,
		0,
		1, // SW_SHOWNORMAL.
	)
	runtime.KeepAlive(file)
	runtime.KeepAlive(&verb)
	if result <= 32 {
		return fmt.Errorf(
			"open folder %q (shell result %d): %w",
			absolute,
			result,
			windowsCallError("shell execute", callErr),
		)
	}
	return nil
}
