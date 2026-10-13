package desktop

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"ai-talker/internal/config"
	"ai-talker/internal/platform"
	"ai-talker/internal/transcript"
)

func TestNativeImportAppendsAtCompletionAndRetryReplacesOnlyItsUneditedResult(t *testing.T) {
	var calls atomic.Int32
	started, release := make(chan struct{}, 1), make(chan struct{})
	h := newHarness(t, runnerFunc(func(ctx context.Context, source string, _ config.Settings, request transcript.Request) (
		transcript.Result, error,
	) {
		if request.OnPartial != nil || request.NativePCM {
			t.Error("imported audio was sent through Stream")
		}
		attempt := calls.Add(1)
		if attempt == 1 {
			started <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
				return transcript.Result{}, ctx.Err()
			}
		}
		text := "Transcrição importada."
		if attempt == 2 {
			text = "Transcrição importada corrigida."
		}
		return resultFor(source, text), nil
	}), func(deps *dependencies) {
		deps.chooseFile = func(_ context.Context, options platform.FileDialogOptions) (string, error) {
			if options.Save || !options.NewestFirst || len(options.Filters) == 0 {
				t.Error("audio import did not request the native date-sorted picker")
			}
			return "imported.wav", nil
		}
	})
	doc := h.d.document
	doc.editor.SetText("Fala anterior.")
	doc.processEditor.SetText("Resultado anterior.")
	h.d.importAudio()
	h.await(func() bool { return doc.busy })
	waitValue(t, started)
	if doc.editor.Text != "Fala anterior." {
		t.Fatal("opening another audio cleared the editor")
	}
	doc.editor.SetText("Fala anterior editada durante a espera.")
	close(release)
	h.await(func() bool { return !doc.busy })
	if doc.editor.Text != "Fala anterior editada durante a espera.\n\nTranscrição importada." ||
		doc.processEditor.Text != "Resultado anterior." {
		t.Fatal("import replaced previous text, pending edits or processed output")
	}
	h.d.retryDocument(doc)
	h.await(func() bool { return !doc.busy })
	if doc.editor.Text != "Fala anterior editada durante a espera.\n\nTranscrição importada corrigida." {
		t.Fatalf("retry duplicated the imported audio: %q", doc.editor.Text)
	}
}

func TestNativeImportKeepsSelectionOnItsOwningTabAndSilencePreservesText(t *testing.T) {
	opened, release := make(chan struct{}, 1), make(chan struct{})
	h := newHarness(t, runnerFunc(func(_ context.Context, source string, _ config.Settings, _ transcript.Request) (
		transcript.Result, error,
	) {
		return resultFor(source, ""), nil
	}), func(deps *dependencies) {
		deps.chooseFile = func(ctx context.Context, _ platform.FileDialogOptions) (string, error) {
			opened <- struct{}{}
			select {
			case <-release:
				return "silent.wav", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
	})
	first := h.d.document
	first.editor.SetText("Texto da primeira aba.")
	h.d.importAudio()
	waitValue(t, opened)
	h.d.addDocument()
	second := h.d.document
	second.editor.SetText("Texto da segunda aba.")
	close(release)
	h.await(func() bool { return first.lastAudio == "silent.wav" && !first.busy })
	h.settle()
	if first.editor.Text != "Texto da primeira aba." || second.editor.Text != "Texto da segunda aba." ||
		h.d.document != second {
		t.Fatal("silence or switching tabs redirected the selected audio or erased text")
	}
}

func TestNativePickerCancellationLeavesTranscriptionAndAudioUntouched(t *testing.T) {
	for _, failure := range []error{nil, context.Canceled} {
		h := newHarness(t, runnerFunc(func(context.Context, string, config.Settings, transcript.Request) (
			transcript.Result, error,
		) {
			t.Error("a canceled picker started recognition")
			return transcript.Result{}, nil
		}), func(deps *dependencies) {
			deps.chooseFile = func(context.Context, platform.FileDialogOptions) (string, error) {
				return "", failure
			}
		})
		h.d.editor.SetText("Texto preservado.")
		h.d.lastAudio = "previous.wav"
		h.d.importAudio()
		h.settle()
		if h.d.editor.Text != "Texto preservado." || h.d.lastAudio != "previous.wav" || h.d.busy {
			t.Fatal("canceling the native picker changed the current document")
		}
	}
}

func TestNativeSaveSnapshotsTheOwningEditorAtConfirmationAndReportsWriteErrors(t *testing.T) {
	for _, processed := range []bool{false, true} {
		for _, invalid := range []bool{false, true} {
			opened, release := make(chan struct{}, 1), make(chan struct{})
			var destination string
			h := newHarness(t, nil, func(deps *dependencies) {
				deps.chooseFile = func(ctx context.Context, options platform.FileDialogOptions) (string, error) {
					if !options.Save || options.DefaultExtension != "txt" || !strings.HasSuffix(options.FileName, ".txt") {
						t.Error("save did not request a native text destination")
					}
					opened <- struct{}{}
					select {
					case <-release:
						return destination, nil
					case <-ctx.Done():
						return "", ctx.Err()
					}
				}
			})
			destination = filepath.Join(h.d.directory, "saved.txt")
			if invalid {
				destination = filepath.Join(h.d.directory, "missing", "saved.txt")
			}
			doc := h.d.document
			doc.editor.SetText("Transcrição inicial.")
			doc.processEditor.SetText("Processado inicial.")
			h.d.saveDocumentText(doc, processed)
			waitValue(t, opened)
			editor := doc.editor
			if processed {
				editor = doc.processEditor
			}
			editor.SetText("Texto confirmado.")
			close(release)
			h.await(func() bool {
				return doc.view.status == "Texto salvo." || doc.errorLabel.Visible() || doc.processError.Visible()
			})
			h.settle()
			if invalid {
				if !doc.errorLabel.Visible() && !doc.processError.Visible() {
					t.Fatal("a failed native export did not expose a recoverable error")
				}
			} else {
				requireFile(t, destination, "Texto confirmado.\n")
			}
		}
	}
}

func TestNativeImportFailureDoesNotOverwriteEarlierText(t *testing.T) {
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.chooseFile = func(context.Context, platform.FileDialogOptions) (string, error) {
			return "", errors.New("seletor indisponível")
		}
	})
	h.d.editor.SetText("Texto preservado.")
	h.d.importAudio()
	h.settle()
	if h.d.editor.Text != "Texto preservado." || !h.d.errorLabel.Visible() {
		t.Fatal("a file-dialog failure erased text or hid the error")
	}
}
