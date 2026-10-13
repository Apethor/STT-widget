package platform

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	ownerCallback      = sync.OnceValue(func() uintptr { return windows.NewCallback(enumOwnedWindow) })
	procSetWindowOwner = user32.NewProc("SetWindowLongPtrW")
)

type ownedWindowSearch struct {
	pid    uint32
	title  [windowTitleSize]uint16
	window windows.HWND
}

// OwnWindow keeps an auxiliary window above this process's main window,
// including when the main window is pinned. It never selects another process.
func OwnWindow(title string) error {
	main, err := currentWindow()
	if err != nil {
		return err
	}
	encoded, err := windows.UTF16FromString(title)
	if err != nil || len(encoded) > windowTitleSize {
		return fmt.Errorf("identificar janela auxiliar: %w", ErrWindowNotFound)
	}
	search := &ownedWindowSearch{pid: uint32(os.Getpid())}
	copy(search.title[:], encoded)
	var pin runtime.Pinner
	pin.Pin(search)
	defer pin.Unpin()
	err = windows.EnumWindows(ownerCallback(), unsafe.Pointer(search))
	runtime.KeepAlive(search)
	if err != nil {
		return fmt.Errorf("localizar janela auxiliar: %w", err)
	}
	if search.window == 0 {
		return ErrWindowNotFound
	}
	if search.window == main {
		return nil
	}
	// GWLP_HWNDPARENT = -8 changes the owner of a top-level window.
	procSetWindowOwner.Call(uintptr(search.window), ^uintptr(7), uintptr(main))
	owner, _, _ := procGetWindow.Call(uintptr(search.window), gwOwner)
	if owner != uintptr(main) {
		return fmt.Errorf("associar janela auxiliar: %w", ErrWindowNotFound)
	}
	result, _, callErr := procSetWindowPos.Call(uintptr(search.window), 0, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate)
	if result == 0 {
		return windowsCallError("posicionar janela auxiliar", callErr)
	}
	return nil
}

func enumOwnedWindow(window windows.HWND, search *ownedWindowSearch) uintptr {
	// Complete enumeration normally: stopping via a zero callback return
	// makes EnumWindows report failure with an unspecified last-error value.
	if search.window != 0 {
		return 1
	}
	var pid uint32
	if _, err := windows.GetWindowThreadProcessId(window, &pid); err != nil || pid != search.pid {
		return 1
	}
	var title [windowTitleSize]uint16
	procInternalWindowText.Call(uintptr(window), uintptr(unsafe.Pointer(&title[0])), uintptr(len(title)))
	if title == search.title {
		search.window = window
	}
	return 1
}
