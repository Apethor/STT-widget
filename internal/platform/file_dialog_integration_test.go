//go:build windows && cgo && filedialogintegration

package platform

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The external, PID-scoped Windows UI automation driver consumes ready.json.
// This opt-in test uses only temporary files and a separate dialog client GUID.
func TestWindowsNativeFileDialogOpenSaveAndCancellation(t *testing.T) {
	directory := os.Getenv("AI_TALKER_TEST_FILE_DIALOG_DIR")
	if directory == "" {
		t.Skip("informe a pasta isolada do controlador nativo de seletor de arquivos")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"open", "save", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			folder := t.TempDir()
			for index, name := range []string{"antigo.wav", "recente.wav"} {
				path := filepath.Join(folder, name)
				if err := os.WriteFile(path, []byte("synthetic audio"), 0o600); err != nil {
					t.Fatal(err)
				}
				modified := time.Date(2026, 10, 1+index, 12, 0, 0, 0, time.UTC)
				if err := os.Chtimes(path, modified, modified); err != nil {
					t.Fatal(err)
				}
			}
			destination := filepath.Join(folder, "recente.wav")
			if mode == "save" {
				destination = filepath.Join(folder, "texto-salvo.txt")
			}
			title := "AI Talker — seletor sintético " + mode
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			type selection struct {
				path string
				err  error
			}
			result := make(chan selection, 1)
			go func() {
				path, err := chooseNativeFile(ctx, 0, FileDialogOptions{
					Title: title, Folder: folder, Save: mode == "save", NewestFirst: mode != "save",
					FileName: "", DefaultExtension: "txt",
					Filters: []FileFilter{{Name: "Arquivos sintéticos", Pattern: "*.*"}},
				})
				result <- selection{path, err}
			}()
			var handle windows.HWND
			for handle == 0 && ctx.Err() == nil {
				handle = syntheticPickerWindow(title)
				if handle == 0 {
					time.Sleep(25 * time.Millisecond)
				}
			}
			if handle == 0 {
				cancel()
				<-result
				t.Fatal("native dialog did not create its own window")
			}
			payload, err := json.Marshal(map[string]any{
				"mode": mode, "window": uintptr(handle), "pid": os.Getpid(),
				"folder": folder, "destination": destination,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, "ready-"+mode+".json"), payload, 0o600); err != nil {
				t.Fatal(err)
			}
			if mode == "cancel" {
				cancel()
			}
			selected := <-result
			if mode == "cancel" {
				if !errors.Is(selected.err, context.Canceled) || selected.path != "" {
					t.Fatal("canceling a native picker did not release its thread")
				}
			} else if selected.err != nil || !windowsPathEqual(selected.path, destination) {
				t.Fatalf("native %s selection = %q / %v", mode, selected.path, selected.err)
			}
			if mode == "save" {
				if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("selecting a save destination truncated/created the file before confirmation")
				}
			}
			t.Logf("native %s: seleção/cancelamento retornou sem alterar outros arquivos", mode)
		})
	}
}

func windowsPathEqual(first, second string) bool {
	a, errA := filepath.Abs(first)
	b, errB := filepath.Abs(second)
	return errA == nil && errB == nil && a == b
}

func syntheticPickerWindow(title string) windows.HWND {
	search := &syntheticPickerSearch{pid: uint32(os.Getpid())}
	encoded, _ := windows.UTF16FromString(title)
	copy(search.title[:], encoded)
	var pin runtime.Pinner
	pin.Pin(search)
	defer pin.Unpin()
	_ = windows.EnumWindows(syntheticPickerCallback(), unsafe.Pointer(search))
	runtime.KeepAlive(search)
	return search.window
}

type syntheticPickerSearch struct {
	pid    uint32
	title  [512]uint16
	window windows.HWND
}

var syntheticPickerCallback = sync.OnceValue(func() uintptr { return windows.NewCallback(enumSyntheticPicker) })

func enumSyntheticPicker(handle windows.HWND, search *syntheticPickerSearch) uintptr {
	var pid uint32
	if _, err := windows.GetWindowThreadProcessId(handle, &pid); err != nil || pid != search.pid {
		return 1
	}
	if visible, _, _ := procIsWindowVisible.Call(uintptr(handle)); visible == 0 {
		return 1
	}
	var text [512]uint16
	procInternalWindowText.Call(uintptr(handle), uintptr(unsafe.Pointer(&text[0])), uintptr(len(text)))
	if windows.UTF16ToString(text[:]) == windows.UTF16ToString(search.title[:]) {
		search.window = handle
		return 0
	}
	return 1
}
