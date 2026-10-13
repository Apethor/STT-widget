package desktop

import (
	"image/color"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"ai-talker/internal/config"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

const (
	surfaceColor   = theme.ColorNameHeaderBackground
	borderColor    = theme.ColorNameInputBorder
	textColor      = theme.ColorNameForeground
	mutedColor     = theme.ColorNameDisabled
	accentColor    = theme.ColorNamePrimary
	recordingColor = theme.ColorNameError
)

type palette struct {
	background, surface, button, border color.NRGBA
	text, muted, accent, accentText     color.NRGBA
	error, errorText                    color.NRGBA
	variant                             fyne.ThemeVariant
}

func rgb(value uint32) color.NRGBA {
	return color.NRGBA{R: uint8(value >> 16), G: uint8(value >> 8), B: uint8(value), A: 255}
}

func paletteFor(mode string) *palette {
	switch mode {
	case config.ThemeLight:
		return &palette{
			background: rgb(0xf1f4f8), surface: rgb(0xffffff),
			button: rgb(0xe8edf5), border: rgb(0xcbd5e1),
			text: rgb(0x182335), muted: rgb(0x526277),
			accent: rgb(0x315fc4), accentText: rgb(0xffffff),
			error: rgb(0xb42318), errorText: rgb(0xffffff),
			variant: theme.VariantLight,
		}
	case config.ThemeDark:
		return &palette{
			background: rgb(0x13171e), surface: rgb(0x1d2430),
			button: rgb(0x2a3443), border: rgb(0x3b495b),
			text: rgb(0xecf1f8), muted: rgb(0xacb9cc),
			accent: rgb(0x9ab8ff), accentText: rgb(0x172442),
			error: rgb(0xffa39a), errorText: rgb(0x401812),
			variant: theme.VariantDark,
		}
	default:
		return &palette{
			background: rgb(0x111917), surface: rgb(0x192521),
			button: rgb(0x20312a), border: rgb(0x2d4138),
			text: rgb(0xedf5f0), muted: rgb(0xa5b8ae),
			accent: rgb(0x79deb5), accentText: rgb(0x143024),
			error: rgb(0xff897f), errorText: rgb(0x401812),
			variant: theme.VariantDark,
		}
	}
}

// A stable theme lets existing editor overrides follow palette changes. The
// immutable palette snapshot also supports reads by Fyne's renderers.
type talkerTheme struct {
	base    fyne.Theme
	fonts   map[fyne.TextStyle]fyne.Resource
	palette atomic.Pointer[palette]
}

func newTheme(mode string) *talkerTheme {
	t := &talkerTheme{base: theme.DefaultTheme(), fonts: make(map[fyne.TextStyle]fyne.Resource)}
	t.setMode(mode)
	fontDir := filepath.Join(os.Getenv("WINDIR"), "Fonts")
	if os.Getenv("WINDIR") == "" {
		return t
	}
	fonts := map[fyne.TextStyle]string{
		{}:                              "segoeui.ttf",
		{Bold: true}:                    "segoeuib.ttf",
		{Italic: true}:                  "segoeuii.ttf",
		{Bold: true, Italic: true}:      "segoeuiz.ttf",
		{Monospace: true}:               "consola.ttf",
		{Monospace: true, Bold: true}:   "consolab.ttf",
		{Monospace: true, Italic: true}: "consolai.ttf",
		{Monospace: true, Bold: true, Italic: true}: "consolaz.ttf",
	}
	for style, name := range fonts {
		payload, err := os.ReadFile(filepath.Join(fontDir, name))
		if err == nil {
			t.fonts[style] = fyne.NewStaticResource(name, payload)
		}
	}
	return t
}

func (t *talkerTheme) setMode(mode string) { t.palette.Store(paletteFor(mode)) }

func (t *talkerTheme) Color(name fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
	p := t.palette.Load()
	switch name {
	case theme.ColorNameBackground:
		return p.background
	case theme.ColorNameButton, theme.ColorNameInputBackground, theme.ColorNameDisabledButton:
		return p.button
	case theme.ColorNameForeground:
		return p.text
	case theme.ColorNameDisabled, theme.ColorNamePlaceHolder:
		return p.muted
	case theme.ColorNamePrimary, theme.ColorNameHyperlink:
		return p.accent
	case theme.ColorNameSuccess:
		if p.variant == theme.VariantLight {
			return rgb(0x168252)
		}
		return rgb(0x79deb5)
	case theme.ColorNameForegroundOnPrimary, theme.ColorNameForegroundOnSuccess:
		return p.accentText
	case theme.ColorNameForegroundOnError:
		return p.errorText
	case theme.ColorNameError:
		return p.error
	case theme.ColorNameInputBorder, theme.ColorNameSeparator, theme.ColorNameScrollBar:
		return p.border
	case theme.ColorNameHeaderBackground, theme.ColorNameMenuBackground, theme.ColorNameOverlayBackground:
		return p.surface
	case theme.ColorNameHover:
		hover := p.accent
		hover.A = 0x25
		return hover
	case theme.ColorNameFocus, theme.ColorNameSelection:
		selection := p.accent
		selection.A = 0x45
		return selection
	default:
		return t.base.Color(name, p.variant)
	}
}

func (t *talkerTheme) Font(style fyne.TextStyle) fyne.Resource {
	// Tabs and leading spacing are text attributes rather than font variants.
	key := fyne.TextStyle{Bold: style.Bold, Italic: style.Italic, Monospace: style.Monospace}
	if resource, ok := t.fonts[key]; ok {
		return resource
	}
	return t.base.Font(style)
}

func (t *talkerTheme) Icon(name fyne.ThemeIconName) fyne.Resource { return t.base.Icon(name) }

func (t *talkerTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNameText:
		return 13
	case theme.SizeNameCaptionText:
		return 11
	case theme.SizeNameHeadingText:
		return 23
	case theme.SizeNamePadding:
		return 8
	case theme.SizeNameInnerPadding:
		return 8
	case theme.SizeNameButtonRadius:
		return 10
	case theme.SizeNameInputRadius:
		return 9
	case theme.SizeNameInlineIcon:
		return 16
	case theme.SizeNameInputBorder:
		return 1
	case theme.SizeNameLineSpacing:
		return 2
	default:
		return t.base.Size(name)
	}
}

type headerTheme struct{ fyne.Theme }

func (t headerTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNamePadding:
		return 4
	case theme.SizeNameInnerPadding:
		return 6
	default:
		return t.Theme.Size(name)
	}
}

type volumeTheme struct{ fyne.Theme }

type tabScrollTheme struct{ fyne.Theme }

func (t tabScrollTheme) Size(name fyne.ThemeSizeName) float32 {
	if name == theme.SizeNameScrollBar || name == theme.SizeNameScrollBarSmall {
		return 3
	}
	return t.Theme.Size(name)
}

func (t tabScrollTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	if name == theme.ColorNameScrollBarBackground || name == theme.ColorNameShadow {
		return color.Transparent
	}
	return t.Theme.Color(name, variant)
}

func (t volumeTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	switch name {
	case theme.ColorNameForeground:
		return t.Theme.Color(accentColor, variant)
	case theme.ColorNameInputBackground:
		return t.Theme.Color(borderColor, variant)
	default:
		return t.Theme.Color(name, variant)
	}
}

func (t volumeTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNameInlineIcon:
		return 18
	case theme.SizeNameInputBorder:
		return 1.5
	case theme.SizeNamePadding, theme.SizeNameInnerPadding:
		return 4
	case theme.SizeNameText:
		return 11
	default:
		return t.Theme.Size(name)
	}
}

type editorTheme struct {
	fyne.Theme
	desktop    *desktop
	document   *document
	transcript bool
}

var editorFontCache sync.Map

func (t editorTheme) Font(style fyne.TextStyle) fyne.Resource {
	if t.desktop == nil {
		return t.Theme.Font(style)
	}
	family := t.desktop.settings.Appearance.EditorFontFamily()
	variant := 0
	if style.Bold {
		variant |= 1
	}
	if style.Italic {
		variant |= 2
	}
	key := family + ":" + string(rune('0'+variant))
	if resource, ok := editorFontCache.Load(key); ok {
		if font, ok := resource.(fyne.Resource); ok {
			return font
		}
	}
	filenames := map[string][4]string{
		"Segoe UI": {"segoeui.ttf", "segoeuib.ttf", "segoeuii.ttf", "segoeuiz.ttf"},
		"Arial":    {"arial.ttf", "arialbd.ttf", "ariali.ttf", "arialbi.ttf"},
		"Consolas": {"consola.ttf", "consolab.ttf", "consolai.ttf", "consolaz.ttf"},
	}
	name := filenames[family][variant]
	fontDir := filepath.Join(os.Getenv("WINDIR"), "Fonts")
	if os.Getenv("WINDIR") == "" {
		return t.Theme.Font(style)
	}
	payload, err := os.ReadFile(filepath.Join(fontDir, name))
	if err != nil {
		return t.Theme.Font(style)
	}
	font := fyne.NewStaticResource(name, payload)
	actual, _ := editorFontCache.LoadOrStore(key, font)
	return actual.(fyne.Resource)
}

func (t editorTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	switch name {
	case theme.ColorNameInputBackground:
		return t.Theme.Color(surfaceColor, variant)
	case theme.ColorNameInputBorder:
		return color.Transparent
	default:
		return t.Theme.Color(name, variant)
	}
}

func (t editorTheme) Size(name fyne.ThemeSizeName) float32 {
	if name == theme.SizeNameText && t.desktop != nil {
		size := float32(t.desktop.settings.Appearance.EditorFontSize())
		if t.document != nil {
			size *= float32(t.desktop.editorZoomValue(t.document, t.transcript)) / 100
		}
		return size
	}
	return t.Theme.Size(name)
}

type providerTheme struct{ fyne.Theme }

func (t providerTheme) Size(name fyne.ThemeSizeName) float32 {
	if name == theme.SizeNameInlineIcon {
		return 16
	}
	return t.Theme.Size(name)
}

type textTheme struct {
	fyne.Theme
	size float32
}

func (t textTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	if name == theme.ColorNameForeground {
		return t.Theme.Color(mutedColor, variant)
	}
	return t.Theme.Color(name, variant)
}

func (t textTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNameText:
		return t.size
	case theme.SizeNameInnerPadding, theme.SizeNamePadding:
		return 0
	default:
		return t.Theme.Size(name)
	}
}
