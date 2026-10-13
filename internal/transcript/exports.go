package transcript

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ai-talker/internal/store"
)

func SaveExports(result *Result, directory string) error {
	destination, err := filepath.Abs(directory)
	if err != nil {
		return fmt.Errorf("localizar a pasta de exportação: %w", err)
	}
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return fmt.Errorf("criar a pasta de exportação: %w", err)
	}
	stem := strings.TrimSuffix(filepath.Base(result.Source), filepath.Ext(result.Source))
	runes := []rune(stem)
	if len(runes) > 100 {
		stem = string(runes[:100])
	}
	base := filepath.Join(destination, stem+"-"+time.Now().Format("20060102-150405.000000000"))
	exports := make(map[string]string, 3)
	for _, extension := range []string{"txt", "md", "json"} {
		exports[extension] = base + "." + extension
	}
	if err := store.Write(exports["txt"], []byte(result.Text+"\n")); err != nil {
		return fmt.Errorf("exportar o texto: %w", err)
	}
	markdown := fmt.Sprintf("# Transcrição — %s\n\nÁudio: %.2f minutos. "+
		"Processamento: %.1f segundos. Modelo: `%s`.\n\n",
		filepath.Base(result.Source), result.DurationSeconds/60, result.ElapsedSeconds, result.ModelID)
	if result.NeedsReview {
		markdown += "> Transcrição para conferência: há partes com resposta vazia ou incompatível com a duração. Compare o texto com o áudio antes de usar.\n\n"
	}
	markdown += result.Text + "\n"
	if err := store.Write(exports["md"], []byte(markdown)); err != nil {
		return fmt.Errorf("exportar o Markdown: %w", err)
	}
	result.Exports = exports
	if err := store.WriteJSON(exports["json"], result); err != nil {
		return fmt.Errorf("exportar os detalhes da transcrição: %w", err)
	}
	return nil
}
