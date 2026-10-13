package platform

import (
	"context"
	"errors"
)

var ErrUnsupportedVideoOutline = errors.New("a borda da captura de vídeo requer Windows")

// VideoOutline is a thin, click-through native border around ScreenRegion.
// Screen-edge pixels use Windows capture exclusion; other edges sit outside
// the selected pixels. Close waits until its native window has been removed.
type VideoOutline interface {
	SetPaused(bool) error
	Close() error
}

func validateVideoOutline(ctx context.Context, region ScreenRegion) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if region.Width < 2 || region.Height < 2 || region.Width > 16384 || region.Height > 16384 ||
		region.X < -16384 || region.Y < -16384 || region.X > 16384 || region.Y > 16384 {
		return errors.New("a área da borda de vídeo é inválida")
	}
	return nil
}
