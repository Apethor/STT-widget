package platform

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	gwOwner         = 4
	wsCaption       = 0x00c00000
	wsExToolWindow  = 0x00000080
	swpNoSize       = 0x0001
	swpNoMove       = 0x0002
	swpNoActivate   = 0x0010
	swpAsyncWindow  = 0x4000
	swShow          = 5
	swRestore       = 9
	dwmUseDarkMode  = 20
	wmSysCommand    = 0x0112
	scMinimize      = 0xf020
	classNameLength = 256
	windowTitleSize = 512
)

var (
	procGetWindow             = user32.NewProc("GetWindow")
	procGetWindowLong         = user32.NewProc("GetWindowLongW")
	procGetClassName          = user32.NewProc("GetClassNameW")
	procInternalWindowText    = user32.NewProc("InternalGetWindowText")
	procIsWindowVisible       = user32.NewProc("IsWindowVisible")
	procIsIconic              = user32.NewProc("IsIconic")
	procSetWindowPos          = user32.NewProc("SetWindowPos")
	procShowWindowAsync       = user32.NewProc("ShowWindowAsync")
	procSetForegroundWindow   = user32.NewProc("SetForegroundWindow")
	procGetForegroundWindow   = user32.NewProc("GetForegroundWindow")
	procPostMessage           = user32.NewProc("PostMessageW")
	procDwmSetWindowAttribute = windows.NewLazySystemDLL("dwmapi.dll").NewProc("DwmSetWindowAttribute")

	// Register one callback for the process lifetime, without a closure that
	// would retain a different search object on every invocation.
	enumWindowCallback = sync.OnceValue(func() uintptr {
		return windows.NewCallback(enumProcessWindow)
	})
)

// windowSearch contains only scalar values. EnumWindows receives a pinned
// pointer for its synchronous call and never retains it.
type windowSearch struct {
	pid    uint32
	window windows.HWND
	rank   uint32
}

// SetPinned changes whether the current process's main window stays above other
// windows. Position, size, and keyboard focus are preserved.
func SetPinned(pinned bool) error {
	window, err := currentWindow()
	if err != nil {
		return fmt.Errorf("pin application window: %w", err)
	}

	// HWND_TOPMOST = -1 and HWND_NOTOPMOST = -2, as pointer-sized integers.
	order := ^uintptr(1)
	if pinned {
		order = ^uintptr(0)
	}
	flags := uintptr(swpNoMove | swpNoSize | swpNoActivate | swpAsyncWindow)
	result, _, callErr := procSetWindowPos.Call(
		uintptr(window),
		order,
		0,
		0,
		0,
		0,
		flags,
	)
	if result == 0 {
		return windowsCallError("pin application window", callErr)
	}
	return nil
}

// Minimize posts a native minimize request to this process's main window.
// The application, recordings and current jobs keep running.
func Minimize() error {
	window, err := currentWindow()
	if err != nil {
		return fmt.Errorf("minimize application window: %w", err)
	}
	result, _, callErr := procPostMessage.Call(uintptr(window), wmSysCommand, scMinimize, 0)
	if result == 0 {
		return windowsCallError("minimize application window", callErr)
	}
	return nil
}

// Activate shows or restores the current process's main window and requests
// foreground activation. Windows may refuse foreground access; that condition
// matches ErrForegroundDenied rather than silently reporting success.
func Activate() error {
	window, err := currentWindow()
	if err != nil {
		return fmt.Errorf("activate application window: %w", err)
	}

	showCommand := uintptr(swShow)
	minimized, _, _ := procIsIconic.Call(uintptr(window))
	if minimized != 0 {
		showCommand = swRestore
	}
	result, _, callErr := procShowWindowAsync.Call(uintptr(window), showCommand)
	if result == 0 {
		return windowsCallError("show application window", callErr)
	}

	foreground, _, _ := procGetForegroundWindow.Call()
	if foreground == uintptr(window) {
		return nil
	}
	result, _, _ = procSetForegroundWindow.Call(uintptr(window))
	if result == 0 {
		// SetForegroundWindow has no documented GetLastError result.
		return ErrForegroundDenied
	}
	return nil
}

// DarkTitlebar sets Windows' immersive dark title bar for the current
// process's main window. Call after the native window has been created.
func DarkTitlebar(dark bool) error {
	window, err := currentWindow()
	if err != nil {
		return fmt.Errorf("set dark title bar: %w", err)
	}
	if err := procDwmSetWindowAttribute.Find(); err != nil {
		return fmt.Errorf("set dark title bar: %w: %w", ErrUnsupported, err)
	}

	enabled := int32(0)
	if dark {
		enabled = 1
	}
	result, _, _ := procDwmSetWindowAttribute.Call(
		uintptr(window),
		dwmUseDarkMode,
		uintptr(unsafe.Pointer(&enabled)),
		unsafe.Sizeof(enabled),
	)
	runtime.KeepAlive(&enabled)
	if uint32(result)&0x80000000 != 0 {
		return fmt.Errorf("set dark title bar: dwm returned hresult 0x%08x", uint32(result))
	}
	return nil
}

func currentWindow() (windows.HWND, error) {
	search := &windowSearch{pid: uint32(os.Getpid())}
	var pin runtime.Pinner
	pin.Pin(search)
	defer pin.Unpin()

	err := windows.EnumWindows(enumWindowCallback(), unsafe.Pointer(search))
	runtime.KeepAlive(search)
	if err != nil {
		return 0, fmt.Errorf("enumerate application windows: %w", err)
	}
	if search.window == 0 {
		return 0, ErrWindowNotFound
	}
	return search.window, nil
}

func enumProcessWindow(window windows.HWND, search *windowSearch) uintptr {
	var pid uint32
	if _, err := windows.GetWindowThreadProcessId(window, &pid); err != nil || pid != search.pid {
		// Windows can disappear during enumeration. Windows belonging to any
		// other process are never considered or changed.
		return 1
	}

	owner, _, _ := procGetWindow.Call(uintptr(window), gwOwner)
	if owner != 0 {
		return 1
	}
	var title [windowTitleSize]uint16
	procInternalWindowText.Call(
		uintptr(window),
		uintptr(unsafe.Pointer(&title[0])),
		uintptr(len(title)),
	)
	// Read the stored caption without sending WM_GETTEXT to a busy GUI thread.
	// The PID was verified first; matching a title never selects another process.
	windowTitle := windows.UTF16ToString(title[:])
	isMainTitle := strings.HasPrefix(windowTitle, "STT-widget")

	// GWL_STYLE = -16 and GWL_EXSTYLE = -20. These flags also exclude GLFW's
	// invisible helper window. The named main window may be undecorated.
	style, _, _ := procGetWindowLong.Call(uintptr(window), ^uintptr(15))
	if style&wsCaption == 0 && !isMainTitle {
		return 1
	}
	extended, _, _ := procGetWindowLong.Call(uintptr(window), ^uintptr(19))
	if extended&wsExToolWindow != 0 {
		return 1
	}

	rank := uint32(1)
	if windowTitle == "STT-widget" {
		rank += 8
	} else if isMainTitle {
		rank += 4
	}
	var className [classNameLength]uint16
	length, _, _ := procGetClassName.Call(
		uintptr(window),
		uintptr(unsafe.Pointer(&className[0])),
		uintptr(len(className)),
	)
	if length > 0 && windows.UTF16ToString(className[:]) == "GLFW30" {
		// Fyne's native windows use GLFW. Prefer these to any other captioned
		// window within the same process, without depending on a title.
		rank += 2
	}
	visible, _, _ := procIsWindowVisible.Call(uintptr(window))
	if visible != 0 {
		rank++
	}
	if rank > search.rank {
		search.window = window
		search.rank = rank
	}
	return 1
}
