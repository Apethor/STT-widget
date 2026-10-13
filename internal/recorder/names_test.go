package recorder

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestRecordingNamesStayCompactDistinctAndPreserveEveryOriginal(t *testing.T) {
	directory := t.TempDir()
	pattern := regexp.MustCompile(`^\d{8}-\d{6}(-(mic|sis))?(-\d+)?\.wav$`)
	saved := make(map[string]string)
	for index := range 30 {
		source := []string{"", "mic", "sis"}[index%3]
		file, path, err := createSourceRecordingFile(directory, source)
		if err != nil {
			t.Fatal(err)
		}
		content := fmt.Sprintf("áudio preservado %d", index)
		if _, err := file.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		if _, duplicate := saved[path]; duplicate || !pattern.MatchString(filepath.Base(path)) {
			t.Fatal("a colisão sobrescreveu uma captura ou produziu nome longo", path)
		}
		saved[path] = content
	}
	for path, content := range saved {
		payload, err := os.ReadFile(path)
		if err != nil || string(payload) != content {
			t.Fatal("outra captura substituiu um áudio anterior", path, err)
		}
	}
}
