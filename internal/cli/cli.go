// Package cli exposes the shared transcription core without opening a window.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"ai-talker/internal/config"
	"ai-talker/internal/transcript"
)

type Runner interface {
	Transcribe(context.Context, string, config.Settings, transcript.Request) (transcript.Result, error)
}

type App struct {
	DataDir string
	Runner  Runner
	Verify  func(context.Context, config.Settings) error
	Copy    func(string) error
	Launch  func(context.Context, config.Settings) error
	Stdout  io.Writer
	Stderr  io.Writer
	Version string
}

type commandOptions struct {
	profile     string
	provider    string
	region      string
	quality     string
	model       string
	streamModel string
	language    string
	bucket      string
	chunks      int
	overlap     float64
	glossary    string
	output      string
	markdown    string
	workers     int
	stdout      bool
	copy        bool
	json        bool
	explicit    map[string]bool
}

func (a App) Run(ctx context.Context, arguments []string) int {
	if a.Stdout == nil {
		a.Stdout = os.Stdout
	}
	if a.Stderr == nil {
		a.Stderr = os.Stderr
	}
	command := "gui"
	if len(arguments) > 0 {
		command = arguments[0]
		arguments = arguments[1:]
	}
	switch command {
	case "help", "-h", "--help":
		fmt.Fprintln(a.Stdout, "STT-widget — gravação e transcrição por AWS Cloud ou Whisper local.")
		fmt.Fprintln(a.Stdout, "Uso: stt-widget-cli gui | doctor | models | transcribe <áudio> [opções] | version")
		fmt.Fprintln(a.Stdout, "Transcrição: --output-dir <pasta> --markdown <arquivo.md> --stdout | --json")
		fmt.Fprintln(a.Stdout, "AWS: --profile <perfil> --region <região> --quality quality|fast")
		fmt.Fprintln(a.Stdout, "Cloud: --model <ID> --stream-model <ID> --transcribe-language pt-BR|auto --transcribe-bucket <nome>")
		return 0
	case "models":
		for _, model := range config.CloudModels() {
			modes := "gravação completa"
			if model.Stream {
				modes += ", Stream"
			}
			fmt.Fprintf(a.Stdout, "%s\n  %s · %s\n", model.ID, model.Name, modes)
		}
		return 0
	case "version", "--version":
		fmt.Fprintln(a.Stdout, a.Version)
		return 0
	case "gui", "doctor", "transcribe":
	default:
		fmt.Fprintf(a.Stderr, "Comando desconhecido: %s. Use stt-widget-cli help.\n", command)
		return 2
	}
	options, positional, err := parse(command, arguments, a.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		fmt.Fprintf(a.Stderr, "Erro de uso: %v\n", err)
		return 2
	}
	if command == "transcribe" && len(positional) != 1 {
		fmt.Fprintln(a.Stderr, "Informe exatamente um arquivo de áudio.")
		return 2
	}
	if command != "transcribe" && len(positional) != 0 {
		fmt.Fprintln(a.Stderr, "Este comando não recebe um arquivo de áudio.")
		return 2
	}
	if options.stdout && options.json {
		fmt.Fprintln(a.Stderr, "Escolha somente --stdout ou --json.")
		return 2
	}
	settings, err := config.Load(a.DataDir)
	if err != nil {
		fmt.Fprintf(a.Stderr, "Erro: %v\n", err)
		return 1
	}
	applyOptions(&settings, options)
	if err := settings.Validate(); err != nil {
		fmt.Fprintf(a.Stderr, "Erro de configuração: %v\n", err)
		return 2
	}
	switch command {
	case "gui":
		if a.Launch == nil {
			fmt.Fprintln(a.Stderr, "O executável da interface não foi encontrado. Compile o pacote Windows.")
			return 1
		}
		if err := a.Launch(ctx, settings); err != nil {
			fmt.Fprintf(a.Stderr, "Erro ao abrir a interface: %v\n", err)
			return 1
		}
		return 0
	case "doctor":
		if a.Verify == nil {
			fmt.Fprintln(a.Stderr, "A verificação AWS não está disponível.")
			return 1
		}
		if err := a.Verify(ctx, settings); err != nil {
			fmt.Fprintf(a.Stderr, "Erro: %v\n", err)
			return 1
		}
		fmt.Fprintln(a.Stdout, "Login AWS válido.")
		fmt.Fprintf(a.Stdout, "Perfil: %s\nRegião: %s\nModelo: %s\nAPI: %s\n",
			settings.Profile, settings.Region, settings.ModelID(), cloudAPI(settings))
		return 0
	default:
		return a.transcribe(ctx, positional[0], settings, options)
	}
}

func parse(command string, arguments []string, output io.Writer) (commandOptions, []string, error) {
	var options commandOptions
	flags := flag.NewFlagSet("ai-talker "+command, flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&options.profile, "profile", "", "Perfil AWS já configurado.")
	flags.StringVar(&options.provider, "provider", "", "bedrock (AWS Cloud), whisper ou api (endpoint/modelo/chave das configurações).")
	flags.StringVar(&options.region, "region", "", "Região AWS.")
	flags.StringVar(&options.quality, "quality", "", "quality ou fast.")
	flags.StringVar(&options.model, "model", "", "ID do modelo Cloud para gravação completa; consulte ai-talker models.")
	flags.StringVar(&options.streamModel, "stream-model", "", "ID do modelo Cloud para Stream.")
	flags.StringVar(&options.language, "transcribe-language", "", "Idioma do Amazon Transcribe; pt-BR por padrão ou auto.")
	flags.StringVar(&options.bucket, "transcribe-bucket", "", "Nome do bucket S3 para Transcribe · arquivo, na mesma região.")
	if command == "transcribe" {
		flags.StringVar(&options.output, "output-dir", "transcricoes", "Pasta para TXT, Markdown e JSON.")
		flags.StringVar(&options.markdown, "markdown", "", "Destino exato do Markdown; não sobrescreve arquivos.")
		flags.IntVar(&options.chunks, "chunk-seconds", 0, "Duração das partes: 15 a 300 segundos.")
		flags.Float64Var(&options.overlap, "overlap-seconds", -1, "Sobreposição: zero a cinco segundos.")
		flags.StringVar(&options.glossary, "glossary", "", "Termos adicionais para a referência de grafia.")
		flags.IntVar(&options.workers, "workers", 2, "Chamadas simultâneas: 1 a 4.")
		flags.BoolVar(&options.stdout, "stdout", false, "Imprimir somente o texto em stdout.")
		flags.BoolVar(&options.copy, "copy", false, "Copiar o conteúdo para o clipboard do Windows.")
		flags.BoolVar(&options.json, "json", false, "Imprimir resultado e partes em JSON em stdout.")
	}
	// The Python CLI accepts flags after the audio path. Keep that contract
	// while using the standard flag parser instead of another CLI dependency.
	ordered, err := flagsFirst(flags, arguments)
	if err != nil {
		return options, nil, err
	}
	if err := flags.Parse(ordered); err != nil {
		return options, nil, err
	}
	options.explicit = make(map[string]bool)
	flags.Visit(func(option *flag.Flag) { options.explicit[option.Name] = true })
	return options, flags.Args(), nil
}

func flagsFirst(flags *flag.FlagSet, arguments []string) ([]string, error) {
	ordered := make([]string, 0, len(arguments)+1)
	positional := make([]string, 0, 1)
	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		if argument == "--" {
			positional = append(positional, arguments[index+1:]...)
			break
		}
		if !strings.HasPrefix(argument, "-") || argument == "-" {
			positional = append(positional, argument)
			continue
		}
		ordered = append(ordered, argument)
		name := strings.TrimLeft(argument, "-")
		if strings.Contains(name, "=") || name == "help" || name == "h" {
			continue
		}
		option := flags.Lookup(name)
		if option == nil {
			return nil, fmt.Errorf("opção desconhecida: %s", argument)
		}
		isBoolean, ok := option.Value.(interface{ IsBoolFlag() bool })
		if ok && isBoolean.IsBoolFlag() {
			continue
		}
		index++
		if index >= len(arguments) {
			return nil, fmt.Errorf("informe um valor para %s", argument)
		}
		ordered = append(ordered, arguments[index])
	}
	ordered = append(ordered, "--")
	return append(ordered, positional...), nil
}

func applyOptions(settings *config.Settings, options commandOptions) {
	if options.explicit["profile"] {
		settings.Profile = options.profile
	}
	if options.explicit["region"] {
		settings.Region = options.region
	}
	if options.explicit["quality"] {
		settings.Quality = options.quality
		settings.Cloud.ModelID = ""
	}
	if options.explicit["model"] {
		settings.Cloud.ModelID = options.model
		settings.Provider = config.ProviderBedrock
	}
	if options.explicit["provider"] {
		settings.Provider = options.provider
	}
	if options.explicit["stream-model"] {
		settings.Cloud.StreamModelID = options.streamModel
	}
	if options.explicit["transcribe-language"] {
		settings.Cloud.Language = options.language
	}
	if options.explicit["transcribe-bucket"] {
		settings.Cloud.S3Bucket = options.bucket
		enabled := true
		settings.Cloud.UseS3 = &enabled
	}
	if options.explicit["chunk-seconds"] {
		settings.ChunkSeconds = options.chunks
	}
	if options.explicit["overlap-seconds"] {
		settings.OverlapSeconds = options.overlap
	}
	if options.glossary != "" {
		settings.Glossary += ", " + options.glossary
	}
}

func cloudAPI(settings config.Settings) string {
	if settings.TranscriptionProvider() == config.ProviderWhisper {
		return "Whisper local"
	}
	model, _ := config.CloudModelByID(settings.CloudModelID())
	switch model.Backend {
	case config.CloudTranscribe:
		return "Amazon Transcribe / StartTranscriptionJob + S3"
	case config.CloudStreaming:
		return "Amazon Transcribe / StartStreamTranscription"
	case config.CloudSonic:
		return "Bedrock Runtime / InvokeModelWithBidirectionalStream"
	default:
		return "Bedrock Runtime / Converse"
	}
}

func (a App) transcribe(ctx context.Context, source string, settings config.Settings, options commandOptions) int {
	if a.Runner == nil {
		fmt.Fprintln(a.Stderr, "O núcleo de transcrição não está disponível.")
		return 1
	}
	if options.workers < 1 || options.workers > 4 {
		fmt.Fprintln(a.Stderr, "Use entre uma e quatro chamadas simultâneas.")
		return 2
	}
	if options.markdown != "" {
		if _, err := os.Stat(options.markdown); !errors.Is(err, os.ErrNotExist) {
			if err == nil {
				fmt.Fprintf(a.Stderr, "O destino já existe: %s\n", options.markdown)
			} else {
				fmt.Fprintf(a.Stderr, "Não foi possível conferir o destino: %v\n", err)
			}
			return 1
		}
	}
	var previous string
	result, err := a.Runner.Transcribe(ctx, source, settings, transcript.Request{
		OutputDir: options.output, Workers: options.workers, PersistLatest: true,
		OnProgress: func(event transcript.Progress) {
			if event.Message != previous {
				fmt.Fprintln(a.Stderr, event.Message)
				previous = event.Message
			}
		},
	})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(a.Stderr, "Transcrição cancelada. O áudio e as partes concluídas foram preservados.")
			return 130
		}
		var failure *transcript.Error
		if errors.As(err, &failure) && failure.PartialText != "" {
			if options.stdout {
				fmt.Fprintln(a.Stdout, failure.PartialText)
			}
			if options.json {
				encodeErr := json.NewEncoder(a.Stdout).Encode(struct {
					Partial     bool   `json:"partial"`
					Error       string `json:"error"`
					PartialText string `json:"partial_text"`
					JobID       string `json:"job_id"`
				}{Partial: true, Error: err.Error(), PartialText: failure.PartialText, JobID: failure.JobID})
				if encodeErr != nil {
					fmt.Fprintf(a.Stderr, "Erro ao imprimir o resultado parcial: %v\n", encodeErr)
				}
			}
			fmt.Fprintln(a.Stderr, "Resultado parcial: uma parte do áudio não foi concluída. O cache permite retomar.")
		}
		fmt.Fprintf(a.Stderr, "Erro: %v\n", err)
		return 1
	}
	var markdown string
	if strings.TrimSpace(result.Text) != "" {
		if options.copy {
			if a.Copy == nil {
				fmt.Fprintln(a.Stderr, "A cópia para o clipboard não está disponível.")
				return 1
			}
			if err := a.Copy(result.Text); err != nil {
				fmt.Fprintf(a.Stderr, "Texto salvo, mas não foi possível copiar: %v\n", err)
				return 1
			}
		}
		if options.markdown != "" {
			markdown, err = copyMarkdown(result.Exports["md"], options.markdown)
			if err != nil {
				fmt.Fprintf(a.Stderr, "Texto salvo, mas não foi possível entregar o Markdown: %v\n", err)
				return 1
			}
		}
	}
	if options.json {
		err := json.NewEncoder(a.Stdout).Encode(struct {
			transcript.Result
			Markdown string `json:"markdown,omitempty"`
		}{Result: result, Markdown: markdown})
		if err != nil {
			fmt.Fprintf(a.Stderr, "Erro ao imprimir o JSON: %v\n", err)
			return 1
		}
	} else if options.stdout {
		fmt.Fprintln(a.Stdout, result.Text)
	} else if result.Exports != nil {
		fmt.Fprintf(a.Stdout, "Texto salvo: %s\n", result.Exports["txt"])
		if markdown != "" {
			fmt.Fprintf(a.Stdout, "Markdown entregue: %s\n", markdown)
		}
	} else {
		fmt.Fprintln(a.Stdout, "Nenhuma fala detectada. Nenhum texto foi gerado.")
	}
	fmt.Fprintf(a.Stderr, "%.2f min de áudio · %.1fs de processamento · %d/%d partes reaproveitadas.\n",
		result.DurationSeconds/60, result.ElapsedSeconds, result.ReusedParts, len(result.Parts))
	if result.NeedsReview {
		fmt.Fprintln(a.Stderr, "Há partes que precisam de conferência. Verifique needs_review e review_reason das partes e compare com o áudio.")
		return 3
	}
	return 0
}

func copyMarkdown(source, destination string) (string, error) {
	if source == "" {
		return "", errors.New("o resultado não contém uma exportação Markdown")
	}
	path, err := filepath.Abs(destination)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	input, err := os.Open(source)
	if err != nil {
		return "", err
	}
	defer input.Close()
	output, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	_, copyErr := io.Copy(output, input)
	syncErr := output.Sync()
	closeErr := output.Close()
	if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
		return "", fmt.Errorf("salvar o Markdown em %s: %w", path, err)
	}
	return path, nil
}
