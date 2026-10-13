//go:build !windows

package secrets

import "errors"

func (Native) Read(string) (string, error) { return "", ErrNotFound }
func (Native) Write(string, string) error {
	return errors.New("o armazenamento seguro de chaves requer Windows")
}
func (Native) Delete(string) error {
	return errors.New("o armazenamento seguro de chaves requer Windows")
}
