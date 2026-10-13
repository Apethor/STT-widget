package desktop

import (
	"path/filepath"
	"testing"
	"time"

	"ai-talker/internal/config"

	"fyne.io/fyne/v2/test"
)

func TestNewRecordingsUseLocalDayForEveryCaptureSource(t *testing.T) {
	for _, mode := range []string{config.CaptureMicrophone, config.CaptureSystem, config.CaptureTeam} {
		t.Run(mode, func(t *testing.T) {
			now := time.Date(2026, 10, 7, 23, 59, 59, 0, time.FixedZone("BRT", -3*3600))
			var folder string
			h := newHarness(t, nil, func(deps *dependencies) {
				deps.now = func() time.Time { return now }
				deps.newConfiguredRecorder = func(settings config.Settings, directory string) recording {
					if settings.Capture.SourceMode() != mode {
						t.Fatal("a escolha da fonte foi perdida")
					}
					folder = directory
					return &fakeRecording{}
				}
			})
			h.d.settings.Capture.Mode = mode
			for _, day := range []string{"2026-10-07", "2026-10-08"} {
				test.Tap(h.d.recordButton)
				h.await(func() bool { return h.d.capture == captureRunning })
				if folder != filepath.Join(h.d.directory, "gravacoes", day) {
					t.Fatal("a gravação não usa a data local de início:", folder)
				}
				now = now.Add(time.Second)
				// Crossing midnight during capture keeps the original directory.
				if filepath.Base(folder) != day {
					t.Fatal("uma gravação em andamento foi movida na virada do dia")
				}
				h.d.stopCapture(keepCapture)
				h.await(func() bool { return h.d.capture == captureIdle })
				h.settle()
			}
		})
	}
}

func TestDatedAndLegacyScreenshotPathsRemainClickable(t *testing.T) {
	h := newHarness(t, nil, nil)
	root := filepath.Join(h.d.directory, "screenshots")
	legacy := filepath.Join(root, "antiga.png")
	dated := filepath.Join(root, "2026-10-07", "nova.png")
	h.d.screenshotFiles = []string{
		legacy, dated,
		filepath.Join(root, "2026-99-99", "data-invalida.png"),
		filepath.Join(root, "outros", "fora.png"),
		filepath.Join(root, "2026-10-07", "outra-pasta", "fora.png"),
		filepath.Join(root, "..", "fora.png"),
	}
	links := h.d.screenshotFileLinks(h.d.document)
	if len(links) != 2 {
		t.Fatal("os links perderam uma captura válida ou aceitaram uma pasta estranha:", links)
	}
	found := map[string]bool{}
	for _, path := range links {
		found[path] = true
	}
	if !found[legacy] || !found[dated] {
		t.Fatal("o caminho de uma captura registrada foi alterado")
	}
	h.d.editor.SetText("Antes.\n" + dated + "\nDepois.")
	h.d.updateTranscriptLinks(h.d.document, true)
	if !h.d.transcriptReading || len(collectLinks(h.d.transcriptPreview.Segments)) != 1 {
		t.Fatal("a screenshot na pasta por dia deixou de aparecer como hyperlink")
	}
}
