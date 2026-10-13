package desktop

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"ai-talker/internal/config"
	"ai-talker/internal/processing"
	"ai-talker/internal/realtime"

	"fyne.io/fyne/v2/test"
)

func TestVoiceCleanupPreviewAndRecyclePreserveTextsHistoryAndExportsAfterReopen(t *testing.T) {
	var sends atomic.Int32
	configure := func(deps *dependencies) {
		deps.newConversation = func(context.Context, config.Settings, realtime.Options, func(realtime.Event)) (voiceConnection, error) {
			sends.Add(1)
			return nil, errors.New("history consultation must not start a provider session")
		}
	}
	h := newHarness(t, nil, configure)
	doc := h.d.document
	doc.editor.SetText("Pergunta anterior\n\nPergunta atual")
	doc.processEditor.SetText("Resultado manual preservado")
	doc.processHistory = []processing.Turn{{Prompt: "Pedido anterior", Response: "Resultado manual preservado"}}
	h.d.selectAudioMode(doc, voiceModeLive)
	files := responseFiles{folder: responseFolder(h.d.directory, h.d.deps.now())}
	partialPCM, finalPCM := []byte{1, 0, 2, 0}, []byte{3, 0, 4, 0, 5, 0}
	partial, err := files.append("partial", partialPCM, 24000)
	if err != nil {
		t.Fatal(err)
	}
	final, err := files.append("final", finalPCM, 24000)
	if err != nil {
		_ = files.close()
		t.Fatal(err)
	}
	if err := files.close(); err != nil {
		t.Fatal(err)
	}
	doc.voice.History = []voiceTurn{
		{ID: "partial", UserText: "Pergunta anterior", Text: "Resposta parcial", AudioPath: partial,
			SampleRate: 24000, Interrupted: true, Created: h.d.deps.now().UTC()},
		{ID: "final", UserText: "Pergunta atual", Text: "Resposta concluída", AudioPath: final,
			SampleRate: 24000, Complete: true, Created: h.d.deps.now().UTC()},
	}
	h.d.renderVoiceHistory(doc)
	expectedHistory := append([]voiceTurn(nil), doc.voice.History...)
	expectedProcessing := append([]processing.Turn(nil), doc.processHistory...)
	original := filepath.Join(recordingFolder(h.d.directory), h.d.deps.now().Format("2006-01-02"), "microphone.wav")
	writeCleanupFile(t, original, []byte("original recording"))
	protected := []string{
		filepath.Join(h.d.directory, "transcricoes", "export.wav"),
		filepath.Join(h.d.directory, "transcricoes", "export.txt"),
		filepath.Join(recordingFolder(h.d.directory), "importados", "original.wav"),
		filepath.Join(recordingFolder(h.d.directory), "respostas", "export.wav"),
		filepath.Join(recordingFolder(h.d.directory), "respostas", "2026-99-99", "invalid-date.wav"),
		filepath.Join(files.folder, "exportados", "export.wav"),
		filepath.Join(t.TempDir(), "external-original.wav"),
	}
	for _, path := range protected {
		writeCleanupFile(t, path, []byte("protected user data"))
	}

	items, err := h.d.scanCleanupFiles(cleanupRecordings)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("preview must contain the capture and both response files, got %+v", items)
	}
	var partialItem cleanupItem
	var previewBytes int64
	for _, item := range items {
		previewBytes += item.Size
		switch item.Source {
		case partial:
			partialItem = item
		case final, original:
		default:
			t.Fatalf("preview included protected data: %+v", item)
		}
	}
	if partialItem.Source == "" || previewBytes != int64(44+len(partialPCM)+44+len(finalPCM)+len("original recording")) {
		t.Fatalf("preview omitted a response or reported the wrong byte count: %+v / %d", items, previewBytes)
	}
	if err := h.d.saveWorkspace(); err != nil {
		t.Fatal(err)
	}
	reopened := newHarness(t, nil, configure)
	reopened.d.directory = h.d.directory
	if !reopened.d.loadWorkspace() {
		t.Fatal("workspace did not reopen")
	}
	current := reopened.d.document
	if !reflect.DeepEqual(current.voice.History, expectedHistory) ||
		!reflect.DeepEqual(current.processHistory, expectedProcessing) {
		t.Fatalf("partial/final response or processing history changed on reopen: %+v", current.voice.History)
	}
	for index, pcm := range [][]byte{partialPCM, finalPCM} {
		actual, rate, err := readVoiceAudio(current.voice.History[index].AudioPath)
		if err != nil || rate != 24000 || !bytes.Equal(actual, pcm) {
			t.Fatalf("reopened response %d lost audio: %v / %d / %v", index, actual, rate, err)
		}
	}
	test.Tap(current.voice.back)
	test.Tap(current.voice.next)
	if sends.Load() != 0 || reopened.d.voiceSession != nil {
		t.Fatal("consulting restored response history sent data to the provider")
	}

	recycleTarget := filepath.Join(t.TempDir(), "recycled-response.wav")
	recycled := false
	err = reopened.d.recycleCleanupItem(reopened.d.ctx, partialItem, func(_ context.Context, paths []string) error {
		if len(paths) != 1 || paths[0] != partial {
			t.Fatalf("recycle received unrelated files: %v", paths)
		}
		recycled = true
		return os.Rename(paths[0], recycleTarget)
	})
	if err != nil || !recycled {
		t.Fatalf("response did not reach the recycle boundary: %v", err)
	}
	if err := reopened.d.applyCleanupItems([]cleanupItem{partialItem}); err != nil {
		t.Fatal(err)
	}
	expectedHistory[0].Missing = true
	if !reflect.DeepEqual(current.voice.History, expectedHistory) ||
		!reflect.DeepEqual(current.processHistory, expectedProcessing) ||
		current.editor.Text != "Pergunta anterior\n\nPergunta atual" ||
		current.processEditor.Text != "Resultado manual preservado" {
		t.Fatalf("audio removal changed text or history: %+v", current.voice.History)
	}
	actual, rate, err := readVoiceAudio(recycleTarget)
	if err != nil || rate != 24000 || !bytes.Equal(actual, partialPCM) {
		t.Fatalf("recycled partial response was not recoverable: %v / %d / %v", actual, rate, err)
	}
	for _, path := range append(protected, original) {
		expected := []byte("protected user data")
		if path == original {
			expected = []byte("original recording")
		}
		if actual, err := os.ReadFile(path); err != nil || !bytes.Equal(actual, expected) {
			t.Fatalf("cleanup changed protected/original data %q: %v", path, err)
		}
	}
	items, err = reopened.d.scanCleanupFiles(cleanupRecordings)
	if err != nil || len(items) != 2 {
		t.Fatalf("preview retained removed audio or lost remaining audio: %+v / %v", items, err)
	}
	afterCleanup := newHarness(t, nil, configure)
	afterCleanup.d.directory = h.d.directory
	if !afterCleanup.d.loadWorkspace() ||
		!reflect.DeepEqual(afterCleanup.d.document.voice.History, expectedHistory) ||
		!reflect.DeepEqual(afterCleanup.d.document.processHistory, expectedProcessing) ||
		afterCleanup.d.editor.Text != "Pergunta anterior\n\nPergunta atual" ||
		afterCleanup.d.processEditor.Text != "Resultado manual preservado" || sends.Load() != 0 {
		t.Fatal("cleanup state/text/history failed to reopen without sending data")
	}
}

func TestVoiceAudioCleanupRemainsUnavailableAfterUndoingCombinedDocuments(t *testing.T) {
	now := time.Now()
	configure := func(deps *dependencies) { deps.now = func() time.Time { return now } }
	h := newHarness(t, nil, configure)
	files := responseFiles{folder: responseFolder(h.d.directory, h.d.deps.now())}
	defer files.close()
	firstPath, err := files.append("first", []byte{1, 0}, 24000)
	if err != nil {
		t.Fatal(err)
	}
	secondPath, err := files.append("second", []byte{2, 0}, 24000)
	if err != nil {
		t.Fatal(err)
	}
	if err := files.close(); err != nil {
		t.Fatal(err)
	}
	first := h.d.document
	first.editor.SetText("Primeira pergunta")
	h.d.selectAudioMode(first, voiceModeLive)
	first.voice.History = []voiceTurn{{
		ID: "first", UserText: "Primeira pergunta", Text: "Primeira resposta", AudioPath: firstPath,
		SampleRate: 24000, Complete: true, Created: h.d.deps.now().UTC(),
	}}
	second := h.d.addDocument()
	second.editor.SetText("Segunda pergunta")
	second.processEditor.SetText("Resultado anterior preservado")
	second.processHistory = []processing.Turn{{Prompt: "Pedido anterior", Response: "Resultado anterior preservado"}}
	h.d.selectAudioMode(second, voiceModeLive)
	second.voice.History = []voiceTurn{{
		ID: "second", UserText: "Segunda pergunta", Text: "Segunda resposta parcial", AudioPath: secondPath,
		SampleRate: 24000, Interrupted: true, Created: h.d.deps.now().UTC(),
	}}
	expectedFirst := append([]voiceTurn(nil), first.voice.History...)
	expectedSecond := append([]voiceTurn(nil), second.voice.History...)
	expectedSecond[0].Missing = true
	if err := h.d.applyCombinedText(h.d.combineSources(), "Primeira pergunta\n\nSegunda pergunta"); err != nil {
		t.Fatal(err)
	}
	if len(h.d.closedTabs) != 1 || h.d.closedTabs[0].combined == nil || !second.closed {
		t.Fatal("combination did not create a detached undo snapshot")
	}
	item := cleanupItem{Category: cleanupRecordings, Source: secondPath, Paths: []string{secondPath}}
	recycleTarget := filepath.Join(t.TempDir(), "recycled-response.wav")
	if err := h.d.recycleCleanupItem(h.d.ctx, item, func(_ context.Context, paths []string) error {
		if len(paths) != 1 || paths[0] != secondPath {
			t.Fatalf("recycle received unrelated files: %v", paths)
		}
		return os.Rename(paths[0], recycleTarget)
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.d.applyCleanupItems([]cleanupItem{item}); err != nil {
		t.Fatal(err)
	}
	h.d.undoCombinedDocuments(h.d.closedTabs[0])
	if len(h.d.documents) != 2 || second.closed ||
		!reflect.DeepEqual(first.voice.History, expectedFirst) ||
		!reflect.DeepEqual(second.voice.History, expectedSecond) ||
		second.editor.Text != "Segunda pergunta" || second.processEditor.Text != "Resultado anterior preservado" ||
		len(second.processHistory) != 1 || second.processHistory[0].Response != "Resultado anterior preservado" {
		t.Fatalf("undo restored removed audio or changed text/history: first=%+v second=%+v",
			first.voice.History, second.voice.History)
	}
	h.d.renderVoiceHistory(second)
	if !second.voice.play.Disabled() {
		t.Fatal("undo enabled playback of a response sent to recycle")
	}
	if pcm, _, err := readVoiceAudio(firstPath); err != nil || !bytes.Equal(pcm, []byte{1, 0}) {
		t.Fatalf("cleanup changed the other response audio: %v / %v", pcm, err)
	}
	reopened := newHarness(t, nil, configure)
	reopened.d.directory = h.d.directory
	if !reopened.d.loadWorkspace() || len(reopened.d.documents) != 2 ||
		!reflect.DeepEqual(reopened.d.documents[1].voice.History, expectedSecond) {
		t.Fatal("undo cleanup state did not persist on reopen")
	}
}

func TestVoiceCleanupBlocksResponseBeforeItsUICallbackWithoutWaitingForFileIO(t *testing.T) {
	h := newHarness(t, nil, nil)
	autoplay := false
	h.d.settings.Conversation.Autoplay = &autoplay
	doc := h.d.document
	h.d.selectAudioMode(doc, voiceModeLive)
	h.drain()
	session := &voiceSession{doc: doc, files: responseFiles{folder: responseFolder(h.d.directory, h.d.deps.now())}}
	defer session.files.close()
	h.d.voiceSession = session
	path, end, err := session.files.appendBlock("pending", []byte{1, 0}, 24000)
	if err != nil {
		t.Fatal(err)
	}
	h.d.post(func() {
		h.d.receiveVoiceEvent(session, realtime.Event{
			Kind: realtime.AssistantAudio, CompletionID: "pending", PCM: []byte{1, 0}, SampleRate: 24000,
		}, path, end)
	})
	if len(doc.voice.History) != 0 {
		t.Fatal("the response UI callback was not left pending")
	}
	items, err := h.d.scanCleanupFiles(cleanupRecordings)
	if err != nil || len(items) != 1 || items[0].Source != path {
		t.Fatalf("new response did not appear in the cleanup preview: %+v / %v", items, err)
	}
	session.files.mu.Lock()
	locked := true
	defer func() {
		if locked {
			session.files.mu.Unlock()
		}
	}()
	checked := make(chan string, 1)
	go func() { checked <- h.d.cleanupItemInUse(items[0]) }()
	select {
	case message := <-checked:
		if message == "" {
			t.Fatal("cleanup allowed the active WAV before its UI callback")
		}
	case <-time.After(time.Second):
		t.Fatal("UI cleanup waited for the response file IO mutex")
	}
	session.files.mu.Unlock()
	locked = false
	if message := h.d.cleanupItemInUse(cleanupItem{Category: cleanupRecordings, Paths: []string{path}}); message == "" {
		t.Fatal("cleanup missed an active response referenced by the item paths")
	}
	h.drain()
	if len(doc.voice.History) != 1 || doc.voice.History[0].AudioPath != path {
		t.Fatal("pending callback did not preserve the response in history")
	}
	h.d.voiceSession = nil
	if message := h.d.cleanupItemInUse(items[0]); message != "" {
		t.Fatalf("response remained blocked after the session ended: %s", message)
	}
}
