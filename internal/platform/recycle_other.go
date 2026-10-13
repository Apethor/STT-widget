//go:build !windows

package platform

import "context"

func Recycle(context.Context, []string) error { return ErrUnsupported }
