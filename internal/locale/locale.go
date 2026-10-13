// Package locale translates application-owned interface text. Recognition
// languages, user content, file paths and provider responses are not inputs to
// this package.
package locale

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
)

const (
	Portuguese = "pt-BR"
	English    = "en"
)

//go:embed en.json
var englishJSON []byte

var current atomic.Value
var english map[string]string
var original map[string]string
var formats []messageFormat
var reverseFormats []messageFormat
var fragments []string

type messageFormat struct {
	pattern *regexp.Regexp
	target  []string
	errors  []bool
}

// aliases preserve old source labels while presenting the approved names in
// both languages. They never rename a directory or change serialized data.
var aliases = map[string]string{
	"Botoneira de sons":       "Painel de sons",
	"Abrir botoneira de sons": "Abrir painel de sons",
	"Gravações":               "Gravações de som",
	"Gravações de áudio":      "Gravações de som",
	"Screenshot":              "Captura de tela",
	"Screenshots":             "Captura de tela",
	"Capturas de vídeo":       "Captura de vídeo",
}

func init() {
	if err := json.Unmarshal(englishJSON, &english); err != nil {
		panic("invalid embedded interface translations: " + err.Error())
	}
	original = make(map[string]string, len(english))
	keys := make([]string, 0, len(english))
	for source := range english {
		keys = append(keys, source)
	}
	sort.Strings(keys)
	for _, source := range keys {
		target := english[source]
		if _, exists := original[target]; !exists {
			original[target] = source
		}
		if source != target {
			fragments = append(fragments, source)
		}
		if format, ok := compileFormat(source, target); ok {
			formats = append(formats, format)
		}
		if format, ok := compileFormat(target, source); ok {
			reverseFormats = append(reverseFormats, format)
		}
	}
	sort.SliceStable(formats, func(i, j int) bool {
		return len(formats[i].pattern.String()) > len(formats[j].pattern.String())
	})
	sort.SliceStable(reverseFormats, func(i, j int) bool {
		return len(reverseFormats[i].pattern.String()) > len(reverseFormats[j].pattern.String())
	})
	sort.SliceStable(fragments, func(i, j int) bool {
		return len(fragments[i]) > len(fragments[j])
	})
	current.Store(Portuguese)
}

// Normalize returns the durable language code; legacy preferences default to
// Portuguese independently of the operating system and recognition language.
func Normalize(language string) string {
	if language == English {
		return English
	}
	return Portuguese
}

func Set(language string) { current.Store(Normalize(language)) }
func Current() string     { return current.Load().(string) }

// Source returns the untranslated value for a localized selector. Callers
// retain their existing stable source values when reading or comparing options.
func Source(text string) string {
	if source, exists := original[text]; exists {
		return source
	}
	if source, ok := applyFormats(reverseFormats, text, sourceError); ok {
		return source
	}
	for _, fragment := range fragments {
		if !errorPrefix(fragment) {
			continue
		}
		if translated := english[fragment]; strings.HasPrefix(text, translated) {
			return fragment + sourceError(text[len(translated):])
		}
	}
	return text
}

func Text(text string) string { return Translate(Current(), text) }

func Format(format string, values ...any) string {
	return fmt.Sprintf(Text(format), values...)
}

// Translate supports exact messages and their formatted variants. Dynamic
// captures are copied byte-for-byte, including paths, IDs and provider errors.
func Translate(language, text string) string {
	source := Source(text)
	if Normalize(language) == Portuguese {
		if name, exists := aliases[source]; exists {
			return name
		}
		if _, own := english[source]; own {
			return portugueseText(source)
		}
		return source
	}
	if name, exists := aliases[source]; exists {
		source = name
	}
	if translated, exists := english[source]; exists {
		return translated
	}
	if translated, ok := applyFormats(formats, source, translateError); ok {
		return translated
	}
	// Concatenated UI messages commonly put an application sentence before a
	// raw service error, path or user name. Translate only the fixed boundary.
	for _, fragment := range fragments {
		if len(fragment) < 4 {
			continue
		}
		if strings.HasPrefix(source, fragment) && boundaryFragment(fragment) {
			if errorPrefix(fragment) {
				return english[fragment] + translateError(source[len(fragment):])
			}
			return english[fragment] + source[len(fragment):]
		}
		if strings.HasSuffix(source, fragment) && strings.HasPrefix(fragment, " · ") {
			return source[:len(source)-len(fragment)] + english[fragment]
		}
	}
	return text
}

func applyFormats(formats []messageFormat, text string, errorText func(string) string) (string, bool) {
	for _, format := range formats {
		match := format.pattern.FindStringSubmatch(text)
		if match == nil {
			continue
		}
		var result strings.Builder
		for index, fragment := range format.target {
			result.WriteString(fragment)
			if index+1 < len(match) {
				value := match[index+1]
				if index < len(format.errors) && format.errors[index] {
					value = errorText(value)
				}
				result.WriteString(value)
			}
		}
		return result.String(), true
	}
	return "", false
}

var ownErrorStart = regexp.MustCompile(`(?i)^(?:não |falha |selecione |informe |configure |use |ler |salvar |remover |preparar |obter |confirmar |abrir |fechar |localizar |criar |exportar |identificar |validar |verificar |o |a |os |as |este |esta |parte |limite |dispositivo )`)

func errorPrefix(text string) bool {
	return strings.HasSuffix(text, ": ") && ownErrorStart.MatchString(text)
}

func sourceError(text string) string {
	source := Source(text)
	if ownErrorStart.MatchString(source) {
		return source
	}
	return text
}

func translateError(text string) string {
	if ownErrorStart.MatchString(Source(text)) {
		return Translate(English, text)
	}
	return text
}

func boundaryFragment(text string) bool {
	return strings.HasSuffix(text, ": ") || strings.HasSuffix(text, " · ") ||
		strings.HasSuffix(text, "\n") || strings.HasSuffix(text, "…") ||
		strings.HasSuffix(text, "grupo ") || strings.HasSuffix(text, "aba ")
}

var formatVerb = regexp.MustCompile(`%(\[[0-9]+\])?[-+# 0]*[0-9]*(\.[0-9]+)?[a-zA-Z%]`)

func compileFormat(source, target string) (messageFormat, bool) {
	matches := formatVerb.FindAllStringIndex(source, -1)
	targetMatches := formatVerb.FindAllStringIndex(target, -1)
	if len(matches) == 0 || len(matches) != len(targetMatches) {
		return messageFormat{}, false
	}
	var expression strings.Builder
	expression.WriteString("(?s)^")
	last := 0
	for _, match := range matches {
		expression.WriteString(regexp.QuoteMeta(source[last:match[0]]))
		if source[match[0]:match[1]] == "%%" {
			expression.WriteString("%")
		} else {
			verb := source[match[1]-1]
			switch verb {
			case 'd', 'b', 'o', 'O', 'U':
				expression.WriteString("([-+]?[0-9]+)")
			case 'x', 'X':
				expression.WriteString("([0-9a-fA-F]+)")
			case 'f', 'F', 'e', 'E', 'g', 'G':
				expression.WriteString("([-+]?[0-9]+(?:\\.[0-9]+)?(?:[eE][-+]?[0-9]+)?)")
			default:
				expression.WriteString("(.*?)")
			}
		}
		last = match[1]
	}
	expression.WriteString(regexp.QuoteMeta(source[last:]))
	expression.WriteString("$")
	// Completely numeric formats need no localization and would match arbitrary
	// user values before a real message. At least one fixed word is required.
	if !regexp.MustCompile(`[A-Za-zÀ-ÿ]{2}`).MatchString(formatVerb.ReplaceAllString(source, "")) {
		return messageFormat{}, false
	}
	fragments := make([]string, 0, len(targetMatches)+1)
	errorCaptures := make([]bool, 0, len(targetMatches))
	last = 0
	for _, match := range targetMatches {
		if target[match[0]:match[1]] == "%%" {
			// The UI only uses escaped percentages after a captured numeric value.
			continue
		}
		fragments = append(fragments, strings.ReplaceAll(target[last:match[0]], "%%", "%"))
		errorCaptures = append(errorCaptures, target[match[1]-1] == 'w')
		last = match[1]
	}
	fragments = append(fragments, strings.ReplaceAll(target[last:], "%%", "%"))
	return messageFormat{pattern: regexp.MustCompile(expression.String()), target: fragments, errors: errorCaptures}, true
}

func portugueseText(source string) string {
	replacements := []string{
		"dos screenshots", "das capturas de tela",
		"os screenshots", "as capturas de tela",
		"Screenshot", "Captura de tela",
		"screenshots", "capturas de tela",
		"screenshot", "captura de tela",
		"Botoneira de sons", "Painel de sons",
		"a botoneira de sons", "o painel de sons",
		"à botoneira", "ao painel de sons",
		"da botoneira", "do painel de sons",
		"na botoneira", "no painel de sons",
	}
	return strings.NewReplacer(replacements...).Replace(source)
}

// Catalog returns a detached copy for coverage and translator tooling.
func Catalog() map[string]string {
	result := make(map[string]string, len(english))
	for source, translation := range english {
		result[source] = translation
	}
	return result
}
