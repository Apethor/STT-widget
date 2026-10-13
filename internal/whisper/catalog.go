// Package whisper provides local speech recognition using whisper.cpp.
package whisper

import (
	"errors"
	"path/filepath"
)

const (
	SourceGitHub      = "github"
	SourceHuggingFace = "huggingface"
)

const (
	EngineVersion = "1.9.5"
	engineURL     = "https://github.com/ggml-org/whisper.cpp/releases/download/b5454/whisper-bin-x64.zip"
	engineSHA256  = "6ba69e3482d7826214f90a6a9c84ca07782aec1e1d0c6a7c30c994fd5d816ccb"
	engineBytes   = 8928640
)

type Model struct {
	ID       string
	Name     string
	Disk     string
	MaxBytes int64
	SHA1     string
	SHA256   string
	GitHub   string
}

// Standard weights retain the upstream SHA1; GitHub assets have a pinned SHA256.
// Smaller multilingual assets were also checked with the native engine.
// Checksums identify the expected bytes, not the publisher's identity.
func Models() []Model {
	return []Model{
		{ID: "tiny-q5_1", Name: "Tiny Q5", Disk: "31 MiB", MaxBytes: 40 << 20,
			SHA1: "2827a03e495b1ed3048ef28a6a4620537db4ee51", SHA256: "818710568da3ca15689e31a743197b520007872ff9576237bda97bd1b469c3d7",
			GitHub: "https://github.com/ddddwq2q/whisper-models/releases/download/q5-models/tiny-q5_1.bin"},
		{ID: "tiny", Name: "Tiny", Disk: "75 MiB", MaxBytes: 90 << 20, SHA1: "bd577a113a864445d4c299885e0cb97d4ba92b5f",
			SHA256: "be07e048e1e599ad46341c8d2a135645097a538221678b7acdd1b1919c6e1b21",
			GitHub: "https://github.com/ddddwq2q/whisper-models/releases/download/Models/ggml-tiny.bin"},
		{ID: "base-q5_1", Name: "Base Q5", Disk: "57 MiB", MaxBytes: 65 << 20,
			SHA1: "a3733eda680ef76256db5fc5dd9de8629e62c5e7", SHA256: "422f1ae452ade6f30a004d7e5c6a43195e4433bc370bf23fac9cc591f01a8898",
			GitHub: "https://github.com/ddddwq2q/whisper-models/releases/download/q5-models/base-q5_1.bin"},
		{ID: "base", Name: "Base", Disk: "142 MiB", MaxBytes: 160 << 20, SHA1: "465707469ff3a37a2b9b8d8f89f2f99de7299dac",
			SHA256: "60ed5bc3dd14eea856493d334349b405782ddcaf0028d4b5df4088345fba2efe",
			GitHub: "https://github.com/ddddwq2q/whisper-models/releases/download/Models/ggml-base.bin"},
		{ID: "small-q5_1", Name: "Small Q5", Disk: "182 MiB", MaxBytes: 200 << 20,
			SHA1: "6fe57ddcfdd1c6b07cdcc73aaf620810ce5fc771", SHA256: "ae85e4a935d7a567bd102fe55afc16bb595bdb618e11b2fc7591bc08120411bb",
			GitHub: "https://github.com/riddl0r/voice-models/releases/download/models-v1/ggml-small-q5_1.bin"},
		{ID: "small", Name: "Small", Disk: "466 MiB", MaxBytes: 500 << 20, SHA1: "55356645c2b361a969dfd0ef2c5a50d530afd8d5",
			SHA256: "1be3a9b2063867b937e64e2ec7483364a79917e157fa98c5d94b5c1fffea987b",
			GitHub: "https://github.com/riddl0r/voice-models/releases/download/models-v1/ggml-small.bin"},
		{ID: "medium", Name: "Medium", Disk: "1,5 GiB", MaxBytes: 1600 << 20, SHA1: "fd9727b6e1217c2f614f9b698455c4ffd82463b4"},
		{ID: "large-v3", Name: "Large v3", Disk: "2,9 GiB", MaxBytes: 3100 << 20, SHA1: "ad82bf6a9043ceed055076d0fd39f5f186ff8062"},
		{ID: "large-v3-q8_0", Name: "Large v3 Q8", Disk: "1,6 GiB", MaxBytes: 1700 << 20,
			SHA256: "24bc434f372355688ab9a623077a63e5361a1c41f4d8d648977e39f9b060f09e",
			GitHub: "https://github.com/sergheinenov/whisper-large-v3-ggml/releases/download/v1.0.0/ggml-large-v3-q8_0.bin"},
		{ID: "large-v3-turbo", Name: "Turbo", Disk: "1,5 GiB", MaxBytes: 1700 << 20, SHA1: "4af2b29d7ec73d781377bfd1758ca957a807e941",
			SHA256: "1fc70f774d38eb169993ac391eea357ef47c88757ef72ee5943879b7e8e2bc69",
			GitHub: "https://github.com/blind0wl/nirivoice-mac-assets/releases/download/whisper-cpp-v1.9.1/ggml-large-v3-turbo.bin"},
		{ID: "large-v3-turbo-q5_0", Name: "Turbo Q5", Disk: "547 MiB", MaxBytes: 600 << 20, SHA1: "e050f7970618a659205450ad97eb95a18d69c9ee",
			SHA256: "394221709cd5ad1f40c46e6031ca61bce88931e6e088c188294c6d5a55ffa7e2",
			GitHub: "https://github.com/riddl0r/voice-models/releases/download/models-v1/ggml-large-v3-turbo-q5_0.bin"},
	}
}

func (model Model) DownloadDescription() string {
	return model.DownloadDescriptionFrom(SourceGitHub)
}

// DownloadDescriptionFrom explains the selected source or its unavailable
// combination without suggesting that a failed download changes hosts.
func (model Model) DownloadDescriptionFrom(source string) string {
	if _, _, _, err := model.DownloadFrom(source); err != nil {
		return err.Error() + " Escolha a outra fonte ou use um arquivo local."
	}
	name := "GitHub"
	if source == SourceHuggingFace {
		name = "Hugging Face"
	}
	return "Download pelo " + name + ", com checksum verificado. Não troca de site automaticamente."
}

// DownloadFrom returns only the selected verified source. legacySHA1 identifies
// upstream Hugging Face pins; GitHub assets use the catalog's SHA256.
func (model Model) DownloadFrom(source string) (address, digest string, legacySHA1 bool, err error) {
	switch source {
	case "", SourceGitHub:
		if model.GitHub == "" || model.SHA256 == "" {
			return "", "", false, errors.New("este modelo não tem download GitHub verificado")
		}
		return model.GitHub, model.SHA256, false, nil
	case SourceHuggingFace:
		if model.SHA1 == "" {
			return "", "", false, errors.New("este modelo não tem download Hugging Face verificado")
		}
		return "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-" + model.ID + ".bin", model.SHA1, true, nil
	default:
		return "", "", false, errors.New("selecione GitHub ou Hugging Face")
	}
}

func ModelByID(id string) (Model, bool) {
	for _, model := range Models() {
		if model.ID == id {
			return model, true
		}
	}
	return Model{}, false
}

func BinaryPath(directory string) string {
	return filepath.Join(directory, "whisper", "engine-"+EngineVersion, "whisper-cli.exe")
}

func ModelPath(directory, model string) string {
	if _, ok := ModelByID(model); !ok {
		return ""
	}
	return filepath.Join(directory, "whisper", "models", "ggml-"+model+".bin")
}
