//go:build windows

package playback

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	winmm           = windows.NewLazySystemDLL("winmm.dll")
	sendString      = winmm.NewProc("mciSendStringW")
	getErrorString  = winmm.NewProc("mciGetErrorStringW")
	nextOutputAlias atomic.Uint64
)

type waveOutput struct {
	alias  string
	volume *sessionVolume
}

func openOutput(path string) (output, error) {
	alias := fmt.Sprintf("aitalkerplay%d", nextOutputAlias.Add(1))
	if _, err := mci(fmt.Sprintf("open %q type waveaudio alias %s", path, alias)); err != nil {
		return nil, err
	}
	device := &waveOutput{alias: alias}
	if _, err := mci("set " + alias + " time format milliseconds"); err != nil {
		closeErr := device.Close()
		if closeErr != nil {
			return nil, fmt.Errorf("%w; liberar a saída: %v", err, closeErr)
		}
		return nil, err
	}
	return device, nil
}

// MCI's waveaudio driver rejects long filenames even through MCI_OPEN.
// Reserve the private WAV in a short temporary location when necessary,
// before copying/decoding, so cancellation still covers all preparation.
func createPlaybackFile(folder string) (*os.File, error) {
	short := func(path string) string {
		encoded, err := windows.UTF16PtrFromString(path)
		if err != nil {
			return path
		}
		if len(windows.StringToUTF16(path)) <= 100 {
			return path
		}
		buffer := make([]uint16, 32768)
		if length, err := windows.GetShortPathName(encoded, &buffer[0], uint32(len(buffer))); err == nil && length > 0 && length < uint32(len(buffer)) {
			return windows.UTF16ToString(buffer[:length])
		}
		return path
	}
	folder = short(folder)
	if len(windows.StringToUTF16(folder)) > 100 {
		folder = short(os.TempDir())
	}
	if len(windows.StringToUTF16(folder)) > 100 {
		return nil, errors.New("o caminho temporário do Windows é longo demais para reproduzir; use uma pasta de dados mais curta")
	}
	file, err := os.CreateTemp(folder, "audio-*.wav")
	if err != nil {
		return nil, err
	}
	return file, nil
}

func (d *waveOutput) Play() error {
	_, err := mci("play " + d.alias)
	return err
}

func (d *waveOutput) Pause() error {
	_, err := mci("pause " + d.alias + " wait")
	return err
}

func (d *waveOutput) Seek(seconds float64) error {
	_, err := mci(fmt.Sprintf("seek %s to %d wait", d.alias, int64(seconds*1000)))
	return err
}

func (d *waveOutput) SetVolume(volume int) error {
	if d.volume == nil {
		control, err := openSessionVolume()
		if err != nil {
			return err
		}
		d.volume = control
	}
	return d.volume.set(volume)
}

func (d *waveOutput) Status() (position, duration float64, playing bool, err error) {
	values := []*float64{&position, &duration}
	for index, item := range []string{"position", "length"} {
		reply, commandErr := mci("status " + d.alias + " " + item)
		if commandErr != nil {
			return 0, 0, false, commandErr
		}
		milliseconds, parseErr := strconv.ParseUint(reply, 10, 32)
		if parseErr != nil {
			return 0, 0, false, fmt.Errorf("ler a posição da reprodução: %w", parseErr)
		}
		*values[index] = float64(milliseconds) / 1000
	}
	mode, err := mci("status " + d.alias + " mode")
	return position, duration, mode == "playing", err
}

func (d *waveOutput) Close() error {
	if d.alias == "" {
		return nil
	}
	_, err := mci("close " + d.alias)
	d.alias = ""
	if d.volume != nil {
		err = errors.Join(err, d.volume.close())
		d.volume = nil
	}
	return err
}

func mci(command string) (string, error) {
	encoded, err := windows.UTF16PtrFromString(command)
	if err != nil {
		return "", fmt.Errorf("o caminho do áudio é inválido: %w", err)
	}
	var result [256]uint16
	code, _, _ := sendString.Call(uintptr(unsafe.Pointer(encoded)), uintptr(unsafe.Pointer(&result[0])), uintptr(len(result)), 0)
	runtime.KeepAlive(encoded)
	if err := mciError(code); err != nil {
		return "", err
	}
	return strings.TrimSpace(windows.UTF16ToString(result[:])), nil
}

func mciError(code uintptr) error {
	if code == 0 {
		return nil
	}
	var description [256]uint16
	ok, _, _ := getErrorString.Call(code, uintptr(unsafe.Pointer(&description[0])), uintptr(len(description)))
	message := fmt.Sprintf("erro de áudio do Windows %d", code)
	if ok != 0 {
		message = windows.UTF16ToString(description[:])
	}
	return fmt.Errorf("reproduzir o áudio: %s", message)
}
