package store

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestConcurrentWritesLeaveOneCompleteDocument(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "result.json")
	var writers sync.WaitGroup
	for index := range 12 {
		writers.Go(func() {
			if err := WriteJSON(path, map[string]any{"index": index, "text": "Texto em português."}); err != nil {
				t.Error(err)
			}
		})
	}
	writers.Wait()
	var saved struct {
		Index int    `json:"index"`
		Text  string `json:"text"`
	}
	if err := ReadJSON(path, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Text != "Texto em português." || saved.Index < 0 || saved.Index > 11 {
		t.Fatalf("incomplete saved document: %+v", saved)
	}
	temporary, err := filepath.Glob(filepath.Join(filepath.Dir(path), "*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(temporary) != 0 {
		t.Fatalf("temporary writes were not cleaned: %v", temporary)
	}
}

func TestFailedSerializationPreservesExistingDocument(t *testing.T) {
	path := filepath.Join(t.TempDir(), "result.json")
	original := []byte(`{"text":"preserved"}`)
	if err := Write(path, original); err != nil {
		t.Fatal(err)
	}
	if err := WriteJSON(path, make(chan int)); err == nil {
		t.Fatal("unsupported JSON value was accepted")
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(saved) != string(original) {
		t.Fatal("failed write changed the previous document")
	}
}
