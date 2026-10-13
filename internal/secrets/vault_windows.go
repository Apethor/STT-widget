//go:build windows

package secrets

import (
	"errors"
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	credentialDLL = windows.NewLazySystemDLL("advapi32.dll")
	credRead      = credentialDLL.NewProc("CredReadW")
	credWrite     = credentialDLL.NewProc("CredWriteW")
	credDelete    = credentialDLL.NewProc("CredDeleteW")
	credFree      = credentialDLL.NewProc("CredFree")
)

type credential struct {
	Flags, Type             uint32
	TargetName, Comment     *uint16
	LastWritten             windows.Filetime
	BlobSize                uint32
	Blob                    *byte
	Persist, AttributeCount uint32
	Attributes              uintptr
	TargetAlias, UserName   *uint16
}

func (Native) Read(target string) (string, error) {
	name, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return "", errors.New("a referência da chave é inválida")
	}
	var saved *credential
	ok, _, callErr := credRead.Call(uintptr(unsafe.Pointer(name)), 1, 0, uintptr(unsafe.Pointer(&saved)))
	runtime.KeepAlive(name)
	if ok == 0 {
		if errors.Is(callErr, windows.ERROR_NOT_FOUND) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("ler a chave no Gerenciador de Credenciais: %w", callErr)
	}
	defer credFree.Call(uintptr(unsafe.Pointer(saved)))
	if saved == nil || saved.Type != 1 || saved.Blob == nil || saved.BlobSize == 0 || saved.BlobSize > 2560 {
		return "", errors.New("a credencial salva é inválida; substitua a chave")
	}
	return string(unsafe.Slice(saved.Blob, int(saved.BlobSize))), nil
}

func (Native) Write(target, key string) error {
	if err := validate(target, key); err != nil {
		return err
	}
	name, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return errors.New("a referência da chave é inválida")
	}
	user, _ := windows.UTF16PtrFromString("STT-widget")
	blob := []byte(key)
	defer clear(blob)
	value := credential{Type: 1, TargetName: name, BlobSize: uint32(len(blob)), Blob: &blob[0], Persist: 2, UserName: user}
	ok, _, callErr := credWrite.Call(uintptr(unsafe.Pointer(&value)), 0)
	runtime.KeepAlive(name)
	runtime.KeepAlive(user)
	runtime.KeepAlive(blob)
	runtime.KeepAlive(value)
	if ok == 0 {
		return fmt.Errorf("salvar a chave no Gerenciador de Credenciais: %w", callErr)
	}
	return nil
}

func (Native) Delete(target string) error {
	name, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return errors.New("a referência da chave é inválida")
	}
	ok, _, callErr := credDelete.Call(uintptr(unsafe.Pointer(name)), 1, 0)
	runtime.KeepAlive(name)
	if ok == 0 && !errors.Is(callErr, windows.ERROR_NOT_FOUND) {
		return fmt.Errorf("remover a chave no Gerenciador de Credenciais: %w", callErr)
	}
	return nil
}
