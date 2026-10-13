package platform

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"syscall"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"
)

const (
	cfUnicodeText          = 13
	gmemMoveable           = 0x0002
	maxClipboardInputBytes = 32 << 20
	maxClipboardBytes      = 64 << 20
)

var (
	procCreateWindowEx   = user32.NewProc("CreateWindowExW")
	procDestroyWindow    = user32.NewProc("DestroyWindow")
	procOpenClipboard    = user32.NewProc("OpenClipboard")
	procCloseClipboard   = user32.NewProc("CloseClipboard")
	procEmptyClipboard   = user32.NewProc("EmptyClipboard")
	procSetClipboardData = user32.NewProc("SetClipboardData")
	procGlobalAlloc      = kernel32.NewProc("GlobalAlloc")
	procGlobalFree       = kernel32.NewProc("GlobalFree")
	procGlobalLock       = kernel32.NewProc("GlobalLock")
	procGlobalUnlock     = kernel32.NewProc("GlobalUnlock")
	procSetLastError     = kernel32.NewProc("SetLastError")
	procMoveMemory       = kernel32.NewProc("RtlMoveMemory")
)

// Copy writes text as native CF_UNICODETEXT without starting a helper process.
// Line feeds are converted to Windows CRLF. Embedded NUL, invalid UTF-8, and
// allocations above 64 MiB are rejected before changing the clipboard.
//
// Opening the clipboard can fail while another application owns it. Such an
// error matches ErrClipboardBusy, allowing the caller to retry asynchronously.
// Callers should run Copy outside the UI loop.
func Copy(text string) (retErr error) {
	units, err := clipboardUTF16(text)
	if err != nil {
		return err
	}

	// The clipboard owner window and clipboard calls must stay on one OS
	// thread. All native buffers contain scalar UTF-16 data, never Go pointers.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	memory, err := clipboardMemory(units)
	if err != nil {
		return err
	}
	defer func() {
		if memory == 0 {
			return // SetClipboardData transferred ownership to Windows.
		}
		result, _, callErr := procGlobalFree.Call(memory)
		if result != 0 {
			retErr = errors.Join(retErr, windowsCallError("free clipboard memory", callErr))
		}
	}()

	window, err := clipboardOwner()
	if err != nil {
		return err
	}
	defer func() {
		result, _, callErr := procDestroyWindow.Call(window)
		if result == 0 {
			retErr = errors.Join(retErr, windowsCallError("destroy clipboard owner", callErr))
		}
	}()

	result, _, callErr := procOpenClipboard.Call(window)
	if result == 0 {
		return errors.Join(ErrClipboardBusy, windowsCallError("open clipboard", callErr))
	}
	defer func() {
		result, _, callErr := procCloseClipboard.Call()
		if result == 0 {
			retErr = errors.Join(retErr, windowsCallError("close clipboard", callErr))
		}
	}()

	result, _, callErr = procEmptyClipboard.Call()
	if result == 0 {
		return windowsCallError("empty clipboard", callErr)
	}
	result, _, callErr = procSetClipboardData.Call(cfUnicodeText, memory)
	if result == 0 {
		return windowsCallError("set clipboard text", callErr)
	}
	memory = 0
	return nil
}

func clipboardUTF16(text string) ([]uint16, error) {
	if len(text) > maxClipboardInputBytes {
		return nil, fmt.Errorf("copy text: input exceeds %d bytes: %w", maxClipboardInputBytes, os.ErrInvalid)
	}
	if !utf8.ValidString(text) || strings.IndexByte(text, 0) >= 0 {
		return nil, fmt.Errorf("copy text: expected utf-8 without nul: %w", os.ErrInvalid)
	}

	// Count the terminated UTF-16 representation before allocating it. CRLF
	// expansion and surrogate pairs must also fit the native buffer limit.
	count := 1
	var previous rune
	for _, character := range text {
		if character == '\n' && previous != '\r' {
			count++
		}
		if character > 0xffff {
			count++
		}
		count++
		if count > maxClipboardBytes/2 {
			return nil, fmt.Errorf("copy text: utf-16 exceeds %d bytes: %w", maxClipboardBytes, os.ErrInvalid)
		}
		previous = character
	}

	units := make([]uint16, 0, count)
	previous = 0
	for _, character := range text {
		if character == '\n' && previous != '\r' {
			units = append(units, '\r')
		}
		units = utf16.AppendRune(units, character)
		previous = character
	}
	return append(units, 0), nil
}

func clipboardMemory(units []uint16) (memory uintptr, retErr error) {
	if len(units) == 0 || len(units) > maxClipboardBytes/2 {
		return 0, fmt.Errorf("allocate clipboard memory: invalid buffer size: %w", os.ErrInvalid)
	}
	size := uintptr(len(units)) * 2
	memory, _, callErr := procGlobalAlloc.Call(gmemMoveable, size)
	if memory == 0 {
		return 0, windowsCallError("allocate clipboard memory", callErr)
	}
	defer func() {
		if retErr == nil {
			return
		}
		result, _, callErr := procGlobalFree.Call(memory)
		if result != 0 {
			retErr = errors.Join(retErr, windowsCallError("free clipboard memory", callErr))
		}
		memory = 0
	}()

	pointer, _, callErr := procGlobalLock.Call(memory)
	if pointer == 0 {
		return memory, windowsCallError("lock clipboard memory", callErr)
	}
	// Keep the native address as uintptr. MoveMemory copies scalar data
	// synchronously, avoiding conversions of native addresses into Go pointers.
	procMoveMemory.Call(pointer, uintptr(unsafe.Pointer(&units[0])), size)
	runtime.KeepAlive(units)

	// GlobalUnlock returns zero when the final lock was successfully removed.
	// Clear the thread's last error first so this differs from failure.
	procSetLastError.Call(0)
	result, _, callErr := procGlobalUnlock.Call(memory)
	if result == 0 && !errors.Is(callErr, syscall.Errno(0)) {
		return memory, windowsCallError("unlock clipboard memory", callErr)
	}
	return memory, nil
}

func clipboardOwner() (uintptr, error) {
	// A system STATIC class needs no Go window procedure or class structure.
	// HWND_MESSAGE = -3 creates an invisible message-only window. Supplying
	// this owner avoids OpenClipboard(NULL)/EmptyClipboard's NULL-owner trap.
	className := [...]uint16{'S', 'T', 'A', 'T', 'I', 'C', 0}
	window, _, callErr := procCreateWindowEx.Call(
		0,
		uintptr(unsafe.Pointer(&className[0])),
		0,
		0,
		0,
		0,
		0,
		0,
		^uintptr(2),
		0,
		0,
		0,
	)
	runtime.KeepAlive(&className)
	if window == 0 {
		return 0, windowsCallError("create clipboard owner", callErr)
	}
	return window, nil
}
