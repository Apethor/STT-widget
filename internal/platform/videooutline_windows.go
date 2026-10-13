package platform

/*
#cgo LDFLAGS: -lgdi32 -luser32
#undef _WIN32_WINNT
#define _WIN32_WINNT 0x0A00
#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#include <stdint.h>

#define TALKER_OUTLINE_PAUSE (WM_APP + 0x171)

static int talkerOutlineThickness(UINT dpi) {
    return max(1, MulDiv(1, dpi ? dpi : 96, 96));
}

static LRESULT CALLBACK talkerOutlineProc(HWND window, UINT message, WPARAM wp, LPARAM lp) {
    switch (message) {
    case WM_NCHITTEST: return HTTRANSPARENT;
    case WM_MOUSEACTIVATE: return MA_NOACTIVATE;
    case WM_ERASEBKGND: return 1;
    case TALKER_OUTLINE_PAUSE:
        SetWindowLongPtrW(window, GWLP_USERDATA, wp ? 1 : 0);
        InvalidateRect(window, NULL, FALSE);
        UpdateWindow(window);
        return 0;
    case WM_PAINT: {
        PAINTSTRUCT paint;
        HDC dc = BeginPaint(window, &paint);
        RECT client;
        GetClientRect(window, &client);
        COLORREF color = GetWindowLongPtrW(window, GWLP_USERDATA)
            ? RGB(145, 145, 145) : RGB(166, 92, 255);
        HBRUSH brush = CreateSolidBrush(color);
        FillRect(dc, &client, brush);
        DeleteObject(brush);
        EndPaint(window, &paint);
        return 0;
    }
    }
    return DefWindowProcW(window, message, wp, lp);
}

static uintptr_t talkerOutlineCreate(int x, int y, int width, int height, DWORD *error) {
    *error = ERROR_SUCCESS;
    RECT selected = {x, y, x+width, y+height};
    RECT bounds = {
        GetSystemMetrics(SM_XVIRTUALSCREEN), GetSystemMetrics(SM_YVIRTUALSCREEN),
        GetSystemMetrics(SM_XVIRTUALSCREEN) + GetSystemMetrics(SM_CXVIRTUALSCREEN),
        GetSystemMetrics(SM_YVIRTUALSCREEN) + GetSystemMetrics(SM_CYVIRTUALSCREEN)
    };
    MONITORINFO monitor = {0};
    monitor.cbSize = sizeof(monitor);
    if (GetMonitorInfoW(MonitorFromRect(&selected, MONITOR_DEFAULTTONEAREST), &monitor) &&
        selected.left >= monitor.rcMonitor.left && selected.top >= monitor.rcMonitor.top &&
        selected.right <= monitor.rcMonitor.right && selected.bottom <= monitor.rcMonitor.bottom)
        bounds = monitor.rcMonitor;
    int left = max(bounds.left, x-1), top = max(bounds.top, y-1);
    int right = min(bounds.right, x+width+1), bottom = min(bounds.bottom, y+height+1);
    int outside = left == x-1 && top == y-1 && right == x+width+1 && bottom == y+height+1;
    int outerWidth = right-left, outerHeight = bottom-top;
    if (outerWidth < 2 || outerHeight < 2) { *error = ERROR_INVALID_PARAMETER; return 0; }
    HINSTANCE instance = GetModuleHandleW(NULL);
    WNDCLASSEXW cls = {0};
    cls.cbSize = sizeof(cls);
    cls.hInstance = instance;
    cls.lpszClassName = L"AI-Talker.VideoOutline";
    cls.lpfnWndProc = talkerOutlineProc;
    if (!RegisterClassExW(&cls) && GetLastError() != ERROR_CLASS_ALREADY_EXISTS) {
        *error = GetLastError();
        return 0;
    }
    HWND window = CreateWindowExW(
        WS_EX_TOPMOST | WS_EX_TOOLWINDOW | WS_EX_NOACTIVATE | WS_EX_TRANSPARENT | WS_EX_LAYERED,
        cls.lpszClassName, L"STT-widget video outline", WS_POPUP,
        left, top, outerWidth, outerHeight, NULL, NULL, instance, NULL);
    if (!window) { *error = GetLastError(); return 0; }
    int thickness = talkerOutlineThickness(GetDpiForWindow(window));
    left = max(bounds.left, x-thickness);
    top = max(bounds.top, y-thickness);
    right = min(bounds.right, x+width+thickness);
    bottom = min(bounds.bottom, y+height+thickness);
    outside = left == x-thickness && top == y-thickness &&
        right == x+width+thickness && bottom == y+height+thickness;
    outerWidth = right-left;
    outerHeight = bottom-top;
    if (!SetWindowPos(window, NULL, left, top, outerWidth, outerHeight, SWP_NOACTIVATE | SWP_NOZORDER)) {
        *error = GetLastError();
        DestroyWindow(window);
        return 0;
    }
    if (!outside) {
        // Before Windows 10 2004, EXCLUDEFROMCAPTURE behaves like MONITOR
        // and can replace an in-frame border with black pixels.
        typedef LONG (WINAPI *TalkerVersion)(OSVERSIONINFOW *);
        TalkerVersion version = (TalkerVersion) GetProcAddress(GetModuleHandleW(L"ntdll.dll"), "RtlGetVersion");
        OSVERSIONINFOW info = {0};
        info.dwOSVersionInfoSize = sizeof(info);
        if (!version || version(&info) != 0 || info.dwBuildNumber < 19041) {
            *error = ERROR_NOT_SUPPORTED;
            DestroyWindow(window);
            return 0;
        }
    }
    HRGN outer = CreateRectRgn(0, 0, outerWidth, outerHeight);
    HRGN inner = CreateRectRgn(thickness, thickness, outerWidth-thickness, outerHeight-thickness);
    if (!outer || !inner || CombineRgn(outer, outer, inner, RGN_DIFF) == ERROR ||
        !SetWindowRgn(window, outer, FALSE)) {
        *error = ERROR_NOT_ENOUGH_MEMORY;
        if (outer) DeleteObject(outer);
        if (inner) DeleteObject(inner);
        DestroyWindow(window);
        return 0;
    }
    // SetWindowRgn owns outer after success. Normal recrops put every edge
    // outside the selected pixels. Screen edges keep one logical pixel
    // inside, requiring verified Windows capture exclusion.
    DeleteObject(inner);
    if (!SetLayeredWindowAttributes(window, 0, 255, LWA_ALPHA)) {
        *error = GetLastError();
        DestroyWindow(window);
        return 0;
    }
    if (!SetWindowDisplayAffinity(window, 0x00000011) && !outside) {
        *error = GetLastError();
        DestroyWindow(window);
        return 0;
    }
    ShowWindow(window, SW_SHOWNOACTIVATE);
    UpdateWindow(window);
    return (uintptr_t) window;
}

static DWORD talkerOutlineRun(uintptr_t target, uintptr_t cancel) {
    HWND window = (HWND) target;
    HANDLE event = (HANDLE) cancel;
    DWORD error = ERROR_SUCCESS;
    for (;;) {
        DWORD wait = MsgWaitForMultipleObjectsEx(1, &event, INFINITE, QS_ALLINPUT, MWMO_INPUTAVAILABLE);
        if (wait == WAIT_OBJECT_0) break;
        if (wait == WAIT_FAILED) { error = GetLastError(); break; }
        MSG message;
        while (PeekMessageW(&message, NULL, 0, 0, PM_REMOVE)) {
            TranslateMessage(&message);
            DispatchMessageW(&message);
        }
    }
    DestroyWindow(window);
    return error;
}

static int talkerOutlinePaused(uintptr_t target, int paused) {
    return PostMessageW((HWND) target, TALKER_OUTLINE_PAUSE, paused ? 1 : 0, 0);
}
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"syscall"

	"golang.org/x/sys/windows"
)

type nativeVideoOutline struct {
	mu     sync.Mutex
	window uintptr
	cancel windows.Handle
	done   chan struct{}
	err    error
}

func videoOutlineThickness(dpi uint32) int {
	return int(C.talkerOutlineThickness(C.UINT(dpi)))
}

func NewVideoOutline(ctx context.Context, region ScreenRegion) (VideoOutline, error) {
	if err := validateVideoOutline(ctx, region); err != nil {
		return nil, err
	}
	cancel, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return nil, fmt.Errorf("preparar a borda de vídeo: %w", err)
	}
	outline := &nativeVideoOutline{cancel: cancel, done: make(chan struct{})}
	ready := make(chan error, 1)
	go outline.run(ctx, region, ready)
	select {
	case err := <-ready:
		if err != nil {
			_ = outline.Close()
			return nil, err
		}
		return outline, nil
	case <-ctx.Done():
		_ = outline.Close()
		return nil, ctx.Err()
	}
}

func (o *nativeVideoOutline) run(ctx context.Context, region ScreenRegion, ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	previousDPI, _, _ := user32.NewProc("SetThreadDpiAwarenessContext").Call(^uintptr(3))
	if previousDPI != 0 {
		defer user32.NewProc("SetThreadDpiAwarenessContext").Call(previousDPI)
	}
	stop := context.AfterFunc(ctx, o.requestClose)
	defer stop()
	defer func() {
		o.mu.Lock()
		o.window = 0
		_ = windows.CloseHandle(o.cancel)
		o.cancel = 0
		o.mu.Unlock()
		close(o.done)
	}()
	var nativeErr C.DWORD
	window := uintptr(C.talkerOutlineCreate(C.int(region.X), C.int(region.Y),
		C.int(region.Width), C.int(region.Height), &nativeErr))
	if window == 0 {
		o.err = fmt.Errorf("mostrar a borda de vídeo: %w", syscall.Errno(nativeErr))
		ready <- o.err
		return
	}
	o.mu.Lock()
	o.window = window
	o.mu.Unlock()
	ready <- nil
	if failure := C.talkerOutlineRun(C.uintptr_t(window), C.uintptr_t(o.cancel)); failure != 0 {
		o.err = fmt.Errorf("acompanhar a borda de vídeo: %w", syscall.Errno(failure))
	}
}

func (o *nativeVideoOutline) SetPaused(paused bool) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.window == 0 {
		return nil
	}
	var flag C.int
	if paused {
		flag = 1
	}
	if C.talkerOutlinePaused(C.uintptr_t(o.window), flag) == 0 {
		return errors.New("não foi possível atualizar a borda de vídeo")
	}
	return nil
}

func (o *nativeVideoOutline) requestClose() {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.cancel != 0 {
		_ = windows.SetEvent(o.cancel)
	}
}

func (o *nativeVideoOutline) Close() error {
	o.requestClose()
	<-o.done
	return o.err
}
