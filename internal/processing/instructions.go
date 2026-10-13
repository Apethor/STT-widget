package processing

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// MaxCustomInstructions is the number of named modes available beside built-ins.
const MaxCustomInstructions = 10

// Instruction is a saved definition whose stable ID survives a rename.
// Requests preserve their own copy of its name and prompt.
type Instruction struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Prompt string `json:"prompt"`
}

// CustomMode recognizes stable IDs which survive instruction renaming.
func CustomMode(mode string) bool { return strings.HasPrefix(mode, "custom-") }

func validPrompt(prompt string) bool {
	return utf8.ValidString(prompt) && strings.TrimSpace(prompt) != "" &&
		utf8.RuneCountInString(prompt) <= MaxPromptRunes
}

// ValidateInstructions enforces the ten custom slots and unique nonreserved
// names before publishing a configuration shared by the mode selectors.
func ValidateInstructions(instructions []Instruction) error {
	if len(instructions) > MaxCustomInstructions {
		return errors.New("use no máximo dez instruções personalizadas")
	}
	ids, names := make(map[string]bool), make(map[string]bool)
	for _, instruction := range instructions {
		name := strings.TrimSpace(instruction.Name)
		key := strings.ToLower(name)
		if !CustomMode(instruction.ID) || len(instruction.ID) > 128 || ids[instruction.ID] ||
			name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 80 ||
			strings.ContainsAny(instruction.ID+name, "\x00\r\n\t") || names[key] ||
			key == "livre" || key == "teams" || key == "mesclar" || !validPrompt(instruction.Prompt) {
			return errors.New("informe nomes únicos, até 80 caracteres, e instruções com até 12.000 caracteres")
		}
		ids[instruction.ID], names[key] = true, true
	}
	return nil
}

// ResolveInstruction freezes the current definition for a new request.
// Historical requests retain their snapshot when a definition is edited.
func (o Options) ResolveInstruction(instructions []Instruction) (Options, error) {
	if !CustomMode(o.Mode) {
		return o, nil
	}
	for _, instruction := range instructions {
		if instruction.ID == o.Mode {
			o.InstructionName, o.InstructionPrompt = instruction.Name, instruction.Prompt
			return o, nil
		}
	}
	return o, fmt.Errorf("a instrução %q foi excluída; escolha outro modo para o próximo pedido", o.InstructionName)
}
