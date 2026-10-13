package transcript

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"ai-talker/internal/audio"
	"ai-talker/internal/config"
)

func TestIndependentRecognitionCacheForcesSameModelAndResumesOnlyNewAttempt(t *testing.T) {
	var calls atomic.Int32
	var comparison atomic.Bool
	var fail atomic.Bool
	factory := func(context.Context, config.Settings) (Transcriber, error) {
		return &fakeCloud{transcribe: func(_ context.Context, chunk audio.Chunk) (ChunkResult, error) {
			calls.Add(1)
			if comparison.Load() && fail.Load() && chunk.Index == 1 {
				return ChunkResult{}, errors.New("comparison failed")
			}
			version := "original"
			if comparison.Load() {
				version = "nova"
			}
			return ChunkResult{Text: fmt.Sprintf("Versão %s, parte %d.", version, chunk.Index+1)}, nil
		}}, nil
	}
	service, source, directory := prepareService(t, speechParts(2), factory)
	settings := config.Default()
	original, err := service.Transcribe(t.Context(), source, settings, Request{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	comparison.Store(true)
	fail.Store(true)
	attempt := Request{Workers: 1, CacheDir: filepath.Join(directory, "comparison-one")}
	_, err = service.Transcribe(t.Context(), source, settings, attempt)
	var failure *Error
	if !errors.As(err, &failure) || failure.PartialText != "Versão nova, parte 1." || calls.Load() != 4 {
		t.Fatalf("the independent attempt reused old text or did not actually recognize the same model again: %v calls=%d", err, calls.Load())
	}
	fail.Store(false)
	updated, err := service.Transcribe(t.Context(), source, settings, attempt)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ReusedParts != 1 || calls.Load() != 5 || strings.Contains(updated.Text, "original") {
		t.Fatal("retry reused an original part instead of the new attempt's completed part")
	}
	comparison.Store(false)
	cached, err := service.Transcribe(t.Context(), source, settings, Request{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	if cached.Text != original.Text || cached.ReusedParts != 2 || calls.Load() != 5 {
		t.Fatal("the comparison overwrote the original transcription cache")
	}
	markdown, err := os.ReadFile(original.Exports["md"])
	if err != nil || !strings.Contains(string(markdown), original.Text) {
		t.Fatal("the comparison changed the original exported transcription")
	}
	attempt.CacheDir = filepath.Join(directory, "comparison-two")
	fresh, err := service.Transcribe(t.Context(), source, settings, attempt)
	if err != nil || fresh.ReusedParts != 0 || calls.Load() != 7 {
		t.Fatal("a second forced attempt silently reused completed comparison parts")
	}
}
