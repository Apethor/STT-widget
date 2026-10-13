package store

import (
	"errors"
	"time"

	"golang.org/x/sys/windows"
)

func replace(source, destination string) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	for attempt := 0; ; attempt++ {
		err := windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
		if err == nil {
			return nil
		}
		isBusy := errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_ACCESS_DENIED)
		if !isBusy || attempt >= 4 {
			return err
		}
		// Readers such as an indexer may briefly deny replacement after close.
		time.Sleep(time.Duration(attempt+1) * 10 * time.Millisecond)
	}
}
