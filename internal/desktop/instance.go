package desktop

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"ai-talker/internal/platform"
	"ai-talker/internal/store"
	"ai-talker/internal/version"
)

type instanceIdentification struct {
	Version    string `json:"version"`
	Revision   string `json:"revision"`
	Executable string `json:"executable"`
	PID        int    `json:"pid"`
}

type guiInstance struct {
	lock            *platform.FileLock
	marker          string
	identification  string
	lastMarkerError string
}

func acquireInstance(directory string) (*guiInstance, error) {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("não foi possível criar a pasta do aplicativo: %w", err)
	}
	marker := filepath.Join(directory, "activate.request")
	lock, err := platform.TryLock(filepath.Join(directory, "gui.lock"))
	if errors.Is(err, platform.ErrLocked) {
		// The second process owns no Fyne window. The existing process consumes
		// this marker and calls its own native activation routine.
		file, writeErr := os.OpenFile(marker, os.O_CREATE|os.O_WRONLY, 0o600)
		if writeErr != nil {
			return nil, fmt.Errorf("não foi possível reabrir a janela existente: %w", writeErr)
		}
		if closeErr := file.Close(); closeErr != nil {
			return nil, fmt.Errorf("não foi possível solicitar a ativação da janela: %w", closeErr)
		}
		var running instanceIdentification
		if err := store.ReadJSON(filepath.Join(directory, "gui-instance.json"), &running); err != nil ||
			running.Version == "" || running.Revision == "" {
			return nil, fmt.Errorf("o STT-widget já está aberto, sem identificação de versão. Para abrir %s, feche a janela anterior e abra o aplicativo novamente", version.String())
		}
		if running.Version != version.Version || running.Revision != version.Revision {
			return nil, fmt.Errorf("o STT-widget %s (%s) já está aberto em %s. Para abrir %s, feche a janela anterior e abra o aplicativo novamente", running.Version, running.Revision, running.Executable, version.String())
		}
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("não foi possível verificar a janela existente: %w", err)
	}
	executable, err := os.Executable()
	if err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("não foi possível identificar o executável: %w", err)
	}
	identification := filepath.Join(directory, "gui-instance.json")
	if err := store.WriteJSON(identification, instanceIdentification{
		Version: version.Version, Revision: version.Revision, Executable: executable, PID: os.Getpid(),
	}); err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("não foi possível registrar a versão em execução: %w", err)
	}
	instance := &guiInstance{lock: lock, marker: marker, identification: identification}
	if _, err := instance.consumeActivation(); err != nil {
		instance.logActivationError(err)
	}
	return instance, nil
}

func (i *guiInstance) consumeActivation() (bool, error) {
	err := os.Remove(i.marker)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	case err != nil:
		return false, err
	default:
		i.lastMarkerError = ""
		return true, nil
	}
}

func (i *guiInstance) logActivationError(err error) {
	if err.Error() == i.lastMarkerError {
		return
	}
	i.lastMarkerError = err.Error()
	slog.Warn("a ativação da janela será tentada novamente", "error", err)
}

func (i *guiInstance) close() error {
	err := os.Remove(i.identification)
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	return errors.Join(err, i.lock.Close())
}
