//go:build windows && cgo

package platform

/*
#cgo LDFLAGS: -lole32 -lshell32 -luuid
#include <stdlib.h>
#include "file_dialog_windows.h"
*/
import "C"

import (
	"context"
	"encoding/binary"
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ChooseFile opens the Windows Explorer picker on a worker's STA thread.
// The owner is disabled by Windows until selection or cancellation completes.
func ChooseFile(ctx context.Context, options FileDialogOptions) (string, error) {
	if err := options.validate(); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	owner, err := currentWindow()
	if err != nil {
		return "", err
	}
	return chooseNativeFile(ctx, uintptr(owner), options)
}

func chooseNativeFile(ctx context.Context, owner uintptr, options FileDialogOptions) (string, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var allocated []unsafe.Pointer
	defer func() {
		for _, pointer := range allocated {
			C.free(pointer)
		}
	}()
	wide := func(value string) *C.wchar_t {
		encoded, _ := windows.UTF16FromString(value) // validate rejected embedded NULs.
		buffer := make([]byte, len(encoded)*2)
		for index, code := range encoded {
			binary.LittleEndian.PutUint16(buffer[index*2:], code)
		}
		pointer := C.CBytes(buffer)
		allocated = append(allocated, pointer)
		return (*C.wchar_t)(pointer)
	}
	title, folder, filename, extension := wide(options.Title), wide(options.Folder), wide(options.FileName), wide(options.DefaultExtension)
	var filters *C.TalkerFileFilter
	if len(options.Filters) > 0 {
		pointer := C.calloc(C.size_t(len(options.Filters)), C.size_t(C.sizeof_TalkerFileFilter))
		if pointer == nil {
			return "", fmt.Errorf("não foi possível preparar os filtros do seletor de arquivos")
		}
		allocated = append(allocated, pointer)
		filters = (*C.TalkerFileFilter)(pointer)
		for index, filter := range options.Filters {
			native := &unsafe.Slice(filters, len(options.Filters))[index]
			native.name, native.pattern = wide(filter.Name), wide(filter.Pattern)
		}
	}
	state := C.talkerNewFileDialogState()
	if state == nil {
		return "", fmt.Errorf("não foi possível preparar o seletor de arquivos")
	}
	defer C.talkerFreeFileDialogState(state)
	finished, watcherDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(watcherDone)
		select {
		case <-ctx.Done():
			C.talkerCancelFileDialog(state)
		case <-finished:
		}
	}()
	var save, newest C.int
	if options.Save {
		save = 1
	}
	if options.NewestFirst {
		newest = 1
	}
	var selected *C.wchar_t
	status := C.talkerChooseFile(state, C.uintptr_t(owner), save, newest, title, folder, filename,
		extension, filters, C.uint(len(options.Filters)), &selected)
	close(finished)
	<-watcherDone // The cancellation worker must finish before native memory is freed.
	if selected != nil {
		defer C.talkerFreeFilePath(selected)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if uint32(status) == 0x800704c7 { // HRESULT_FROM_WIN32(ERROR_CANCELLED)
		return "", nil
	}
	if status < 0 {
		return "", fmt.Errorf("o seletor de arquivos do Windows falhou (0x%08x)", uint32(status))
	}
	if selected == nil {
		return "", fmt.Errorf("o seletor de arquivos não retornou um caminho")
	}
	return windows.UTF16PtrToString((*uint16)(unsafe.Pointer(selected))), nil
}
