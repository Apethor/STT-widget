//go:build windows && integration

package secrets

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestWindowsCredentialRoundTripReplacementAndDeletion(t *testing.T) {
	// A unique test target never reads or changes the user's provider keys.
	target := fmt.Sprintf("AI-Talker/Test/%d", time.Now().UnixNano())
	vault := Native{}
	t.Cleanup(func() {
		if err := vault.Delete(target); err != nil {
			t.Error(err)
		}
	})
	if err := vault.Write(target, "fictitious-first-key"); err != nil {
		t.Fatal(err)
	}
	key, err := vault.Read(target)
	if err != nil || key != "fictitious-first-key" {
		t.Fatal("o Windows não preservou a chave", err)
	}
	if err := vault.Write(target, "fictitious-replacement-key"); err != nil {
		t.Fatal(err)
	}
	key, err = vault.Read(target)
	if err != nil || key != "fictitious-replacement-key" {
		t.Fatal("a substituição não atualizou o armazenamento seguro", err)
	}
	if err := vault.Delete(target); err != nil {
		t.Fatal(err)
	}
	if _, err := vault.Read(target); !errors.Is(err, ErrNotFound) {
		t.Fatal("a chave removida continuou acessível", err)
	}
}
