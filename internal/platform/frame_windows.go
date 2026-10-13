package platform

/*
#cgo LDFLAGS: -ldwmapi -luser32
#undef _WIN32_WINNT
#define _WIN32_WINNT 0x0A00
#define WIN32_LEAN_AND_MEAN
#include <stdint.h>
#include <windows.h>
#include <windowsx.h>
#include <dwmapi.h>

// The window owns this native allocation until WM_NCDESTROY. Neither the
// property nor the WNDPROC callback stores a pointer into Go memory.
typedef struct {
    WNDPROC previous;
    DWORD originalStyle;
    UINT dpi;
    int top, bottom, controls;
} TalkerFrame;
static const wchar_t frameProperty[] = L"AI-Talker.IntegratedFrame";
static const UINT frameHeaderMessage = WM_APP + 0x41f;

static int frameHitTest(int x, int y, int width, int height, int border,
                        int topCaption, int bottomCaption, int controls) {
    if (x < 0 || y < 0 || x >= width || y >= height) return HTNOWHERE;
    int left = x < border, right = x >= width - border;
    int top = y < border, bottom = y >= height - border;
    if (top && left) return HTTOPLEFT;
    if (top && right) return HTTOPRIGHT;
    if (bottom && left) return HTBOTTOMLEFT;
    if (bottom && right) return HTBOTTOMRIGHT;
    if (top) return HTTOP;
    if (bottom) return HTBOTTOM;
    if (left) return HTLEFT;
    if (right) return HTRIGHT;
    if (y >= topCaption && y < bottomCaption && x < width - controls) return HTCAPTION;
    return HTCLIENT;
}

static int frameBorder(HWND window, UINT metric) {
    UINT dpi = GetDpiForWindow(window);
    return GetSystemMetricsForDpi(metric, dpi) +
           GetSystemMetricsForDpi(SM_CXPADDEDBORDER, dpi);
}

static LRESULT CALLBACK frameWindowProc(HWND window, UINT message, WPARAM wparam, LPARAM lparam) {
    TalkerFrame *frame = (TalkerFrame *) GetPropW(window, frameProperty);
    if (!frame) return DefWindowProcW(window, message, wparam, lparam);
    WNDPROC previous = frame->previous;

    if (message == frameHeaderMessage) {
        // Scalar message parameters keep pointers out of the window protocol.
        // Only the owning native thread updates the hit-test geometry.
        int top = (int) (wparam & 0xffff);
        int bottom = (int) ((wparam >> 16) & 0xffff);
        if (bottom <= top || lparam < 0 || lparam > 0xffff) return 0;
        frame->top = top;
        frame->bottom = bottom;
        frame->controls = (int) lparam;
        frame->dpi = GetDpiForWindow(window);
        return 1;
    }
    if (message == WM_NCDESTROY) {
        SetWindowLongPtrW(window, GWLP_WNDPROC, (LONG_PTR) previous);
        RemovePropW(window, frameProperty);
        HeapFree(GetProcessHeap(), 0, frame);
        return CallWindowProcW(previous, window, message, wparam, lparam);
    }
    if (message == WM_NCCALCSIZE) {
        // Returning zero extends the client over the old caption, so DWM
        // stops drawing its duplicate title and small application icon.
        // Undecorated GLFW windows already maximize to the monitor's work
        // area. Insetting them again makes each driver resize smaller.
        if (IsZoomed(window) && (frame->originalStyle & WS_CAPTION)) {
            RECT *rect = wparam ? &((NCCALCSIZE_PARAMS *) lparam)->rgrc[0] : (RECT *) lparam;
            int x = frameBorder(window, SM_CXSIZEFRAME);
            int y = frameBorder(window, SM_CYSIZEFRAME);
            rect->left += x; rect->right -= x;
            rect->top += y; rect->bottom -= y;
        }
        return 0;
    }
    if (message == WM_GETMINMAXINFO) {
        LRESULT result = CallWindowProcW(previous, window, message, wparam, lparam);
        // GLFW's size limits include its former non-client frame. Our client
        // covers that frame, so subtract only those old non-client dimensions.
        RECT rect = {0};
        DWORD extended = (DWORD) GetWindowLongPtrW(window, GWL_EXSTYLE);
        if (AdjustWindowRectExForDpi(&rect, frame->originalStyle, FALSE, extended, GetDpiForWindow(window))) {
            MINMAXINFO *info = (MINMAXINFO *) lparam;
            info->ptMinTrackSize.x = max(1, info->ptMinTrackSize.x - (rect.right - rect.left));
            info->ptMinTrackSize.y = max(1, info->ptMinTrackSize.y - (rect.bottom - rect.top));
        }
        return result;
    }
    LRESULT result = 0;
    // Caption actions are real canvas buttons. DWM's old invisible hit targets
    // must not intercept them; the title alone retains native drag/double-click.
    if (message == WM_NCHITTEST) {
        POINT point = { GET_X_LPARAM(lparam), GET_Y_LPARAM(lparam) };
        RECT rect;
        if (ScreenToClient(window, &point) && GetClientRect(window, &rect)) {
            int border = IsZoomed(window) ? 0 : frameBorder(window, SM_CXSIZEFRAME);
            UINT dpi = GetDpiForWindow(window);
            int top = MulDiv(frame->top, dpi, frame->dpi);
            int bottom = MulDiv(frame->bottom, dpi, frame->dpi);
            int controls = MulDiv(frame->controls, dpi, frame->dpi);
            result = frameHitTest(point.x, point.y, rect.right, rect.bottom, border, top, bottom, controls);
            if (result != HTNOWHERE) return result;
        }
    }
    return CallWindowProcW(previous, window, message, wparam, lparam);
}

static DWORD installFrame(uintptr_t handle, int top, int bottom, int controls) {
    HWND window = (HWND) handle;
    if (top < 0 || bottom <= top || bottom > 0xffff || controls < 0 || controls > 0xffff) {
        return ERROR_INVALID_PARAMETER;
    }
    DWORD pid = 0;
    GetWindowThreadProcessId(window, &pid);
    if (pid != GetCurrentProcessId()) return ERROR_ACCESS_DENIED;
    if (GetPropW(window, frameProperty)) {
        WPARAM bounds = (WPARAM) top | ((WPARAM) bottom << 16);
        return SendMessageW(window, frameHeaderMessage, bounds, (LPARAM) controls) == 1
            ? ERROR_SUCCESS : ERROR_INVALID_WINDOW_HANDLE;
    }
    RECT client;
    if (!GetClientRect(window, &client)) return GetLastError();
    TalkerFrame *frame = (TalkerFrame *) HeapAlloc(GetProcessHeap(), HEAP_ZERO_MEMORY, sizeof(TalkerFrame));
    if (!frame) return ERROR_NOT_ENOUGH_MEMORY;
    frame->previous = (WNDPROC) GetWindowLongPtrW(window, GWLP_WNDPROC);
    frame->originalStyle = (DWORD) GetWindowLongPtrW(window, GWL_STYLE);
    frame->top = top; frame->bottom = bottom; frame->controls = controls;
    frame->dpi = GetDpiForWindow(window);
    if (!frame->previous || !SetPropW(window, frameProperty, frame)) {
        DWORD error = GetLastError();
        HeapFree(GetProcessHeap(), 0, frame);
        return error ? error : ERROR_INVALID_WINDOW_HANDLE;
    }
    SetLastError(ERROR_SUCCESS);
    if (!SetWindowLongPtrW(window, GWLP_WNDPROC, (LONG_PTR) frameWindowProc)) {
        DWORD error = GetLastError();
        RemovePropW(window, frameProperty);
        HeapFree(GetProcessHeap(), 0, frame);
        return error ? error : ERROR_INVALID_WINDOW_HANDLE;
    }
    SetLastError(ERROR_SUCCESS);
    // GLFW creates the window undecorated so its client-size calculations
    // match this full-client frame. Restore native resizing, snapping and
    // caption commands without introducing a painted non-client caption.
    DWORD integratedStyle = (frame->originalStyle & ~WS_CAPTION) |
        WS_THICKFRAME | WS_MAXIMIZEBOX | WS_MINIMIZEBOX | WS_SYSMENU;
    LONG_PTR previousStyle = SetWindowLongPtrW(window, GWL_STYLE, (LONG_PTR) integratedStyle);
    DWORD styleError = !previousStyle ? GetLastError() : ERROR_SUCCESS;
    MARGINS margins = {0};
    HRESULT extended = DwmExtendFrameIntoClientArea(window, &margins);
    if (styleError || FAILED(extended) ||
        !SetWindowPos(window, NULL, 0, 0, client.right, client.bottom,
                      SWP_NOMOVE | SWP_NOZORDER | SWP_NOACTIVATE | SWP_FRAMECHANGED | SWP_ASYNCWINDOWPOS)) {
        DWORD error = styleError ? styleError : FAILED(extended) ? ERROR_NOT_SUPPORTED : GetLastError();
        SetWindowLongPtrW(window, GWLP_WNDPROC, (LONG_PTR) frame->previous);
        SetWindowLongPtrW(window, GWL_STYLE, (LONG_PTR) frame->originalStyle);
        RemovePropW(window, frameProperty);
        HeapFree(GetProcessHeap(), 0, frame);
        return error;
    }
    return ERROR_SUCCESS;
}
*/
import "C"

import (
	"errors"
	"fmt"
	"image/color"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var frameMu sync.Mutex

// IntegratedTitlebar removes the duplicate native caption. Canvas controls stay
// clickable; native dragging, double-click maximization and resizing remain.
func IntegratedTitlebar(dark bool, background color.NRGBA, header WindowHeader) error {
	frameMu.Lock()
	defer frameMu.Unlock()
	window, err := currentWindow()
	if err != nil {
		return fmt.Errorf("integrate application frame: %w", err)
	}
	return integrateWindowFrame(window, dark, background, header)
}

func integrateWindowFrame(window windows.HWND, dark bool, background color.NRGBA, header WindowHeader) error {
	// Cosmetic DWM attributes are optional on Windows 10. They must not prevent
	// the custom frame from being installed when newer attributes are absent.
	var decorationError error
	enabled := uint32(0)
	if dark {
		enabled = 1
	}
	// The stored title and icon remain available to the taskbar, screen readers
	// and instance detection even though the caption is not painted.
	tint := uint32(background.R) | uint32(background.G)<<8 | uint32(background.B)<<16
	border := uint32(0xfffffffe) // DWMWA_COLOR_NONE
	for _, attribute := range []struct {
		name  uintptr
		value *uint32
	}{{20, &enabled}, {35, &tint}, {34, &border}} {
		result, _, _ := procDwmSetWindowAttribute.Call(
			uintptr(window), attribute.name, uintptr(unsafe.Pointer(attribute.value)), 4,
		)
		runtime.KeepAlive(attribute.value)
		if uint32(result)&0x80000000 != 0 {
			decorationError = fmt.Errorf("integrate application frame: dwm returned hresult 0x%08x", uint32(result))
		}
	}
	if result := C.installFrame(C.uintptr_t(window), C.int(header.Top), C.int(header.Bottom), C.int(header.ControlsWidth)); result != 0 {
		return fmt.Errorf("integrate application frame: %w", syscall.Errno(result))
	}
	return errors.Join(decorationError, restoreWindowIcon(window))
}

func frameHitTest(x, y, width, height, border int, header WindowHeader) int {
	return int(C.frameHitTest(C.int(x), C.int(y), C.int(width), C.int(height), C.int(border),
		C.int(header.Top), C.int(header.Bottom), C.int(header.ControlsWidth)))
}
