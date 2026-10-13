package platform

import (
	"context"
	"errors"
	"testing"
)

func TestVideoOutlineCanceledContextDoesNotCreateNativeWindow(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	outline, err := NewVideoOutline(ctx, ScreenRegion{Width: 640, Height: 480})
	if outline != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("a borda foi aberta depois do cancelamento", outline, err)
	}
}

func TestVideoOutlineRejectsInvalidRegion(t *testing.T) {
	t.Parallel()
	outline, err := NewVideoOutline(t.Context(), ScreenRegion{Width: 0, Height: 480})
	if outline != nil || err == nil {
		t.Fatal("a borda aceitou um recorte inválido", outline, err)
	}
}
