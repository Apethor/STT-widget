package platform

import (
	"errors"
	"strings"
)

// FileDialogOptions describes one Windows file selection. Cancellation returns
// an empty path; selecting a path does not open or truncate the file.
type FileDialogOptions struct {
	Title            string
	Folder           string
	FileName         string
	DefaultExtension string
	Save             bool
	NewestFirst      bool
	Filters          []FileFilter
}

type FileFilter struct {
	Name    string
	Pattern string
}

func (options FileDialogOptions) validate() error {
	values := []string{options.Title, options.Folder, options.FileName, options.DefaultExtension}
	for _, filter := range options.Filters {
		if filter.Name == "" || filter.Pattern == "" {
			return errors.New("filtro de arquivos inválido")
		}
		values = append(values, filter.Name, filter.Pattern)
	}
	for _, value := range values {
		if strings.ContainsRune(value, '\x00') {
			return errors.New("nome ou caminho de arquivo inválido")
		}
	}
	return nil
}
