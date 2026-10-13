// Package branding contains the application's original artwork.
package branding

import _ "embed"

// PNG is the desktop icon, embedded so the executable needs no asset directory.
//
//go:embed ai-talker.png
var PNG []byte

// ICO retains the original multi-size Windows artwork for native window icons.
//
//go:embed ai-talker.ico
var ICO []byte

// The title variants are rendered from the original SVG at its native size,
// preserving stroke widths before the desktop scales their pixels.
//
//go:embed ai-talker-title-light.png
var TitleLight []byte

//go:embed ai-talker-title-dark.png
var TitleDark []byte
