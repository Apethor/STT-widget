package platform

import (
	"encoding/binary"
	"errors"
	"os"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

func TestClipboardUTF16(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		text string
		want string
	}{
		{name: "empty", text: "", want: ""},
		{name: "ascii", text: "Bedrock", want: "Bedrock"},
		{name: "portuguese and emoji", text: "Gravação, ação 🎙️", want: "Gravação, ação 🎙️"},
		{name: "line feeds", text: "primeira\nsegunda\n", want: "primeira\r\nsegunda\r\n"},
		{name: "existing crlf", text: "primeira\r\nsegunda", want: "primeira\r\nsegunda"},
		{name: "mixed endings", text: "a\r\nb\nc", want: "a\r\nb\r\nc"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			units, err := clipboardUTF16(test.text)
			if err != nil {
				t.Fatal(err)
			}
			if len(units) == 0 || units[len(units)-1] != 0 {
				t.Fatal("clipboard representation is not nul terminated")
			}
			got := string(utf16.Decode(units[:len(units)-1]))
			if got != test.want {
				t.Fatalf("clipboard text = %q, want %q", got, test.want)
			}
		})
	}
}

func TestClipboardUTF16RejectsInvalidText(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		text func() string
	}{
		{name: "embedded nul", text: func() string { return "before\x00after" }},
		{name: "invalid utf8", text: func() string { return string([]byte{0xff, 0xfe}) }},
		{name: "oversized input", text: func() string { return strings.Repeat("a", maxClipboardInputBytes+1) }},
		{name: "oversized crlf expansion", text: func() string { return strings.Repeat("\n", maxClipboardBytes/4) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			units, err := clipboardUTF16(test.text())
			if units != nil || !errors.Is(err, os.ErrInvalid) {
				t.Fatalf("clipboard representation = %v, %v; want nil, ErrInvalid", len(units), err)
			}
		})
	}
}

func TestClipboardMemory(t *testing.T) {
	t.Parallel()
	// Test native allocation and copying without opening or changing the
	// clipboard, or creating an owner window.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	units, err := clipboardUTF16("Gravação 🎙️\nSegunda linha.")
	if err != nil {
		t.Fatal(err)
	}
	memory, err := clipboardMemory(units)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		result, _, callErr := procGlobalFree.Call(memory)
		if result != 0 {
			t.Error(windowsCallError("free test clipboard memory", callErr))
		}
	}()
	pointer, _, callErr := procGlobalLock.Call(memory)
	if pointer == 0 {
		t.Fatal(windowsCallError("lock test clipboard memory", callErr))
	}
	defer func() {
		procSetLastError.Call(0)
		result, _, callErr := procGlobalUnlock.Call(memory)
		if result == 0 && !errors.Is(callErr, syscall.Errno(0)) {
			t.Error(windowsCallError("unlock test clipboard memory", callErr))
		}
	}()

	data := make([]byte, len(units)*2)
	var bytesRead uintptr
	err = windows.ReadProcessMemory(
		windows.CurrentProcess(),
		pointer,
		&data[0],
		uintptr(len(data)),
		&bytesRead,
	)
	if err != nil {
		t.Fatal(err)
	}
	if bytesRead != uintptr(len(data)) {
		t.Fatalf("native memory read = %d bytes, want %d", bytesRead, len(data))
	}
	got := make([]uint16, len(units))
	for index := range got {
		got[index] = binary.LittleEndian.Uint16(data[index*2:])
	}
	if !slices.Equal(got, units) {
		t.Fatalf("native clipboard memory = %v, want %v", got, units)
	}
}

func TestClipboardMemoryRejectsEmptyBuffer(t *testing.T) {
	t.Parallel()
	memory, err := clipboardMemory(nil)
	if memory != 0 || !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("empty native clipboard buffer = %d, %v; want zero, ErrInvalid", memory, err)
	}
}
