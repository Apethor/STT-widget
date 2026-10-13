// Package secrets stores API credentials separately from application metadata.
package secrets

import "errors"

var ErrNotFound = errors.New("a chave deste endpoint não foi salva")

type Vault interface {
	Read(string) (string, error)
	Write(string, string) error
	Delete(string) error
}

type Native struct{}

func validate(target, key string) error {
	if len(target) < 1 || len(target) > 1024 || len(key) < 1 || len(key) > 2560 {
		return errors.New("a referência ou a chave da API é inválida")
	}
	for _, value := range []string{target, key} {
		for _, character := range value {
			if character == 0 || character == '\r' || character == '\n' {
				return errors.New("a referência ou a chave da API é inválida")
			}
		}
	}
	return nil
}
