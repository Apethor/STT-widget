package screenshot

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"time"

	"ai-talker/internal/store"
)

func encode(ctx context.Context, pixels image.Image) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if pixels == nil || pixels.Bounds().Empty() {
		return nil, errors.New("a captura não contém pixels")
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, pixels); err != nil {
		return nil, fmt.Errorf("preparar a captura PNG: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// SaveNew creates a unique timestamped PNG in the date's YYYY-MM-DD subfolder.
// Even identical clock values never overwrite another capture. A failed
// initial write removes its partial file.
func SaveNew(ctx context.Context, directory string, pixels image.Image, when time.Time) (path string, err error) {
	payload, err := encode(ctx, pixels)
	if err != nil {
		return "", err
	}
	directory = filepath.Join(directory, when.Format("2006-01-02"))
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", fmt.Errorf("criar a pasta de screenshots: %w", err)
	}
	name := "Screenshot_" + when.Format("2006-01-02_15-04-05.000000000")
	for suffix := 0; ; suffix++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		filename := name + ".png"
		if suffix > 0 {
			filename = fmt.Sprintf("%s_%d.png", name, suffix+1)
		}
		path = filepath.Join(directory, filename)
		file, openErr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(openErr, os.ErrExist) {
			continue
		}
		if openErr != nil {
			return "", fmt.Errorf("criar a captura: %w", openErr)
		}
		_, writeErr := file.Write(payload)
		if writeErr == nil {
			writeErr = file.Sync()
		}
		err = errors.Join(writeErr, file.Close())
		if err != nil {
			return "", errors.Join(fmt.Errorf("salvar a captura: %w", err), os.Remove(path))
		}
		return path, nil
	}
}

// Save replaces the same PNG atomically; a failed save keeps its previous bytes.
func Save(ctx context.Context, path string, pixels image.Image) error {
	payload, err := encode(ctx, pixels)
	if err != nil {
		return err
	}
	return store.Write(path, payload)
}
