// Package store writes recoverable application data without truncating old files.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// A bounded set of locks serializes replacement of the same path. Concurrent
// MoveFileEx operations on Windows can otherwise reject one another.
var writeLocks [64]sync.Mutex

func WriteJSON(path string, value any) error {
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("preparar o arquivo JSON: %w", err)
	}
	return Write(path, append(payload, '\n'))
}

func Write(path string, payload []byte) (err error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("localizar o arquivo de dados: %w", err)
	}
	path = absolute
	hash := fnv.New32a()
	hash.Write([]byte(strings.ToLower(path)))
	mutex := &writeLocks[hash.Sum32()%uint32(len(writeLocks))]
	mutex.Lock()
	defer mutex.Unlock()
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("criar a pasta de dados: %w", err)
	}
	file, err := os.CreateTemp(directory, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("criar o arquivo temporário: %w", err)
	}
	temporary := file.Name()
	defer func() {
		if removeErr := os.Remove(temporary); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, fmt.Errorf("remover o arquivo temporário: %w", removeErr))
		}
	}()
	if _, err := file.Write(payload); err != nil {
		return errors.Join(fmt.Errorf("salvar os dados: %w", err), file.Close())
	}
	if err := file.Sync(); err != nil {
		return errors.Join(fmt.Errorf("confirmar a gravação no disco: %w", err), file.Close())
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("fechar o arquivo salvo: %w", err)
	}
	if err := replace(temporary, path); err != nil {
		return fmt.Errorf("substituir o arquivo salvo: %w", err)
	}
	return nil
}

func ReadJSON(path string, value any) error {
	payload, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(payload, value); err != nil {
		return fmt.Errorf("ler o arquivo JSON: %w", err)
	}
	return nil
}
