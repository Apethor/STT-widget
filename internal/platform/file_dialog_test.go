package platform

import "testing"

func TestFileDialogRejectsEmbeddedNULAndEmptyFilters(t *testing.T) {
	t.Parallel()
	for _, options := range []FileDialogOptions{
		{Title: "Importar\x00áudio"},
		{Folder: "C:\\folder\x00other"},
		{FileName: "text\x00.txt"},
		{DefaultExtension: "txt\x00"},
		{Filters: []FileFilter{{Name: "Audio", Pattern: ""}}},
		{Filters: []FileFilter{{Name: "", Pattern: "*.wav"}}},
		{Filters: []FileFilter{{Name: "Audio", Pattern: "*.wav\x00*.exe"}}},
	} {
		if options.validate() == nil {
			t.Fatalf("invalid native picker options were accepted: %+v", options)
		}
	}
}
