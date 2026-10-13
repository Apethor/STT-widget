package platform

import (
	"context"
	"errors"
	"testing"
)

func TestScreenshotCanceledContextDoesNotOpenSelection(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	pixels, err := SelectScreenshot(ctx)
	if pixels != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("a seleção cancelada retornou imagem ou outro erro: %v, %v", pixels, err)
	}
}
