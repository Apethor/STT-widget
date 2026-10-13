// Package soundboard stores playable effects separately from recordings.
package soundboard

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"ai-talker/internal/store"
)

const (
	maxButtons   = 44
	maxNameRunes = 48
	maxAudioSize = 50 * 1024 * 1024
)

//go:embed assets/applause.mp3 assets/laughter-0.12.mp3 assets/*.wav
var defaultAssets embed.FS

type Button struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	File    string `json:"file"`
	Builtin bool   `json:"builtin,omitempty"`
	Renamed bool   `json:"renamed,omitempty"`
}

// Position stores physical screen coordinates, including negative coordinates
// on a monitor to the left of the primary display.
type Position struct {
	X int `json:"x"`
	Y int `json:"y"`
}

type manifest struct {
	Version  int       `json:"version"`
	Volume   int       `json:"volume"`
	Buttons  []Button  `json:"buttons"`
	Position *Position `json:"window_position,omitempty"`
}

type Board struct {
	mu        sync.Mutex
	directory string
	data      manifest
}

var defaults = []Button{
	{ID: "applause", Name: "Aplausos", File: "applause.mp3", Builtin: true},
	{ID: "laughter", Name: "Risadas", File: "laughter-0.12.mp3", Builtin: true},
	{ID: "mario-pipe", Name: "Impulso", File: "impulse.wav", Builtin: true},
	{ID: "mario-star", Name: "Brilho", File: "sparkle.wav", Builtin: true},
	{ID: "rimshot", Name: "Ba dum tss", File: "rimshot.wav", Builtin: true},
	{ID: "wow", Name: "Uau!", File: "wow.wav", Builtin: true},
}

// IDs remain stable for saved catalogs. Legacy files stay in the person's data
// directory; only the built-in catalog is moved to the public-domain effects.
var legacyEffects = map[string]struct{ name, file string }{
	"mario-pipe": {name: "Cano do Mario", file: "mario-pipe.wav"},
	"mario-star": {name: "Estrelinha do Mario", file: "mario-star.wav"},
}

func Load(dataDirectory string) (*Board, error) {
	root, err := filepath.Abs(filepath.Join(dataDirectory, "soundboard"))
	if err != nil {
		return nil, fmt.Errorf("localizar a botoneira: %w", err)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("preparar a pasta da botoneira: %w", err)
	}
	board := &Board{directory: root}
	path := filepath.Join(root, "soundboard.json")
	if err := store.ReadJSON(path, &board.data); errors.Is(err, os.ErrNotExist) {
		board.data = manifest{Version: 3, Volume: 100, Buttons: slices.Clone(defaults)}
		if err := board.saveLocked(); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, fmt.Errorf("ler a botoneira: %w", err)
	}
	if err := board.validateLocked(); err != nil {
		return nil, err
	}
	if board.data.Version == 1 {
		// The replacement gets a new filename, preserving the old source file
		// and every custom sound, name and volume.
		for index := range board.data.Buttons {
			button := &board.data.Buttons[index]
			if button.ID == "laughter" && button.Builtin && button.File == "laughter.mp3" {
				button.File = "laughter-0.12.mp3"
			}
			for _, original := range defaults {
				originalName := original.Name
				if legacy, ok := legacyEffects[original.ID]; ok {
					originalName = legacy.name
				}
				if original.ID == button.ID && button.Name != originalName {
					button.Renamed = true
				}
			}
		}
		for _, item := range defaults {
			if !slices.ContainsFunc(board.data.Buttons, func(button Button) bool { return button.ID == item.ID }) {
				if len(board.data.Buttons) == maxButtons {
					return nil, errors.New("libere espaço no painel de sons para incluir os quatro efeitos novos")
				}
				item.Name = board.builtinNameLocked(item.Name, item.ID)
				board.data.Buttons = append(board.data.Buttons, item)
			}
		}
		board.data.Version = 2
		if err := board.saveLocked(); err != nil {
			return nil, err
		}
	}
	if board.data.Version == 2 {
		for index := range board.data.Buttons {
			button := &board.data.Buttons[index]
			legacy, ok := legacyEffects[button.ID]
			if !button.Builtin || !ok {
				continue
			}
			for _, current := range defaults {
				if current.ID != button.ID {
					continue
				}
				button.File = current.File
				if !button.Renamed && button.Name == legacy.name {
					button.Name = board.builtinNameLocked(current.Name, button.ID)
				}
				break
			}
		}
		board.data.Version = 3
		if err := board.saveLocked(); err != nil {
			return nil, err
		}
	}
	if err := board.validateLocked(); err != nil {
		return nil, err
	}
	for _, asset := range defaults {
		if err := board.ensureBuiltinLocked(asset.File); err != nil {
			return nil, err
		}
	}
	for _, button := range board.data.Buttons {
		// Preserve an unavailable custom entry for diagnosis/removal. Path
		// still rejects unsafe or unreadable files before preparing audio.
		if _, err := board.resolveLocked(button); err != nil && button.Builtin {
			return nil, err
		}
	}
	return board, nil
}

func (b *Board) Position() *Position {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.data.Position == nil {
		return nil
	}
	result := *b.data.Position
	return &result
}

func (b *Board) SetPosition(position Position) error {
	if position.X < -100000 || position.X > 100000 || position.Y < -100000 || position.Y > 100000 {
		return errors.New("a posição do painel de sons é inválida")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.data.Position != nil && *b.data.Position == position {
		return nil
	}
	previous := b.data.Position
	b.data.Position = &position
	if err := b.saveLocked(); err != nil {
		b.data.Position = previous
		return err
	}
	return nil
}

// DefaultName identifies labels that can follow the interface language. An
// explicitly renamed button retains the person's text in every language.
func DefaultName(button Button) (string, bool) {
	if !button.Builtin || button.Renamed {
		return button.Name, false
	}
	for _, item := range defaults {
		if item.ID == button.ID && button.Name == item.Name {
			return item.Name, true
		}
	}
	return button.Name, false
}

func (b *Board) Buttons() []Button {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.data.Buttons)
}

func (b *Board) Volume() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.Volume
}

func (b *Board) SetVolume(volume int) error {
	if volume < 0 || volume > 100 {
		return errors.New("o volume da botoneira deve ficar entre 0 e 100%")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	previous := b.data.Volume
	b.data.Volume = volume
	if err := b.saveLocked(); err != nil {
		b.data.Volume = previous
		return err
	}
	return nil
}

func (b *Board) Path(id string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, button := range b.data.Buttons {
		if button.ID == id {
			return b.resolveLocked(button)
		}
	}
	return "", errors.New("o botão de som não está salvo")
}

func (b *Board) Add(source, name string) (Button, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.data.Buttons) >= maxButtons {
		return Button{}, fmt.Errorf("a botoneira aceita até %d sons", maxButtons)
	}
	name, err := validName(name)
	if err != nil {
		return Button{}, err
	}
	for _, button := range b.data.Buttons {
		if strings.EqualFold(button.Name, name) {
			return Button{}, errors.New("já existe um botão com esse nome")
		}
	}
	extension := strings.ToLower(filepath.Ext(source))
	if extension != ".wav" && extension != ".mp3" {
		return Button{}, errors.New("selecione um arquivo WAV ou MP3")
	}
	details, err := os.Stat(source)
	if err != nil || !details.Mode().IsRegular() || details.Size() < 1 || details.Size() > maxAudioSize {
		return Button{}, errors.New("o som deve ser um arquivo WAV ou MP3 de até 50 MB")
	}
	identifier, err := newID()
	if err != nil {
		return Button{}, err
	}
	button := Button{ID: "custom-" + identifier, Name: name, File: "custom-" + identifier + extension}
	if err := b.copyCustomLocked(source, button.File); err != nil {
		return Button{}, err
	}
	b.data.Buttons = append(b.data.Buttons, button)
	if err := b.saveLocked(); err != nil {
		b.data.Buttons = b.data.Buttons[:len(b.data.Buttons)-1]
		_ = os.Remove(filepath.Join(b.directory, button.File))
		return Button{}, err
	}
	return button, nil
}

func (b *Board) Rename(id, name string) error {
	name, err := validName(name)
	if err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	index := slices.IndexFunc(b.data.Buttons, func(button Button) bool { return button.ID == id })
	if index < 0 {
		return errors.New("o botão de som não está salvo")
	}
	for position, button := range b.data.Buttons {
		if position != index && strings.EqualFold(button.Name, name) {
			return errors.New("já existe um botão com esse nome")
		}
	}
	previous, renamed := b.data.Buttons[index].Name, b.data.Buttons[index].Renamed
	b.data.Buttons[index].Name = name
	b.data.Buttons[index].Renamed = true
	if err := b.saveLocked(); err != nil {
		b.data.Buttons[index].Name = previous
		b.data.Buttons[index].Renamed = renamed
		return err
	}
	return nil
}

func (b *Board) Remove(id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	index := slices.IndexFunc(b.data.Buttons, func(button Button) bool { return button.ID == id })
	if index < 0 {
		return errors.New("o botão de som não está salvo")
	}
	button := b.data.Buttons[index]
	if button.Builtin {
		return errors.New("os sons incluídos no app não podem ser removidos")
	}
	b.data.Buttons = slices.Delete(b.data.Buttons, index, index+1)
	if err := b.saveLocked(); err != nil {
		b.data.Buttons = slices.Insert(b.data.Buttons, index, button)
		return err
	}
	if err := os.Remove(filepath.Join(b.directory, button.File)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remover o arquivo do botão: %w", err)
	}
	return nil
}

func (b *Board) validateLocked() error {
	if (b.data.Version < 1 || b.data.Version > 3) || b.data.Volume < 0 || b.data.Volume > 100 ||
		len(b.data.Buttons) == 0 || len(b.data.Buttons) > maxButtons {
		return errors.New("o arquivo salvo da botoneira é inválido")
	}
	if position := b.data.Position; position != nil &&
		(position.X < -100000 || position.X > 100000 || position.Y < -100000 || position.Y > 100000) {
		return errors.New("a posição salva do painel de sons é inválida")
	}
	ids, names, files := make(map[string]bool), make(map[string]bool), make(map[string]bool)
	for _, button := range b.data.Buttons {
		name, err := validName(button.Name)
		if err != nil || name != button.Name || button.ID == "" ||
			len(button.ID) > 80 || strings.ContainsAny(button.ID, "\x00\r\n\t/\\") ||
			filepath.Base(button.File) != button.File || button.File == "." || button.File == "" ||
			ids[button.ID] || names[strings.ToLower(button.Name)] || files[strings.ToLower(button.File)] {
			return errors.New("o catálogo salvo da botoneira contém um botão inválido")
		}
		legacy, hasLegacy := legacyEffects[button.ID]
		if button.Builtin && !slices.ContainsFunc(defaults, func(item Button) bool {
			return item.ID == button.ID && (item.File == button.File ||
				b.data.Version == 1 && item.ID == "laughter" && button.File == "laughter.mp3" ||
				b.data.Version <= 2 && hasLegacy && button.File == legacy.file)
		}) || !button.Builtin && (!strings.HasPrefix(button.ID, "custom-") ||
			!strings.HasPrefix(button.File, "custom-")) {
			return errors.New("a botoneira contém um arquivo fora do catálogo seguro")
		}
		extension := strings.ToLower(filepath.Ext(button.File))
		if extension != ".wav" && extension != ".mp3" {
			return errors.New("o catálogo da botoneira aceita somente WAV ou MP3")
		}
		ids[button.ID], names[strings.ToLower(button.Name)], files[strings.ToLower(button.File)] = true, true, true
	}
	return nil
}

func (b *Board) builtinNameLocked(base, id string) string {
	name := base
	for suffix := 1; slices.ContainsFunc(b.data.Buttons, func(button Button) bool {
		return button.ID != id && strings.EqualFold(button.Name, name)
	}); suffix++ {
		name = fmt.Sprintf("%s (%d)", base, suffix)
	}
	return name
}

func (b *Board) ensureBuiltinLocked(name string) error {
	path := filepath.Join(b.directory, name)
	if _, err := os.Lstat(path); err == nil {
		if _, err := b.resolveFileLocked(name); err != nil {
			return err
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("localizar o som incluído: %w", err)
	}
	payload, err := defaultAssets.ReadFile("assets/" + name)
	if err != nil {
		return fmt.Errorf("ler o som incluído %s: %w", name, err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("salvar o som incluído %s: %w", name, err)
	}
	_, writeErr := file.Write(payload)
	return errors.Join(writeErr, file.Close())
}

func (b *Board) resolveLocked(button Button) (string, error) {
	return b.resolveFileLocked(button.File)
}

func (b *Board) resolveFileLocked(name string) (string, error) {
	if filepath.Base(name) != name || name == "." || name == "" {
		return "", errors.New("o caminho do som não é válido")
	}
	path := filepath.Join(b.directory, name)
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("abrir o som salvo: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > maxAudioSize {
		return "", errors.New("o som salvo não é um arquivo regular ou excede 50 MB")
	}
	return path, nil
}

func (b *Board) copyCustomLocked(source, target string) error {
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("abrir o som escolhido: %w", err)
	}
	defer input.Close()
	output, err := os.OpenFile(filepath.Join(b.directory, target), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("copiar o som para a botoneira: %w", err)
	}
	_, copyErr := io.Copy(output, io.LimitReader(input, maxAudioSize+1))
	closeErr := output.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(filepath.Join(b.directory, target))
		if copyErr != nil {
			copyErr = fmt.Errorf("copiar o som para a botoneira: %w", copyErr)
		}
		if closeErr != nil {
			closeErr = fmt.Errorf("fechar o som copiado: %w", closeErr)
		}
		return errors.Join(copyErr, closeErr)
	}
	info, err := os.Stat(filepath.Join(b.directory, target))
	if err != nil || info.Size() < 1 || info.Size() > maxAudioSize {
		_ = os.Remove(filepath.Join(b.directory, target))
		return errors.New("o arquivo escolhido excede 50 MB")
	}
	return nil
}

func (b *Board) saveLocked() error {
	if err := store.WriteJSON(filepath.Join(b.directory, "soundboard.json"), b.data); err != nil {
		return fmt.Errorf("salvar a botoneira: %w", err)
	}
	return nil
}

func validName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if !utf8.ValidString(name) || name == "" || utf8.RuneCountInString(name) > maxNameRunes ||
		name == "." || name == ".." || strings.ContainsAny(name, "\x00\r\n\t<>:\"/\\|?*") ||
		strings.HasSuffix(name, ".") {
		return "", errors.New("use um nome de até 48 caracteres para o botão")
	}
	return name, nil
}

func newID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", errors.New("não foi possível identificar o novo som")
	}
	return hex.EncodeToString(bytes[:]), nil
}
