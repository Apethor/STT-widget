package desktop

import (
	"ai-talker/internal/branding"
	"ai-talker/internal/config"

	"fyne.io/fyne/v2"
)

func headerIcon(mode string) fyne.Resource {
	artwork := branding.PNG
	switch mode {
	case config.ThemeLight:
		artwork = branding.TitleLight
	case config.ThemeDark:
		artwork = branding.TitleDark
	}
	return fyne.NewStaticResource("ai-talker-title-"+mode+".png", artwork)
}
