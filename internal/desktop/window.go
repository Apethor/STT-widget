package desktop

import (
	"runtime"

	"fyne.io/fyne/v2"
	fynedesktop "fyne.io/fyne/v2/driver/desktop"
)

func desktopWindowTitle() string {
	return "STT-widget"
}

func newDesktopWindow(application fyne.App) fyne.Window {
	if runtime.GOOS == "windows" {
		if driver, ok := application.Driver().(fynedesktop.Driver); ok {
			// The driver must know there is no native caption. Removing it
			// only through Win32 leaves GLFW adding its former frame on every
			// resize requested after a popup or dialog changes the canvas.
			window := driver.CreateSplashWindow()
			window.SetTitle(desktopWindowTitle())
			return window
		}
	}
	return application.NewWindow(desktopWindowTitle())
}
