package platform

import "testing"

func TestNativeVideoOutlineLogicalPixelThicknessFollowsDPI(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		dpi  uint32
		want int
	}{
		{name: "escala padrão", dpi: 96, want: 1},
		{name: "escala 125%", dpi: 120, want: 1},
		{name: "escala 150%", dpi: 144, want: 2},
		{name: "escala 200%", dpi: 192, want: 2},
		{name: "escala 300%", dpi: 288, want: 3},
		{name: "dpi não informado", dpi: 0, want: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := videoOutlineThickness(tc.dpi); got != tc.want {
				t.Fatalf("a borda perdeu um pixel lógico na escala %d: %d, esperado %d", tc.dpi, got, tc.want)
			}
		})
	}
}
