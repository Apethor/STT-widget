package screenshot

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func readPNG(t *testing.T, path string) image.Image {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	pixels, err := png.Decode(file)
	if err != nil {
		t.Fatal(err)
	}
	return pixels
}

func TestSaveNewTimestampCollisionAndUpdateSamePNG(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "screenshots")
	when := time.Date(2026, 10, 6, 14, 23, 45, 123000000, time.FixedZone("BRT", -3*3600))
	original := testImage()
	first, err := SaveNew(t.Context(), directory, original, when)
	if err != nil {
		t.Fatal(err)
	}
	second, err := SaveNew(t.Context(), directory, original, when)
	if err != nil {
		t.Fatal(err)
	}
	if first == second || !strings.Contains(filepath.Base(first), "2026-10-06_14-23-45.123") {
		t.Fatalf("as capturas não têm data/hora e nomes únicos: %q, %q", first, second)
	}
	document, err := New(original)
	if err != nil {
		t.Fatal(err)
	}
	document.BeginStroke(Point{20, 20}, 8, color.NRGBA{R: 255, A: 255}, false)
	document.EndStroke()
	if err := Save(t.Context(), first, document.Snapshot()); err != nil {
		t.Fatal(err)
	}
	if readPNG(t, first).At(20, 20) == readPNG(t, second).At(20, 20) {
		t.Fatal("salvar marcações não atualizou o PNG ou mudou outra captura")
	}
	if filepath.Dir(first) != filepath.Join(directory, "2026-10-06") {
		t.Fatal("a captura não ficou organizada na pasta do dia")
	}
	entries, err := os.ReadDir(filepath.Dir(first))
	if err != nil || len(entries) != 2 {
		t.Fatalf("salvar deixou arquivos adicionais/temporários: %v, %v", entries, err)
	}
}

func TestSaveNewGroupsByLocalDateAndLeavesExistingFiles(t *testing.T) {
	directory := t.TempDir()
	legacy := filepath.Join(directory, "antiga.png")
	if err := os.WriteFile(legacy, []byte("arquivo antigo"), 0o600); err != nil {
		t.Fatal(err)
	}
	when := time.Date(2026, 10, 7, 23, 59, 59, 0, time.FixedZone("BRT", -3*3600))
	for _, date := range []struct {
		when time.Time
		day  string
	}{
		{when, "2026-10-07"},
		{when.Add(time.Second), "2026-10-08"},
	} {
		path, err := SaveNew(t.Context(), directory, testImage(), date.when)
		if err != nil || filepath.Dir(path) != filepath.Join(directory, date.day) {
			t.Fatal("a mudança de dia/localidade não criou a pasta correta:", path, err)
		}
		if err := Save(t.Context(), path, testImage()); err != nil {
			t.Fatal("atualizar a imagem mudou sua pasta:", err)
		}
	}
	if before, err := os.ReadFile(legacy); err != nil || string(before) != "arquivo antigo" {
		t.Fatal("a organização por data alterou um arquivo anterior")
	}
}

func TestCanceledSaveAndInvalidImageKeepPreviousFile(t *testing.T) {
	path, err := SaveNew(t.Context(), t.TempDir(), testImage(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := Save(ctx, path, testImage()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelar salvamento não interrompeu a operação: %v", err)
	}
	if err := Save(t.Context(), path, nil); err == nil {
		t.Fatal("imagem ausente não foi rejeitada")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("falhar ao salvar modificou o arquivo anterior: %v", err)
	}
	blocked := filepath.Join(t.TempDir(), "arquivo")
	if err := os.WriteFile(blocked, []byte("preservar"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveNew(t.Context(), filepath.Join(blocked, "screenshots"), testImage(), time.Now()); err == nil {
		t.Fatal("erro ao criar a pasta não foi reportado")
	}
}
