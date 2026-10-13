//go:build !windows

package soundboard

import "errors"

func openRenderDevice() (renderDevice, error) {
	return nil, errors.New("a saída rápida do painel de sons requer Windows")
}
