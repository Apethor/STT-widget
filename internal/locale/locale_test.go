package locale

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

func TestNormalizeLegacyLanguage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, input, want string
	}{
		{name: "legacy missing", input: "", want: Portuguese},
		{name: "Portuguese", input: Portuguese, want: Portuguese},
		{name: "English", input: English, want: English},
		{name: "unknown", input: "fr", want: Portuguese},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Normalize(test.input); got != test.want {
				t.Fatalf("Normalize(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestTranslateApplicationMessagesPreservesDynamicValues(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, source, want string
	}{
		{name: "label", source: "Configurações", want: "Settings"},
		{name: "formatted group", source: "Grupo 19", want: "Group 19"},
		{name: "specific format", source: "Aba 2 · avaliação fechada", want: "Tab 2 · evaluation closed"},
		{name: "raw error", source: "Não foi possível iniciar a conversa: AccessDeniedException: abc/def",
			want: "Could not start the conversation: AccessDeniedException: abc/def"},
		{name: "model and count", source: "Conversa 4 · 3 de 5", want: "Conversation 4 · 3 of 5"},
		{name: "external error unchanged", source: "AWS InvalidRequestException: unsupported voice carolina",
			want: "AWS InvalidRequestException: unsupported voice carolina"},
		{name: "path unchanged", source: `C:\dados\Grupo 19\Salvar.wav`, want: `C:\dados\Grupo 19\Salvar.wav`},
		{name: "ID unchanged", source: "amazon.nova-2-5-sonic", want: "amazon.nova-2-5-sonic"},
		{name: "custom name capture", source: "Opções de Configurações", want: "Options for Configurações"},
		{name: "nested own error", source: "Não foi possível iniciar a conversa: salvar os dados: disk full",
			want: "Could not start the conversation: save data: disk full"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Translate(English, test.source); got != test.want {
				t.Fatalf("Translate(%q) = %q, want %q", test.source, got, test.want)
			}
		})
	}
}

func TestTranslateApprovedPortugueseAndEnglishNames(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, source, portuguese, english string
	}{
		{name: "soundboard", source: "Botoneira de sons", portuguese: "Painel de sons", english: "Soundboard"},
		{name: "recordings", source: "Gravações", portuguese: "Gravações de som", english: "Audio recordings"},
		{name: "screen", source: "Screenshot", portuguese: "Captura de tela", english: "Screen capture"},
		{name: "video", source: "Capturas de vídeo", portuguese: "Captura de vídeo", english: "Video capture"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Translate(Portuguese, test.source); got != test.portuguese {
				t.Fatalf("Portuguese = %q, want %q", got, test.portuguese)
			}
			if got := Translate(English, test.source); got != test.english {
				t.Fatalf("English = %q, want %q", got, test.english)
			}
		})
	}
}

func TestCatalogPreservesFormatContracts(t *testing.T) {
	t.Parallel()
	for source, target := range Catalog() {
		if strings.ContainsRune(source, '\uFFFD') || strings.ContainsRune(target, '\uFFFD') {
			t.Errorf("replacement character in %q", source)
		}
		if !reflect.DeepEqual(formatVerb.FindAllString(source, -1), formatVerb.FindAllString(target, -1)) {
			t.Errorf("format contract differs: %q -> %q", source, target)
		}
	}
}

// This inventory includes every application-owned natural-language literal in
// the desktop, not merely constructors wrapped in uiText. Imports, paths, JSON
// contracts, regular expressions, SVGs and smoke fixtures are excluded.
func TestCatalogCoversDesktopApplicationText(t *testing.T) {
	t.Parallel()
	catalog := Catalog()
	files, err := filepath.Glob("../desktop/*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		name := filepath.Base(path)
		if strings.HasSuffix(name, "_test.go") || strings.HasPrefix(name, "window_smoke") {
			continue
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			text, err := strconv.Unquote(literal.Value)
			if err != nil || !applicationLiteral(text) {
				return true
			}
			if _, translated := catalog[text]; !translated {
				t.Errorf("%s: missing interface translation for %q", fset.Position(literal.Pos()), text)
			}
			return true
		})
	}
}

var alphabeticWord = regexp.MustCompile(`[A-Za-zÀ-ÿ]{2}`)
var svgPath = regexp.MustCompile(`^M[0-9.]`)

func applicationLiteral(text string) bool {
	if len(text) < 4 || !alphabeticWord.MatchString(text) ||
		svgPath.MatchString(text) || strings.HasPrefix(text, "<") ||
		strings.HasPrefix(text, "http://") || strings.HasPrefix(text, "https://") {
		return false
	}
	if text == "WAVEfmt " || strings.Contains(text, `\s`) || strings.Contains(text, `\d`) ||
		strings.Contains(text, `"`) && strings.HasPrefix(text, "(") {
		return false
	}
	return strings.ContainsAny(text, " \n") || strings.ContainsFunc(text, func(r rune) bool {
		return r > unicode.MaxASCII && unicode.IsLetter(r)
	})
}

func ExampleTranslate() {
	fmt.Println(Translate(English, "Configurações"))
	fmt.Println(Translate(English, "Grupo 2"))
	// Output:
	// Settings
	// Group 2
}

func TestEmbeddedCatalogHasExpectedBreadth(t *testing.T) {
	t.Parallel()
	if len(Catalog()) < 1400 {
		t.Fatal("the complete desktop catalog was replaced with a small label list")
	}
	if _, err := os.Stat("en.json"); err != nil {
		t.Fatal(err)
	}
}
