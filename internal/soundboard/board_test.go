package soundboard

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ai-talker/internal/audio"
)

func TestBoardLoadsBundledEffectsAndKeepsCustomSoundInsideItsFolder(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	board, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if buttons := board.Buttons(); len(buttons) != 6 ||
		buttons[0].Name != "Aplausos" || buttons[1].Name != "Risadas" ||
		board.Volume() != 100 {
		t.Fatalf("os sons iniciais da botoneira estão incompletos: %+v", buttons)
	}
	for _, button := range board.Buttons() {
		path, err := board.Path(button.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(filepath.Clean(path), filepath.Join(root, "soundboard")+string(os.PathSeparator)) {
			t.Fatalf("o som incluído escapou da pasta de dados: %q", path)
		}
		if info, err := os.Stat(path); err != nil || info.Size() == 0 {
			t.Fatalf("o asset padrão não foi copiado: %q %v", path, err)
		}
	}

	source := filepath.Join(root, "outside.mp3")
	if err := os.WriteFile(source, []byte("not modified by the board"), 0o600); err != nil {
		t.Fatal(err)
	}
	custom, err := board.Add(source, "Introdução")
	if err != nil {
		t.Fatal(err)
	}
	customPath, err := board.Path(custom.ID)
	if err != nil || customPath == source || filepath.Dir(customPath) != filepath.Join(root, "soundboard") {
		t.Fatalf("o arquivo pessoal não foi copiado para a pasta privada: %q %v", customPath, err)
	}
	if err := board.Rename(custom.ID, "Vinheta"); err != nil {
		t.Fatal(err)
	}
	if err := board.SetVolume(36); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Volume() != 36 || len(reloaded.Buttons()) != 7 || reloaded.Buttons()[6].Name != "Vinheta" {
		t.Fatalf("a lista ou o volume não persistiram: %+v", reloaded)
	}
	if err := reloaded.Remove(custom.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := reloaded.Path(custom.ID); err == nil {
		t.Fatal("o botão removido ainda está acessível")
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("remover o botão também apagou o arquivo original: %v", err)
	}
}

func TestBoardMigratesOldEffectsPreservingCustomNamesVolumeAndFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	directory := filepath.Join(root, "soundboard")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"applause.mp3", "laughter.mp3", "custom-test.wav"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("original preserved"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := `{"version":1,"volume":39,"buttons":[{"id":"applause","name":"My applause","file":"applause.mp3","builtin":true},{"id":"laughter","name":"Risadas","file":"laughter.mp3","builtin":true},{"id":"custom-test","name":"Cano do Mario","file":"custom-test.wav"}]}`
	if err := os.WriteFile(filepath.Join(directory, "soundboard.json"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	board, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	buttons := board.Buttons()
	if len(buttons) != 7 || board.Volume() != 39 || buttons[0].Name != "My applause" ||
		!buttons[0].Renamed || buttons[1].File != "laughter-0.12.mp3" ||
		buttons[2].Name != "Cano do Mario" || buttons[3].Name == "Cano do Mario" {
		t.Fatal("migration replaced custom preferences:", buttons)
	}
	for _, name := range []string{"applause.mp3", "laughter.mp3", "custom-test.wav"} {
		bytes, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil || string(bytes) != "original preserved" {
			t.Fatal("migration changed or removed an existing source:", name, err)
		}
	}
	if err := board.SetPosition(Position{X: -600, Y: 233}); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Load(root)
	if err != nil || reloaded.Position() == nil || *reloaded.Position() != (Position{X: -600, Y: 233}) ||
		len(reloaded.Buttons()) != 7 {
		t.Fatal("position did not survive reopening or migration duplicated defaults:", err)
	}
	if err := reloaded.Rename("laughter", "Risadas"); err != nil {
		t.Fatal(err)
	}
	if _, translate := DefaultName(reloaded.Buttons()[1]); translate {
		t.Fatal("explicitly renamed button is still translated")
	}
}

func TestBoardRejectsUnsafeNamesExtensionsAndVolume(t *testing.T) {
	t.Parallel()
	board, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "sample.wav")
	if err := os.WriteFile(source, []byte{1, 2, 3, 4}, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", "../som", strings.Repeat("x", maxNameRunes+1), "linha\nnova"} {
		if _, err := board.Add(source, name); err == nil {
			t.Fatalf("um nome inválido foi aceito: %q", name)
		}
	}
	if err := board.SetVolume(101); err == nil {
		t.Fatal("o volume passou de 100%")
	}
	builtin := board.Buttons()[0]
	if err := board.Remove(builtin.ID); err == nil {
		t.Fatal("um som padrão foi removido")
	}
	unsupported := filepath.Join(t.TempDir(), "sample.ogg")
	if err := os.WriteFile(unsupported, []byte{1, 2, 3}, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := board.Add(unsupported, "Outro"); err == nil {
		t.Fatal("o formato OGG não foi recusado")
	}
}

func TestBoardReloadKeepsUnavailableCustomAndAllowsPreparingValidImport(t *testing.T) {
	t.Parallel()
	for _, unavailable := range []string{"missing", "empty", "directory"} {
		t.Run(unavailable, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			board, err := Load(root)
			if err != nil {
				t.Fatal(err)
			}
			wav, err := audio.EncodeWAV(t.Context(), []byte{100, 0, 156, 255})
			if err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(root, "original.wav")
			if err := os.WriteFile(source, wav, 0o600); err != nil {
				t.Fatal(err)
			}
			legacy, err := board.Add(source, "My legacy effect")
			if err != nil {
				t.Fatal(err)
			}
			path, err := board.Path(legacy.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			switch unavailable {
			case "empty":
				if err := os.WriteFile(path, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			reloaded, err := Load(root)
			if err != nil {
				t.Fatalf("unavailable custom blocked the catalogue: %v", err)
			}
			buttons := reloaded.Buttons()
			if len(buttons) != 7 || buttons[6] != legacy {
				t.Fatal("custom inventory was discarded or renamed:", buttons)
			}
			if _, err := reloaded.Path(legacy.ID); err == nil {
				t.Fatal("an unavailable or irregular file was allowed for preparation")
			}
			imported, err := reloaded.Add(source, "New valid effect")
			if err != nil {
				t.Fatal(err)
			}
			engine, _ := readyTestEngine(t)
			engine.decode = func(_ context.Context, path string) ([]int16, error) {
				if _, err := os.ReadFile(path); err != nil {
					return nil, err
				}
				return []int16{1000, 1000, -1000, -1000}, nil
			}
			for _, button := range reloaded.Buttons() {
				path, err := reloaded.Path(button.ID)
				if err != nil {
					if button.ID != legacy.ID {
						t.Fatal(err)
					}
					continue
				}
				if err := engine.Prepare(t.Context(), button.ID, path); err != nil {
					t.Fatal(err)
				}
				if !engine.Ready(button.ID) {
					t.Fatal("a valid builtin or import did not become ready:", button.ID)
				}
			}
			if engine.Ready(legacy.ID) || !engine.Ready(imported.ID) {
				t.Fatal("the unavailable custom blocked the new valid import")
			}
			if original, err := os.ReadFile(source); err != nil || string(original) != string(wav) {
				t.Fatal("the external original was modified:", err)
			}
			if unavailable != "missing" {
				if _, err := os.Lstat(path); err != nil {
					t.Fatal("the unavailable file or directory was removed:", err)
				}
			}
		})
	}
}

func TestBoardStillRejectsIrregularBuiltinFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	board, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	path, err := board.Path("applause")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root); err == nil {
		t.Fatal("an empty builtin bypassed regular-file validation")
	}
}
