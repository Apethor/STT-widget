//go:build windows

package platform

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// ShowNotice presents startup errors from a GUI executable without a console.
func ShowNotice(title, message string) error {
	caption, err := windows.UTF16PtrFromString(title)
	if err != nil {
		return err
	}
	text, err := windows.UTF16PtrFromString(message)
	if err != nil {
		return err
	}
	result, _, callErr := windows.NewLazySystemDLL("user32.dll").
		NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(caption)), 0x40)
	if result == 0 {
		return callErr
	}
	return nil
}
