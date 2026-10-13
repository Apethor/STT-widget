package platform

import "errors"

// ErrScreenshotCanceled means the user pressed Escape/right-click or the
// application canceled selection. No image is persisted in this case.
var ErrScreenshotCanceled = errors.New("seleção de screenshot cancelada")
