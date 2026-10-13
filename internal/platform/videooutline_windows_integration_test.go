//go:build windows && integration

package platform

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWindowsVideoOutlineIsExternalExcludedClickThroughAndChangesOnPause(t *testing.T) {
	if os.Getenv("AI_TALKER_VIDEO_OUTLINE_TEST") != "1" {
		t.Skip("a borda real é uma integração opcional com janela nativa isolada")
	}
	region := ScreenRegion{X: 100, Y: 100, Width: 320, Height: 200}
	foreground := user32.NewProc("GetForegroundWindow")
	before, _, _ := foreground.Call()
	created, err := NewVideoOutline(t.Context(), region)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = created.Close() })
	outline := created.(*nativeVideoOutline)
	outline.mu.Lock()
	window := outline.window
	outline.mu.Unlock()
	dpi, _, _ := user32.NewProc("GetDpiForWindow").Call(window)
	thickness := videoOutlineThickness(uint32(dpi))
	if after, _, _ := foreground.Call(); after != before {
		t.Fatal("a borda tomou o foco da janela que estava em uso")
	}
	var affinity uint32
	if ok, _, err := user32.NewProc("GetWindowDisplayAffinity").Call(window, uintptr(unsafe.Pointer(&affinity))); ok == 0 || affinity != 0x11 {
		t.Fatal("a borda não está excluída da captura do Windows", affinity, err)
	}
	style, _, _ := user32.NewProc("GetWindowLongPtrW").Call(window, ^uintptr(19)) // GWL_EXSTYLE=-20
	if style&0x080800a0 != 0x080800a0 {
		t.Fatalf("a borda perdeu a passagem de cliques, camada ou ausência de foco: %#x", style)
	}
	gdi := windows.NewLazySystemDLL("gdi32.dll")
	nativeRegion, _, _ := gdi.NewProc("CreateRectRgn").Call(0, 0, 0, 0)
	defer gdi.NewProc("DeleteObject").Call(nativeRegion)
	if kind, _, err := user32.NewProc("GetWindowRgn").Call(window, nativeRegion); kind == 0 {
		t.Fatal("a borda perdeu o recorte externo", err)
	}
	border, _, _ := gdi.NewProc("PtInRegion").Call(nativeRegion, 20, uintptr(thickness-1))
	interior, _, _ := gdi.NewProc("PtInRegion").Call(nativeRegion, 20, uintptr(thickness))
	if border == 0 || interior != 0 {
		t.Fatal("a borda não ficou fora dos pixels selecionados")
	}
	// Windows native hit testing must find the underlying desktop/window
	// rather than this layered transparent border, including its visible edge.
	point := uintptr(uint32(region.X+20)) | uintptr(uint32(region.Y-1))<<32
	hit, _, _ := user32.NewProc("WindowFromPoint").Call(point)
	if hit == window {
		t.Fatal("a borda interceptou a interação com o conteúdo abaixo")
	}
	pauseState := func() uintptr {
		state, _, _ := user32.NewProc("GetWindowLongPtrW").Call(window, ^uintptr(20)) // GWLP_USERDATA=-21
		return state
	}
	dc, _, _ := user32.NewProc("GetDC").Call(window)
	if dc == 0 {
		t.Fatal("não foi possível conferir os pixels da borda")
	}
	defer func() {
		if dc != 0 {
			user32.NewProc("ReleaseDC").Call(window, dc)
		}
	}()
	pixel := func() uintptr {
		rgb, _, _ := gdi.NewProc("GetPixel").Call(dc, 20, 0)
		return rgb
	}
	purple := pixel()
	if purple != 0xff5ca6 {
		t.Fatalf("a borda ativa não ficou roxa: %#x", purple)
	}
	if pauseState() != 0 {
		t.Fatal("a borda não iniciou roxa")
	}
	if err := outline.SetPaused(true); err != nil {
		t.Fatal(err)
	}
	waitOutlineState(t, pauseState, 1)
	waitOutlineState(t, pixel, 0x919191)
	gray := pixel()
	if err := outline.SetPaused(false); err != nil {
		t.Fatal(err)
	}
	waitOutlineState(t, pauseState, 0)
	waitOutlineState(t, pixel, 0xff5ca6)
	user32.NewProc("ReleaseDC").Call(window, dc)
	dc = 0
	if err := outline.Close(); err != nil {
		t.Fatal(err)
	}
	if visible, _, _ := user32.NewProc("IsWindow").Call(window); visible != 0 {
		t.Fatal("encerrar a captura preservou a janela da borda")
	}
	if err := outline.Close(); err != nil {
		t.Fatal("encerrar a borda pela segunda vez não foi seguro", err)
	}
	if directory := os.Getenv("AI_TALKER_VIDEO_OUTLINE_EVIDENCE"); directory != "" {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		proof, err := json.MarshalIndent(map[string]any{
			"region": region, "native_window": window, "display_affinity": affinity,
			"style": style, "border_outside_selected_pixels": true,
			"native_hit_passthrough": true, "focus_preserved": true,
			"pause_gray_resume_purple": true, "close_removed_window": true,
			"purple_pixel": purple, "gray_pixel": gray,
			"effective_dpi": dpi, "logical_pixel_thickness_physical": thickness,
		}, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "native-outline.json"), proof, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func waitOutlineState(t *testing.T, read func() uintptr, expected uintptr) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for read() != expected {
		select {
		case <-ticker.C:
		case <-timer.C:
			t.Fatal("a borda não acompanhou a pausa ou retomada")
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		}
	}
}

func TestWindowsVideoOutlineCancellationRemovesNativeWindow(t *testing.T) {
	if os.Getenv("AI_TALKER_VIDEO_OUTLINE_TEST") != "1" {
		t.Skip("a borda real é uma integração opcional com janela nativa isolada")
	}
	ctx, cancel := context.WithCancel(t.Context())
	created, err := NewVideoOutline(ctx, ScreenRegion{X: 100, Y: 100, Width: 320, Height: 200})
	if err != nil {
		t.Fatal(err)
	}
	outline := created.(*nativeVideoOutline)
	window := outline.window
	cancel()
	if err := outline.Close(); err != nil {
		t.Fatal(err)
	}
	if visible, _, _ := user32.NewProc("IsWindow").Call(window); visible != 0 {
		t.Fatal("cancelar a captura preservou a janela da borda")
	}
}

func TestWindowsVideoOutlineAtMonitorEdgesRemainsVisibleAndExcluded(t *testing.T) {
	if os.Getenv("AI_TALKER_VIDEO_OUTLINE_TEST") != "1" {
		t.Skip("a borda real é uma integração opcional com janela nativa isolada")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	previousDPI, _, _ := user32.NewProc("SetThreadDpiAwarenessContext").Call(^uintptr(3))
	if previousDPI != 0 {
		defer user32.NewProc("SetThreadDpiAwarenessContext").Call(previousDPI)
	}
	metrics := user32.NewProc("GetSystemMetrics")
	width, _, _ := metrics.Call(0)
	height, _, _ := metrics.Call(1)
	if width == 0 || height == 0 {
		t.Fatal("não foi possível medir o monitor principal")
	}
	created, err := NewVideoOutline(t.Context(), ScreenRegion{Width: int(width), Height: int(height)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = created.Close() })
	outline := created.(*nativeVideoOutline)
	outline.mu.Lock()
	window := outline.window
	outline.mu.Unlock()
	var rect struct{ Left, Top, Right, Bottom int32 }
	if ok, _, err := user32.NewProc("GetWindowRect").Call(window, uintptr(unsafe.Pointer(&rect))); ok == 0 {
		t.Fatal("não foi possível medir a borda da tela", err)
	}
	if rect.Left != 0 || rect.Top != 0 || int(rect.Right) != int(width) || int(rect.Bottom) != int(height) {
		t.Fatal("a borda da tela inteira ficou fora do monitor", rect, width, height)
	}
	var affinity uint32
	if ok, _, err := user32.NewProc("GetWindowDisplayAffinity").Call(window, uintptr(unsafe.Pointer(&affinity))); ok == 0 || affinity != 0x11 {
		t.Fatal("os pixels internos da borda não ficaram excluídos da captura", affinity, err)
	}
	dc, _, _ := user32.NewProc("GetDC").Call(window)
	if dc == 0 {
		t.Fatal("não foi possível conferir o pixel visível na borda do monitor")
	}
	defer user32.NewProc("ReleaseDC").Call(window, dc)
	gdi := windows.NewLazySystemDLL("gdi32.dll")
	purple, _, _ := gdi.NewProc("GetPixel").Call(dc, 20, 0)
	if purple != 0xff5ca6 {
		t.Fatalf("a borda da tela inteira não ficou visível: %#x", purple)
	}
	if directory := os.Getenv("AI_TALKER_VIDEO_OUTLINE_EVIDENCE"); directory != "" {
		proof, err := json.MarshalIndent(map[string]any{
			"primary_monitor_width": width, "primary_monitor_height": height,
			"outline_rect": rect, "display_affinity": affinity,
			"visible_purple_pixel": purple, "fullscreen_outline_visible": true,
		}, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "native-outline-screen-edges.json"), proof, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
