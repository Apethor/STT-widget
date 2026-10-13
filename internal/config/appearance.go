package config

import "errors"

const (
	ThemeGreen = "green"
	ThemeLight = "light"
	ThemeDark  = "dark"

	LayoutStacked = "stacked"
	LayoutInline  = "inline"
	LayoutColumn  = "column"

	RecordingLarge  = "large"
	RecordingMedium = "medium"
	RecordingSmall  = "small"
)

// Appearance changes the window without changing audio or text processing.
type Appearance struct {
	Theme         string `json:"theme"`
	Layout        string `json:"layout"`
	RecordingSize string `json:"recording_size"`
	FontFamily    string `json:"font_family,omitempty"`
	FontSize      int    `json:"font_size,omitempty"`
	InvertPanels  bool   `json:"invert_panels,omitempty"`
}

func DefaultAppearance() Appearance {
	return Appearance{
		Theme: ThemeDark, Layout: LayoutColumn, RecordingSize: RecordingLarge,
		FontFamily: "Segoe UI", FontSize: 14,
	}
}

func (a Appearance) EditorFontFamily() string {
	if a.FontFamily == "" {
		return "Segoe UI"
	}
	return a.FontFamily
}

func (a Appearance) EditorFontSize() int {
	if a.FontSize == 0 {
		return 14
	}
	return a.FontSize
}

func (a Appearance) Validate() error {
	switch a.Theme {
	case ThemeGreen, ThemeLight, ThemeDark:
	default:
		return errors.New("selecione o tema Claro, Escuro ou Verde")
	}
	switch a.Layout {
	case LayoutStacked, LayoutInline, LayoutColumn:
	default:
		return errors.New("selecione a disposição dos três boxes")
	}
	switch a.RecordingSize {
	case RecordingLarge, RecordingMedium, RecordingSmall:
	default:
		return errors.New("selecione o tamanho Pequeno, Médio ou Grande da gravação")
	}
	if a.FontFamily != "" && a.FontFamily != "Segoe UI" && a.FontFamily != "Arial" && a.FontFamily != "Consolas" {
		return errors.New("selecione Segoe UI, Arial ou Consolas para os editores")
	}
	if a.FontSize != 0 && (a.FontSize < 10 || a.FontSize > 32) {
		return errors.New("o tamanho base da fonte deve ficar entre 10 e 32")
	}
	return nil
}
