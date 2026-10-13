package video

import (
	"testing"
	"time"
)

func TestCaptureProgressWaitsForAFrameAndReadsSplitWritesOnlyOnce(t *testing.T) {
	t.Parallel()
	progress := newCaptureProgress()
	_, _ = progress.Write([]byte("frame=0\nout_time_us=0\nprogress=continue\n"))
	select {
	case <-progress.firstFrame:
		t.Fatal("o vídeo ficou pronto antes de existir um quadro")
	default:
	}
	before := time.Now().Add(-100 * time.Millisecond)
	_, _ = progress.Write([]byte("frame=   3\nout_time_"))
	_, _ = progress.Write([]byte("us=100000\nprogress=continue\n"))
	after := time.Now().Add(-100 * time.Millisecond)
	select {
	case first := <-progress.firstFrame:
		if first.Before(before) || first.After(after) {
			t.Fatalf("o primeiro quadro perdeu a relação com seu timestamp: %v", first)
		}
	default:
		t.Fatal("um quadro codificado não liberou o início da captura")
	}
	_, _ = progress.Write([]byte("frame=30\nout_time_us=1000000\nprogress=end\n"))
	select {
	case <-progress.firstFrame:
		t.Fatal("o progresso entregou mais de uma origem para a mesma captura")
	default:
	}
}
