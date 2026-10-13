//go:build !windows

package platform

import "context"

func NewVideoOutline(ctx context.Context, region ScreenRegion) (VideoOutline, error) {
	if err := validateVideoOutline(ctx, region); err != nil {
		return nil, err
	}
	return nil, ErrUnsupportedVideoOutline
}
