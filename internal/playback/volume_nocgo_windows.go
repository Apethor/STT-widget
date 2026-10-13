//go:build windows && !cgo

package playback

import "errors"

type sessionVolume struct{}

func openSessionVolume() (*sessionVolume, error) {
	return nil, errors.New("o volume de reprodução requer o pacote Windows com suporte nativo")
}
func (*sessionVolume) set(int) error { return nil }
func (*sessionVolume) close() error  { return nil }
