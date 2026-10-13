package platform

import (
	"fmt"
	"image/color"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

func auxiliaryWindow(title string) (windows.HWND, error) {
	encoded, err := windows.UTF16FromString(title)
	if err != nil || len(encoded) > windowTitleSize {
		return 0, ErrWindowNotFound
	}
	search := &ownedWindowSearch{pid: uint32(os.Getpid())}
	copy(search.title[:], encoded)
	var pin runtime.Pinner
	pin.Pin(search)
	defer pin.Unpin()
	err = windows.EnumWindows(ownerCallback(), unsafe.Pointer(search))
	runtime.KeepAlive(search)
	if err != nil {
		return 0, err
	}
	if search.window == 0 {
		return 0, ErrWindowNotFound
	}
	return search.window, nil
}

// AuxiliaryTitlebar targets an exact title in the current process. It cannot
// accidentally change the main window or another instance's soundboard.
func AuxiliaryTitlebar(title string, dark bool, background color.NRGBA, header WindowHeader) error {
	window, err := auxiliaryWindow(title)
	if err != nil {
		return err
	}
	if header.Top < 0 || header.Bottom <= header.Top || header.Bottom > 0xffff ||
		header.ControlsWidth < 0 || header.ControlsWidth > 0xffff {
		return windows.ERROR_INVALID_PARAMETER
	}
	err = integrateWindowFrame(window, dark, background, header)
	if err == nil {
		return nil
	}
	// Older Windows 10 builds reject optional DWM colors after the native
	// frame is installed. Confirm its window-owned property and icons so
	// that unsupported decoration cannot prevent restoring its position.
	property, _ := windows.UTF16PtrFromString("AI-Talker.IntegratedFrame")
	frame, _, _ := user32.NewProc("GetPropW").Call(uintptr(window), uintptr(unsafe.Pointer(property)))
	runtime.KeepAlive(property)
	if frame == 0 {
		return err
	}
	return restoreWindowIcon(window)
}

func AuxiliaryMinimize(title string) error {
	window, err := auxiliaryWindow(title)
	if err != nil {
		return err
	}
	result, _, callErr := procPostMessage.Call(uintptr(window), wmSysCommand, scMinimize, 0)
	if result == 0 {
		return windowsCallError("minimizar painel de sons", callErr)
	}
	return nil
}

func AuxiliaryToggleMaximized(title string) error {
	window, err := auxiliaryWindow(title)
	if err != nil {
		return err
	}
	maximized, _, _ := user32.NewProc("IsZoomed").Call(uintptr(window))
	command := uintptr(0xf030) // SC_MAXIMIZE
	if maximized != 0 {
		command = 0xf120 // SC_RESTORE
	}
	result, _, callErr := procPostMessage.Call(uintptr(window), wmSysCommand, command, 0)
	if result == 0 {
		return windowsCallError("maximizar ou restaurar painel de sons", callErr)
	}
	return nil
}

func AuxiliaryWindowGeometry(title string) (AuxiliaryGeometry, error) {
	window, err := auxiliaryWindow(title)
	if err != nil {
		return AuxiliaryGeometry{}, err
	}
	var rectangle struct{ Left, Top, Right, Bottom int32 }
	result, _, callErr := user32.NewProc("GetWindowRect").Call(uintptr(window), uintptr(unsafe.Pointer(&rectangle)))
	runtime.KeepAlive(&rectangle)
	if result == 0 {
		return AuxiliaryGeometry{}, windowsCallError("ler posição do painel de sons", callErr)
	}
	maximized, _, _ := user32.NewProc("IsZoomed").Call(uintptr(window))
	minimized, _, _ := procIsIconic.Call(uintptr(window))
	return AuxiliaryGeometry{
		X: int(rectangle.Left), Y: int(rectangle.Top),
		Maximized: maximized != 0, Minimized: minimized != 0,
	}, nil
}

func PositionAuxiliaryWindow(title string, x, y int) error {
	window, err := auxiliaryWindow(title)
	if err != nil {
		return err
	}
	var placement struct{ Left, Top, Right, Bottom int32 }
	result, _, callErr := user32.NewProc("GetWindowRect").Call(uintptr(window), uintptr(unsafe.Pointer(&placement)))
	if result == 0 {
		return windowsCallError("ler tamanho do painel de sons", callErr)
	}
	width, height := placement.Right-placement.Left, placement.Bottom-placement.Top
	placement.Left, placement.Top = int32(x), int32(y)
	placement.Right, placement.Bottom = int32(x)+width, int32(y)+height
	const monitorDefaultToNearest = 2
	monitor, _, _ := user32.NewProc("MonitorFromRect").Call(uintptr(unsafe.Pointer(&placement)), monitorDefaultToNearest)
	var info struct {
		Size    uint32
		Monitor struct{ Left, Top, Right, Bottom int32 }
		Work    struct{ Left, Top, Right, Bottom int32 }
		Flags   uint32
	}
	info.Size = uint32(unsafe.Sizeof(info))
	result, _, callErr = user32.NewProc("GetMonitorInfoW").Call(monitor, uintptr(unsafe.Pointer(&info)))
	if result == 0 {
		return windowsCallError("localizar monitor do painel de sons", callErr)
	}
	// A disconnected monitor's old coordinates are brought back onto the
	// nearest work area, preserving negative positions on existing displays.
	x = max(int(info.Work.Left), min(x, int(info.Work.Right)-min(int(width), int(info.Work.Right-info.Work.Left))))
	y = max(int(info.Work.Top), min(y, int(info.Work.Bottom)-min(int(height), int(info.Work.Bottom-info.Work.Top))))
	result, _, callErr = procSetWindowPos.Call(
		uintptr(window), 0, uintptr(x), uintptr(y), 0, 0,
		swpNoSize|swpNoActivate|swpAsyncWindow|0x0004,
	)
	runtime.KeepAlive(&placement)
	runtime.KeepAlive(&info)
	if result == 0 {
		return fmt.Errorf("posicionar painel de sons: %w", callErr)
	}
	return nil
}
