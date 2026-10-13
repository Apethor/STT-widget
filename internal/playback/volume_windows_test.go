//go:build windows && cgo && integration

package playback

import (
	"math"
	"os"
	"runtime"
	"testing"
)

func TestWindowsPlaybackSessionVolumeIntegration(t *testing.T) {
	if os.Getenv("AI_TALKER_PLAYBACK_TEST") != "1" {
		t.Skip("o volume nativo da sessão de teste é uma integração opcional")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	volume, err := openSessionVolume()
	if err != nil {
		t.Fatal(err)
	}
	defer volume.close()
	before, err := volume.get()
	if err != nil {
		t.Fatal(err)
	}
	defer volume.set(int(math.Round(float64(before) * 100)))
	for _, percent := range []int{27, 0, 100} {
		if err := volume.set(percent); err != nil {
			t.Fatal(err)
		}
		current, err := volume.get()
		if err != nil || math.Abs(float64(current)-float64(percent)/100) > 0.005 {
			t.Fatal("o volume da sessão não corresponde ao controle", percent, current, err)
		}
	}
	t.Log("Windows: controle nativo de volume da sessão do processo conferido, sem modificar o volume global.")
}
