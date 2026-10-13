package platform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

const (
	fileOperationDelete    = 3
	fileOperationAllowUndo = 0x0040
	fileOperationSilent    = 0x0004
	fileOperationNoConfirm = 0x0010
	fileOperationNoErrorUI = 0x0400
)

type shellFileOperation struct {
	window       uintptr
	function     uint32
	from         *uint16
	to           *uint16
	flags        uint16
	aborted      int32
	nameMappings uintptr
	title        *uint16
}

// Recycle moves absolute files to the Windows Recycle Bin without prompting
// again. The caller must show its own preview and confirmation first.
func Recycle(ctx context.Context, paths []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(paths) == 0 {
		return errors.New("nenhum arquivo foi selecionado")
	}
	var from []uint16
	for _, path := range paths {
		if strings.TrimSpace(path) == "" || strings.ContainsRune(path, '\x00') {
			return errors.New("o caminho do arquivo é inválido")
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return fmt.Errorf("localizar arquivo para a Lixeira: %w", err)
		}
		if _, err := os.Lstat(absolute); err != nil {
			return fmt.Errorf("localizar %s: %w", filepath.Base(absolute), err)
		}
		encoded := utf16.Encode([]rune(absolute))
		from = append(from, encoded...)
		from = append(from, 0)
	}
	from = append(from, 0)
	operation := shellFileOperation{
		function: fileOperationDelete, from: &from[0],
		flags: fileOperationAllowUndo | fileOperationSilent |
			fileOperationNoConfirm | fileOperationNoErrorUI,
	}
	result, _, _ := syscall.NewLazyDLL("shell32.dll").NewProc("SHFileOperationW").Call(
		uintptr(unsafe.Pointer(&operation)),
	)
	runtime.KeepAlive(from)
	runtime.KeepAlive(&operation)
	if result != 0 {
		return fmt.Errorf("o Windows recusou a operação para a Lixeira (código %d)", result)
	}
	if operation.aborted != 0 {
		return errors.New("o Windows interrompeu a operação para a Lixeira")
	}
	return nil
}
