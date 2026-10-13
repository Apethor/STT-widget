package platform

import "testing"

func TestIntegratedFrameHitTestKeepsNativeDragResizeAndClientArea(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		x, y int
		want int
	}{
		{"canto superior esquerdo", 1, 1, 13},
		{"canto superior direito", 479, 1, 14},
		{"canto inferior esquerdo", 1, 899, 16},
		{"canto inferior direito", 479, 899, 17},
		{"borda esquerda", 1, 100, 10},
		{"borda direita", 479, 100, 11},
		{"borda superior", 100, 1, 12},
		{"borda inferior", 100, 899, 15},
		{"arrastar", 100, 20, 2},
		{"configuração", 360, 20, 1},
		{"minimizar no canvas", 405, 20, 1},
		{"fechar no canvas", 455, 20, 1},
		{"depois do cabeçalho", 100, 40, 1},
		{"canvas", 100, 80, 1},
		{"fora", -1, 20, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := frameHitTest(tc.x, tc.y, 480, 900, 8, WindowHeader{Top: 8, Bottom: 36, ControlsWidth: 220}); got != tc.want {
				t.Fatalf("hit test = %d; esperado %d", got, tc.want)
			}
		})
	}
	if got := frameHitTest(100, 20, 480, 900, 0, WindowHeader{Top: 8, Bottom: 36, ControlsWidth: 220}); got != 2 {
		t.Fatalf("a janela maximizada não deve oferecer redimensionamento pela borda: %d", got)
	}
}
