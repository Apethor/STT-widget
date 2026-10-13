//go:build !windows || !cgo

package pcmoutput

import "errors"

func openDevice(string, int) (device, error) {
	return nil, errors.New("a reprodução de respostas requer Windows com WASAPI")
}
