package whisper

import (
	"errors"

	"golang.org/x/sys/windows"
)

func artifactBusy(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_SHARING_VIOLATION)
}
