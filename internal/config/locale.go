package config

import "errors"

const (
	InterfacePortuguese = "pt-BR"
	InterfaceEnglish    = "en"
)

func (s Settings) InterfaceLocale() string {
	if s.InterfaceLanguage == InterfaceEnglish {
		return InterfaceEnglish
	}
	return InterfacePortuguese
}

func validateInterfaceLanguage(language string) error {
	if language != "" && language != InterfacePortuguese && language != InterfaceEnglish {
		return errors.New("selecione Português (Brasil) ou Inglês para a interface")
	}
	return nil
}
