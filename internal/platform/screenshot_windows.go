package platform

/*
#cgo LDFLAGS: -lgdi32 -luser32 -lmsimg32
#undef _WIN32_WINNT
#define _WIN32_WINNT 0x0A00
#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#include <windowsx.h>
#include <stdint.h>
#include <stdlib.h>

// Selection and its GDI resources belong to one native thread. No callback
// retains Go memory and no global cursor setting is changed.
typedef struct {
    HDC screenDC, memoryDC, tintDC;
    HBITMAP bitmap, tint;
    HGDIOBJ oldBitmap, oldTint;
    BYTE *pixels;
    int x, y, width, height;
    POINT start, end;
    RECT selected;
    int dragging, done, accepted;
    int english;
} TalkerSelection;

static RECT talkerSelectionRect(TalkerSelection *s) {
    RECT r = { min(s->start.x, s->end.x), min(s->start.y, s->end.y),
               max(s->start.x, s->end.x), max(s->start.y, s->end.y) };
    return r;
}

static LRESULT CALLBACK talkerSelectionProc(HWND window, UINT message, WPARAM wp, LPARAM lp) {
    TalkerSelection *s = (TalkerSelection *) GetWindowLongPtrW(window, GWLP_USERDATA);
    if (message == WM_NCCREATE) {
        s = (TalkerSelection *) ((CREATESTRUCTW *) lp)->lpCreateParams;
        SetWindowLongPtrW(window, GWLP_USERDATA, (LONG_PTR) s);
    }
    if (!s) return DefWindowProcW(window, message, wp, lp);
    switch (message) {
    case WM_ERASEBKGND: return 1;
    case WM_SETCURSOR:
        SetCursor(LoadCursorW(NULL, MAKEINTRESOURCEW(32515))); // IDC_CROSS
        return TRUE;
    case WM_PAINT: {
        PAINTSTRUCT paint;
        HDC dc = BeginPaint(window, &paint);
        BitBlt(dc, 0, 0, s->width, s->height, s->memoryDC, 0, 0, SRCCOPY);
        if (s->dragging) {
            RECT r = talkerSelectionRect(s);
            BLENDFUNCTION blend = {AC_SRC_OVER, 0, 62, 0};
            if (r.right > r.left && r.bottom > r.top)
                AlphaBlend(dc, r.left, r.top, r.right-r.left, r.bottom-r.top,
                           s->tintDC, 0, 0, 1, 1, blend);
            HPEN pen = CreatePen(PS_SOLID, 2, RGB(166, 92, 255));
            HGDIOBJ oldPen = SelectObject(dc, pen);
            HGDIOBJ oldBrush = SelectObject(dc, GetStockObject(NULL_BRUSH));
            Rectangle(dc, r.left, r.top, r.right, r.bottom);
            SelectObject(dc, oldBrush);
            SelectObject(dc, oldPen);
            DeleteObject(pen);
        }
        SetBkMode(dc, TRANSPARENT);
        RECT text = {18, 18, s->width-18, 58};
        SetTextColor(dc, RGB(0, 0, 0));
        OffsetRect(&text, 1, 1);
        const wchar_t *hint = s->english ?
            L"Drag to capture an area  |  Esc or right-click: cancel" :
            L"Arraste para capturar uma área  |  Esc ou botão direito: cancelar";
        DrawTextW(dc, hint, -1, &text, DT_LEFT | DT_SINGLELINE);
        OffsetRect(&text, -1, -1);
        SetTextColor(dc, RGB(255, 255, 255));
        DrawTextW(dc, hint, -1, &text, DT_LEFT | DT_SINGLELINE);
        EndPaint(window, &paint);
        return 0;
    }
    case WM_LBUTTONDOWN:
        s->start.x = max(0, min(s->width, GET_X_LPARAM(lp)));
        s->start.y = max(0, min(s->height, GET_Y_LPARAM(lp)));
        s->end = s->start;
        s->dragging = 1;
        SetCapture(window);
        InvalidateRect(window, NULL, FALSE);
        return 0;
    case WM_MOUSEMOVE:
        if (s->dragging) {
            s->end.x = max(0, min(s->width, GET_X_LPARAM(lp)));
            s->end.y = max(0, min(s->height, GET_Y_LPARAM(lp)));
            InvalidateRect(window, NULL, FALSE);
        }
        return 0;
    case WM_LBUTTONUP:
        if (s->dragging) {
            s->end.x = max(0, min(s->width, GET_X_LPARAM(lp)));
            s->end.y = max(0, min(s->height, GET_Y_LPARAM(lp)));
            s->selected = talkerSelectionRect(s);
            s->dragging = 0;
            ReleaseCapture();
            if (s->selected.right-s->selected.left >= 2 && s->selected.bottom-s->selected.top >= 2)
                s->accepted = s->done = 1;
            InvalidateRect(window, NULL, FALSE);
        }
        return 0;
    case WM_KEYDOWN:
        if (wp == VK_ESCAPE) s->done = 1;
        return 0;
    case WM_HOTKEY:
        if (wp == 0xA17) s->done = 1;
        return 0;
    case WM_RBUTTONDOWN:
    case WM_CLOSE:
    case WM_DISPLAYCHANGE:
        s->done = 1;
        return 0;
    case WM_ACTIVATE:
        if (LOWORD(wp) == WA_INACTIVE) s->done = 1;
        return 0;
    case WM_CAPTURECHANGED:
        // Lost capture never turns an incomplete drag into a screenshot.
        if (s->dragging) {
            s->dragging = 0;
            InvalidateRect(window, NULL, FALSE);
        }
        return 0;
    }
    return DefWindowProcW(window, message, wp, lp);
}

static void talkerSelectionFree(TalkerSelection *s) {
    if (!s) return;
    if (s->oldTint) SelectObject(s->tintDC, s->oldTint);
    if (s->tint) DeleteObject(s->tint);
    if (s->tintDC) DeleteDC(s->tintDC);
    if (s->oldBitmap) SelectObject(s->memoryDC, s->oldBitmap);
    if (s->bitmap) DeleteObject(s->bitmap);
    if (s->memoryDC) DeleteDC(s->memoryDC);
    if (s->screenDC) ReleaseDC(NULL, s->screenDC);
    free(s);
}

static DWORD talkerSelectScreenshot(uintptr_t cancel, int english, TalkerSelection **result) {
    *result = NULL;
    DWORD error = ERROR_SUCCESS;
    HWND window = NULL;
    int escapeRegistered = 0;
    HINSTANCE instance = GetModuleHandleW(NULL);
    const wchar_t *className = L"AI-Talker.ScreenshotSelection";
    DPI_AWARENESS_CONTEXT previousDPI = SetThreadDpiAwarenessContext(DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2);
    TalkerSelection *s = (TalkerSelection *) calloc(1, sizeof(TalkerSelection));
    if (!s) { error = ERROR_NOT_ENOUGH_MEMORY; goto done; }
    s->english = english;
    s->x = GetSystemMetrics(SM_XVIRTUALSCREEN);
    s->y = GetSystemMetrics(SM_YVIRTUALSCREEN);
    s->width = GetSystemMetrics(SM_CXVIRTUALSCREEN);
    s->height = GetSystemMetrics(SM_CYVIRTUALSCREEN);
    // Bound allocations before converting them to Go's addressable slice.
    if (s->width <= 0 || s->height <= 0 || (uint64_t)s->width * s->height > 100000000) {
        error = ERROR_INVALID_DATA; goto done;
    }
    s->screenDC = GetDC(NULL);
    s->memoryDC = CreateCompatibleDC(s->screenDC);
    BITMAPINFO info = {0};
    info.bmiHeader.biSize = sizeof(BITMAPINFOHEADER);
    info.bmiHeader.biWidth = s->width;
    info.bmiHeader.biHeight = -s->height;
    info.bmiHeader.biPlanes = 1;
    info.bmiHeader.biBitCount = 32;
    info.bmiHeader.biCompression = BI_RGB;
    s->bitmap = CreateDIBSection(s->screenDC, &info, DIB_RGB_COLORS, (void **) &s->pixels, NULL, 0);
    if (!s->screenDC || !s->memoryDC || !s->bitmap) { error = ERROR_NOT_ENOUGH_MEMORY; goto done; }
    s->oldBitmap = SelectObject(s->memoryDC, s->bitmap);
    // Capture before creating the overlay; annotations and instructions never
    // become part of the saved image, including layered desktop windows.
    if (!BitBlt(s->memoryDC, 0, 0, s->width, s->height, s->screenDC, s->x, s->y, SRCCOPY | CAPTUREBLT)) {
        error = GetLastError(); if (!error) error = ERROR_GEN_FAILURE; goto done;
    }
    GdiFlush();
    s->tintDC = CreateCompatibleDC(s->screenDC);
    s->tint = CreateCompatibleBitmap(s->screenDC, 1, 1);
    if (!s->tintDC || !s->tint) { error = ERROR_NOT_ENOUGH_MEMORY; goto done; }
    s->oldTint = SelectObject(s->tintDC, s->tint);
    SetPixel(s->tintDC, 0, 0, RGB(166, 92, 255));
    WNDCLASSEXW cls = {0};
    cls.cbSize = sizeof(cls);
    cls.lpfnWndProc = talkerSelectionProc;
    cls.hInstance = instance;
    cls.hCursor = LoadCursorW(NULL, MAKEINTRESOURCEW(32515));
    cls.lpszClassName = className;
    if (!RegisterClassExW(&cls) && GetLastError() != ERROR_CLASS_ALREADY_EXISTS) {
        error = GetLastError(); goto done;
    }
    window = CreateWindowExW(WS_EX_TOPMOST | WS_EX_TOOLWINDOW, className,
        english ? L"Select screen capture" : L"Selecionar captura de tela",
                WS_POPUP, s->x, s->y, s->width, s->height, NULL, NULL, instance, s);
    if (!window) { error = GetLastError(); goto done; }
    if (WaitForSingleObject((HANDLE)cancel, 0) == WAIT_OBJECT_0) { error = ERROR_CANCELLED; goto done; }
    // Escape belongs to this selection only. It must still cancel if Windows
    // does not immediately grant the overlay keyboard focus.
    escapeRegistered = RegisterHotKey(window, 0xA17, MOD_NOREPEAT, VK_ESCAPE);
    ShowWindow(window, SW_SHOW);
    SetForegroundWindow(window);
    SetFocus(window);
    UpdateWindow(window);
    while (!s->done) {
        HANDLE canceled = (HANDLE) cancel;
        DWORD wait = MsgWaitForMultipleObjectsEx(1, &canceled, INFINITE, QS_ALLINPUT, MWMO_INPUTAVAILABLE);
        if (wait == WAIT_OBJECT_0) { error = ERROR_CANCELLED; break; }
        if (wait == WAIT_FAILED) { error = GetLastError(); break; }
        MSG message;
        while (PeekMessageW(&message, NULL, 0, 0, PM_REMOVE)) {
            if (message.message == WM_QUIT) { s->done = 1; break; }
            TranslateMessage(&message);
            DispatchMessageW(&message);
            if (s->done) break;
        }
    }
    if (!error && !s->accepted) error = ERROR_CANCELLED;
done:
    if (window) {
        if (escapeRegistered) UnregisterHotKey(window, 0xA17);
        if (GetCapture() == window) ReleaseCapture();
        DestroyWindow(window);
        SetCursor(LoadCursorW(NULL, MAKEINTRESOURCEW(32512))); // IDC_ARROW
    }
    if (previousDPI) SetThreadDpiAwarenessContext(previousDPI);
    if (!error) *result = s;
    else talkerSelectionFree(s);
    return error;
}
*/
import "C"

import (
	"context"
	"fmt"
	"image"
	"runtime"
	"unsafe"

	"ai-talker/internal/locale"

	"golang.org/x/sys/windows"
)

func captureSelectionEnglish() C.int {
	if locale.Current() == locale.English {
		return 1
	}
	return 0
}

// SelectScreenRegion uses the same frozen, DPI-aware overlay as screenshots
// and returns physical virtual-desktop coordinates for subsequent capture.
func SelectScreenRegion(ctx context.Context) (ScreenRegion, error) {
	if err := ctx.Err(); err != nil {
		return ScreenRegion{}, err
	}
	canceled, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return ScreenRegion{}, fmt.Errorf("preparar seleção da área de vídeo: %w", err)
	}
	finished := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = windows.SetEvent(canceled)
		close(finished)
	})
	defer func() {
		if !stop() {
			<-finished
		}
		_ = windows.CloseHandle(canceled)
	}()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var selection *C.TalkerSelection
	code := C.talkerSelectScreenshot(C.uintptr_t(canceled), captureSelectionEnglish(), &selection)
	if code != 0 {
		if ctx.Err() != nil {
			return ScreenRegion{}, ctx.Err()
		}
		if code == C.ERROR_CANCELLED {
			return ScreenRegion{}, ErrScreenshotCanceled
		}
		return ScreenRegion{}, fmt.Errorf("selecionar área para vídeo: %w", windows.Errno(code))
	}
	defer C.talkerSelectionFree(selection)
	if err := ctx.Err(); err != nil {
		return ScreenRegion{}, err
	}
	area := selection.selected
	region := ScreenRegion{
		X:      int(selection.x) + int(area.left),
		Y:      int(selection.y) + int(area.top),
		Width:  int(area.right - area.left),
		Height: int(area.bottom - area.top),
	}
	if region.Width < 2 || region.Height < 2 {
		return ScreenRegion{}, ErrScreenshotCanceled
	}
	return region, nil
}

// SelectScreenshot freezes the virtual desktop and waits for a native area
// selection on its own OS thread. Recording and the Fyne loop keep running.
func SelectScreenshot(ctx context.Context) (image.Image, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	canceled, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return nil, fmt.Errorf("preparar seleção de screenshot: %w", err)
	}
	finished := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = windows.SetEvent(canceled)
		close(finished)
	})
	defer func() {
		if !stop() {
			<-finished
		}
		_ = windows.CloseHandle(canceled)
	}()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var selection *C.TalkerSelection
	code := C.talkerSelectScreenshot(C.uintptr_t(canceled), captureSelectionEnglish(), &selection)
	if code != 0 {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if code == C.ERROR_CANCELLED {
			return nil, ErrScreenshotCanceled
		}
		return nil, fmt.Errorf("capturar área da tela: %w", windows.Errno(code))
	}
	defer C.talkerSelectionFree(selection)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	area := selection.selected
	width, height := int(area.right-area.left), int(area.bottom-area.top)
	pixels := unsafe.Slice((*byte)(unsafe.Pointer(selection.pixels)), int(selection.width)*int(selection.height)*4)
	result := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		from := ((int(area.top)+y)*int(selection.width) + int(area.left)) * 4
		to := y * result.Stride
		for x := 0; x < width; x++ {
			result.Pix[to+x*4] = pixels[from+x*4+2]
			result.Pix[to+x*4+1] = pixels[from+x*4+1]
			result.Pix[to+x*4+2] = pixels[from+x*4]
			result.Pix[to+x*4+3] = 255
		}
	}
	return result, nil
}
