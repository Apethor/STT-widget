//go:build windows && integration

package platform

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestWindowsRecyclePreservesAndRestoresIsolatedFile(t *testing.T) {
	if os.Getenv("AI_TALKER_RECYCLE_TEST") != "1" {
		t.Skip("a Lixeira real é uma integração opcional com arquivo isolado")
	}
	directory := t.TempDir()
	source := filepath.Join(directory, "AI Talker conferência de Lixeira.txt")
	original := []byte("Arquivo sintético exclusivo deste teste.\n")
	if err := os.WriteFile(source, original, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if err := Recycle(ctx, []string{source}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(source); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("o arquivo não saiu da pasta de origem", err)
	}

	// Inspect only items whose original directory is this test's temporary
	// directory. Restoring the item proves recoverability, then t.TempDir
	// removes the restored test file during cleanup.
	script := `
$ErrorActionPreference = 'Stop'
$originalDirectory = [System.IO.Path]::GetDirectoryName($env:AI_TALKER_RECYCLE_PATH)
$shell = New-Object -ComObject Shell.Application
$items = @($shell.NameSpace(10).Items() | Where-Object {
    [string]::Equals([string]$_.ExtendedProperty('System.Recycle.DeletedFrom'),
        $originalDirectory, [System.StringComparison]::OrdinalIgnoreCase)
})
if ($items.Count -ne 1) { throw 'O arquivo isolado não foi encontrado na Lixeira.' }
$items[0].InvokeVerb('undelete')
`
	command := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-STA", "-Command", script)
	command.Env = append(os.Environ(), "AI_TALKER_RECYCLE_PATH="+source)
	Hide(command)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("restaurar o arquivo isolado da Lixeira: %v\n%s", err, output)
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		restored, err := os.ReadFile(source)
		if err == nil {
			if !bytes.Equal(restored, original) {
				t.Fatal("o conteúdo mudou depois de passar pela Lixeira")
			}
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
			t.Fatal("o Windows não restaurou o arquivo isolado", ctx.Err())
		case <-ticker.C:
		}
	}
}
