package soundboard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestPublicEffectsMigrateSavedCatalogsWithoutChangingPersonalNamesOrFiles(t *testing.T) {
	for _, version := range []int{1, 2} {
		t.Run(fmt.Sprintf("version-%d", version), func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			directory := filepath.Join(root, "soundboard")
			if err := os.MkdirAll(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"mario-pipe.wav", "mario-star.wav", "custom-test.wav"} {
				if err := os.WriteFile(filepath.Join(directory, name), []byte("synthetic original"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			laughter := "laughter-0.12.mp3"
			if version == 1 {
				laughter = "laughter.mp3"
			}
			legacy := fmt.Sprintf(`{"version":%d,"volume":37,"window_position":{"x":-250,"y":320},"buttons":[
{"id":"applause","name":"Aplausos","file":"applause.mp3","builtin":true},
{"id":"laughter","name":"Risadas","file":%q,"builtin":true},
{"id":"mario-pipe","name":"Cano do Mario","file":"mario-pipe.wav","builtin":true},
{"id":"mario-star","name":"Minha estrela","file":"mario-star.wav","builtin":true,"renamed":true},
{"id":"rimshot","name":"Ba dum tss","file":"rimshot.wav","builtin":true},
{"id":"wow","name":"Uau!","file":"wow.wav","builtin":true},
{"id":"custom-test","name":"Impulso","file":"custom-test.wav"}]}`, version, laughter)
			catalogPath := filepath.Join(directory, "soundboard.json")
			if err := os.WriteFile(catalogPath, []byte(legacy), 0o600); err != nil {
				t.Fatal(err)
			}
			board, err := Load(root)
			if err != nil {
				t.Fatal(err)
			}
			buttons := board.Buttons()
			if len(buttons) != 7 || board.Volume() != 37 || board.Position() == nil ||
				*board.Position() != (Position{X: -250, Y: 320}) ||
				buttons[2].ID != "mario-pipe" || buttons[2].File != "impulse.wav" ||
				buttons[2].Name != "Impulso (1)" || buttons[2].Renamed ||
				buttons[3].ID != "mario-star" || buttons[3].File != "sparkle.wav" ||
				buttons[3].Name != "Minha estrela" || !buttons[3].Renamed ||
				buttons[6].Name != "Impulso" || buttons[6].Builtin {
				t.Fatal("migration changed saved preferences or failed to avoid a name collision:", buttons)
			}
			for _, name := range []string{"mario-pipe.wav", "mario-star.wav", "custom-test.wav"} {
				payload, err := os.ReadFile(filepath.Join(directory, name))
				if err != nil || string(payload) != "synthetic original" {
					t.Fatal("migration removed or changed an existing source:", name, err)
				}
			}
			for _, id := range []string{"mario-pipe", "mario-star"} {
				path, err := board.Path(id)
				if err != nil {
					t.Fatal(err)
				}
				payload, err := os.ReadFile(path)
				if err != nil || len(payload) < 44 || string(payload[:4]) != "RIFF" {
					t.Fatal("the replacement effect was not installed as a WAV:", id, err)
				}
			}
			payload, err := os.ReadFile(catalogPath)
			if err != nil {
				t.Fatal(err)
			}
			var saved manifest
			if err := json.Unmarshal(payload, &saved); err != nil || saved.Version != 3 {
				t.Fatal("migration was not persisted:", err)
			}
			reloaded, err := Load(root)
			if err != nil || !slices.Equal(reloaded.Buttons(), buttons) {
				t.Fatal("reopening changed the migrated catalog:", err)
			}
		})
	}
}
