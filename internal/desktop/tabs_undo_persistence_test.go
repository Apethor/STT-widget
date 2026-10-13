package desktop

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"ai-talker/internal/processing"
	"ai-talker/internal/store"
)

func undoHydrationHarness(t *testing.T, now time.Time) *testHarness {
	t.Helper()
	return newHarness(t, nil, func(deps *dependencies) {
		deps.now = func() time.Time { return now }
	})
}

func reopenUndoWorkspace(t *testing.T, directory string, now time.Time) *testHarness {
	t.Helper()
	h := undoHydrationHarness(t, now)
	h.d.directory = directory
	if !h.d.loadWorkspace() || h.d.workspaceInvalid {
		payload, err := os.ReadFile(filepath.Join(directory, "workspace-go-v1.json"))
		t.Fatalf("valid workspace with recoverable tabs failed to reopen: %v\n%s", err, payload)
	}
	return h
}

func TestUndoHydrationKeepsRemainingDeadlineFullStateAndConsumedEntries(t *testing.T) {
	start := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	h := undoHydrationHarness(t, start)
	first := h.d.document
	first.editor.SetText("Primeira transcrição preservada")
	first.processEditor.SetText("Resultado manual preservado")
	first.processHistory = []processing.Turn{{Prompt: "Pedido enviado", Response: "Resposta enviada"}}
	first.voice = &voiceDocument{Mode: voiceModeRecording, History: []voiceTurn{{
		ID: "partial", UserText: "Fala", Text: "Resposta parcial", SampleRate: 24000,
		Interrupted: true, Missing: true, Created: start,
	}}}
	first.transcriptZoom, first.processZoom = 140, 120
	if err := h.d.renameDocument(first, "Minha aba"); err != nil {
		t.Fatal(err)
	}
	second := h.d.addDocument()
	second.editor.SetText("Segunda transcrição")
	h.d.addDocument()
	h.d.addDocument()
	h.d.closeDocument(first.item)
	h.d.deps.now = func() time.Time { return start.Add(20 * time.Second) }
	h.d.closeDocument(second.item)
	deadline := start.Add(50 * time.Second)
	if len(h.d.closedTabs) != 2 || !h.d.closedTabs[0].until.Equal(deadline) || !h.d.closedTabs[1].until.Equal(deadline) {
		t.Fatal("new tab closure did not renew thirty seconds for the whole stack")
	}
	nextID := h.d.nextDocument
	reopened := reopenUndoWorkspace(t, h.d.directory, start.Add(25*time.Second))
	if len(reopened.d.documents) != 2 || reopened.d.nextDocument != nextID || reopened.d.nextGroup != h.d.nextGroup ||
		len(reopened.d.closedTabs) != 2 || !reopened.d.closedTab.until.Equal(deadline) ||
		reopened.d.closedTab.until.Sub(reopened.d.deps.now()) != 25*time.Second {
		t.Fatal("hydration changed open tabs/IDs or restarted the deadline")
	}
	reopened.d.undoCloseDocument()
	if reopened.d.document.id != second.id || reopened.d.document.editor.Text != "Segunda transcrição" {
		t.Fatal("hydrated closure stack did not restore the most recent tab first")
	}
	again := reopenUndoWorkspace(t, h.d.directory, start.Add(45*time.Second))
	if len(again.d.closedTabs) != 1 || again.d.closedTab.document.id != first.id ||
		again.d.closedTab.until.Sub(again.d.deps.now()) != 5*time.Second {
		t.Fatal("consumed Undo returned after reopening or the remaining deadline changed")
	}
	again.d.undoCloseDocument()
	restored := again.d.document
	if restored.id != first.id || restored.name != "Minha aba" ||
		restored.editor.Text != "Primeira transcrição preservada" || restored.processEditor.Text != "Resultado manual preservado" ||
		!reflect.DeepEqual(restored.processHistory, first.processHistory) ||
		!reflect.DeepEqual(restored.voice.History, first.voice.History) ||
		restored.transcriptZoom != 140 || restored.processZoom != 120 ||
		restored.busy || restored.processBusy || restored.jobCancel != nil || restored.processCancel != nil {
		t.Fatal("hydration lost document data or restored cancelled task ownership")
	}
	final := reopenUndoWorkspace(t, h.d.directory, start.Add(46*time.Second))
	if len(final.d.documents) != 4 || len(final.d.closedTabs) != 0 || final.d.undoCloseButton.Visible() {
		t.Fatal("a fully consumed closure stack was offered again after reopening")
	}
}

func TestUndoHydrationExpiresWhileClosedAndIgnoresExpiredInvalidRecovery(t *testing.T) {
	start := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	h := undoHydrationHarness(t, start)
	original := h.d.document
	original.editor.SetText("Texto preservado em rascunho")
	h.d.addDocument()
	h.d.closeDocument(original.item)
	path := filepath.Join(h.d.directory, "workspace-go-v1.json")
	var saved workspaceSnapshot
	if err := store.ReadJSON(path, &saved); err != nil {
		t.Fatal(err)
	}
	// Expired recovery data must not invalidate the active tabs.
	saved.ClosedTabs[0].State.ID = 0
	if err := store.WriteJSON(path, saved); err != nil {
		t.Fatal(err)
	}
	reopened := reopenUndoWorkspace(t, h.d.directory, start.Add(30*time.Second))
	if len(reopened.d.closedTabs) != 0 || reopened.d.undoCloseButton.Visible() || len(reopened.d.documents) != 1 {
		t.Fatal("expired recovery was restored or rejected valid open tabs")
	}
	reopened.d.undoCloseDocument()
	if len(reopened.d.documents) != 1 {
		t.Fatal("expired Undo restored a closed document")
	}
	closed := reopened.d.document
	reopened.d.closeDocument(closed.item)
	if len(reopened.d.closedTabs) != 1 || reopened.d.closedTab.document.id != closed.id ||
		!reopened.d.closedTab.until.Equal(start.Add(60*time.Second)) {
		t.Fatal("a new closure recovered an expired entry or failed to start a new thirty-second sequence")
	}
}

func TestUndoHydrationRestoresRemovedGroupAndPreservesReplacementWork(t *testing.T) {
	start := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	for _, custom := range []bool{false, true} {
		name := "removed default group"
		if custom {
			name = "removed custom group"
		}
		t.Run(name, func(t *testing.T) {
			h := undoHydrationHarness(t, start)
			first := h.d.document
			first.editor.SetText("Texto do grupo removido")
			wantName, wantMarker, wantWidth := "Grupo 1", "Grupo 1", float32(0)
			if custom {
				wantName, wantMarker, wantWidth = "Grupo personalizado", "", 184
				h.d.groupNames[first.group] = wantName
				delete(h.d.groupAutomaticNames, first.group)
				h.d.groupNameWidths[first.group] = wantWidth
			}
			other := h.d.addGroup()
			h.d.closeDocument(first.item)
			reopened := reopenUndoWorkspace(t, h.d.directory, start.Add(7*time.Second))
			if len(reopened.d.groups) != 1 || reopened.d.groups[0] != other.group {
				t.Fatal("hydration added a closed group to the visible workspace before Undo")
			}
			reopened.d.undoCloseDocument()
			if len(reopened.d.groups) != 2 || reopened.d.groups[0] != first.group ||
				reopened.d.groupNames[first.group] != wantName ||
				reopened.d.groupAutomaticNames[first.group] != wantMarker ||
				reopened.d.groupNameWidths[first.group] != wantWidth ||
				reopened.d.document.id != first.id || reopened.d.document.editor.Text != "Texto do grupo removido" {
				t.Fatal("hydrated Undo lost the removed group position/name/automatic marker/width or its tab")
			}
			again := reopenUndoWorkspace(t, h.d.directory, start.Add(8*time.Second))
			if again.d.groupNames[first.group] != wantName || again.d.groupAutomaticNames[first.group] != wantMarker {
				t.Fatal("restored default/custom group metadata was not valid after reopening")
			}
		})
	}
	for _, kind := range []string{"untouched", "text", "presentation"} {
		t.Run(kind, func(t *testing.T) {
			h := undoHydrationHarness(t, start)
			original := h.d.document
			original.editor.SetText("Texto da aba original")
			h.d.closeDocument(original.item)
			replacement := h.d.document
			switch kind {
			case "text":
				replacement.editor.SetText("Trabalho novo")
			case "presentation":
				replacement.editors.Offset = 0.37
				h.d.rememberPanelSizes(replacement)
				if replacement.presentationEpoch == 0 {
					t.Fatal("presentation fixture did not mark the replacement as used")
				}
			}
			if err := h.d.saveWorkspace(); err != nil {
				t.Fatal(err)
			}
			reopened := reopenUndoWorkspace(t, h.d.directory, start.Add(10*time.Second))
			if reopened.d.closedTab.replacement != reopened.d.document {
				t.Fatal("replacement identity was cloned instead of matching the open tab")
			}
			reopened.d.undoCloseDocument()
			want := 1
			if kind != "untouched" {
				want = 2
			}
			if len(reopened.d.documents) != want || reopened.d.document.id != original.id {
				t.Fatal("hydrated Undo removed replacement work or retained an untouched placeholder")
			}
			if kind == "text" && reopened.d.documents[1].editor.Text != "Trabalho novo" {
				t.Fatal("hydrated Undo changed the replacement text")
			}
		})
	}
}

func TestUndoGroupClosureRenewsOtherEntriesAndPersistsOwnRemoval(t *testing.T) {
	start := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	h := undoHydrationHarness(t, start)
	first := h.d.document
	first.editor.SetText("Aba recuperável do primeiro grupo")
	h.d.addDocument()
	h.d.closeDocument(first.item)
	other := h.d.addGroup()
	closedOther := h.d.addDocument()
	h.d.closeDocument(closedOther.item)
	h.d.deps.now = func() time.Time { return start.Add(29 * time.Second) }
	h.d.closeGroup(other.group)
	if len(h.d.closedTabs) != 1 || h.d.closedTab.document.id != first.id ||
		!h.d.closedTab.until.Equal(start.Add(59*time.Second)) {
		t.Fatal("closing a group did not remove its entries and renew the other group recovery")
	}
	reopened := reopenUndoWorkspace(t, h.d.directory, start.Add(31*time.Second))
	if len(reopened.d.closedTabs) != 1 || reopened.d.closedTab.document.id != first.id ||
		!reopened.d.closedTab.until.Equal(start.Add(59*time.Second)) {
		t.Fatal("group closure recovery changes were not persisted atomically")
	}
	reopened.d.undoCloseDocument()
	if reopened.d.document.id != first.id || reopened.d.document.editor.Text != "Aba recuperável do primeiro grupo" {
		t.Fatal("closing another group lost the original Undo")
	}
}

func TestUndoHydrationCombinedAliasesAndRemovedScreenshotsStayExact(t *testing.T) {
	start := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	h := undoHydrationHarness(t, start)
	first := h.d.document
	screenshot := filepath.Join(h.d.directory, "screenshots", "deleted.png")
	writeCleanupFile(t, screenshot, []byte("isolated synthetic image"))
	first.editor.SetText("Primeira fala\n" + screenshot)
	first.screenshotFiles = []string{screenshot}
	first.processEditor.SetText("Resultado manual original")
	first.processHistory = []processing.Turn{{Prompt: "Pedido original", Response: "Resposta original"}}
	first.voice = &voiceDocument{Mode: voiceModeRecording, History: []voiceTurn{{
		ID: "voice", UserText: "Fala antiga", Text: "Resposta antiga", Missing: true, Created: start, Complete: true,
	}}}
	second := h.d.addDocument()
	second.editor.SetText("Segunda fala")
	if err := h.d.applyCombinedTextWithLabel(h.d.combineSources(), "Primeira combinação", "Juntar"); err != nil {
		t.Fatal(err)
	}
	third := h.d.addDocument()
	third.editor.SetText("Terceira fala")
	if err := h.d.applyCombinedTextWithLabel(h.d.combineSources(), "Segunda combinação", "Mesclar"); err != nil {
		t.Fatal(err)
	}
	h.d.markScreenshotRemoved(screenshot)
	if err := os.Remove(screenshot); err != nil {
		t.Fatal(err)
	}
	if err := h.d.saveWorkspace(); err != nil {
		t.Fatal(err)
	}
	reopened := reopenUndoWorkspace(t, h.d.directory, start.Add(10*time.Second))
	if reopened.d.document.editor.Text != "Segunda combinação" || len(reopened.d.closedTabs) != 2 ||
		reopened.d.closedTabs[0].document != reopened.d.document || reopened.d.closedTabs[1].document != reopened.d.document {
		t.Fatal("hydration overwrote the current combination or duplicated its shared tab identity")
	}
	reopened.d.undoCloseDocument()
	if len(reopened.d.documents) != 2 || reopened.d.documents[0].id != first.id ||
		reopened.d.documents[1].id != third.id || reopened.d.documents[0].editor.Text != "Primeira combinação" {
		t.Fatal("hydrated Mesclar did not restore the previous combined pair")
	}
	again := reopenUndoWorkspace(t, h.d.directory, start.Add(15*time.Second))
	again.d.undoCloseDocument()
	restored := again.d.document
	if len(again.d.documents) != 3 || again.d.documents[1].id != second.id || again.d.documents[2].id != third.id ||
		!strings.Contains(restored.editor.Text, "[Screenshot removida]") ||
		strings.Contains(restored.editor.Text, screenshot) || slices.Contains(restored.screenshotFiles, screenshot) ||
		restored.processEditor.Text != "Resultado manual original" ||
		!reflect.DeepEqual(restored.processHistory, first.processHistory) ||
		!reflect.DeepEqual(restored.voice.History, first.voice.History) ||
		restored.busy || restored.processBusy || restored.jobCancel != nil || restored.processCancel != nil {
		t.Fatal("hydrated Juntar resurrected a screenshot/task or lost original text/history/voice")
	}
}

func TestUndoHydrationWritesQueueAtomicallyAndProtectsInvalidActiveArchive(t *testing.T) {
	start := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	h := undoHydrationHarness(t, start)
	first := h.d.document
	first.editor.SetText("Texto arquivado")
	second := h.d.addDocument()
	h.d.addDocument()
	h.d.closeDocument(first.item)
	path := filepath.Join(h.d.directory, "workspace-go-v1.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	until := h.d.closedTab.until
	h.d.deps.now = func() time.Time { return start.Add(20 * time.Second) }
	h.d.deps.writeWorkspace = func(string, workspaceSnapshot) error { return errors.New("isolated save failure") }
	h.d.closeDocument(second.item)
	h.d.undoCloseDocument()
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) || second.closed ||
		len(h.d.closedTabs) != 1 || !h.d.closedTab.until.Equal(until) || !first.closed {
		t.Fatal("failed writes changed the persisted recovery queue, its deadline, or document ownership")
	}
	h.d.deps.writeWorkspace = nil
	var saved workspaceSnapshot
	if err := store.ReadJSON(path, &saved); err != nil {
		t.Fatal(err)
	}
	saved.ClosedTabs[0].State.ID = 0
	if err := store.WriteJSON(path, saved); err != nil {
		t.Fatal(err)
	}
	invalidBytes, _ := os.ReadFile(path)
	reopened := undoHydrationHarness(t, start.Add(21*time.Second))
	reopened.d.directory = h.d.directory
	if !reopened.d.loadWorkspace() || !reopened.d.workspaceInvalid {
		t.Fatal("invalid active recovery snapshots were accepted")
	}
	preserved, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(invalidBytes, preserved) {
		t.Fatal("invalid recovery input was overwritten during loading")
	}
	saved.ClosedTabs = nil
	if err := store.WriteJSON(path, saved); err != nil {
		t.Fatal(err)
	}
	legacy := reopenUndoWorkspace(t, h.d.directory, start.Add(22*time.Second))
	if len(legacy.d.documents) != 2 || legacy.d.closedTab != nil {
		t.Fatal("workspace without optional recovery fields failed backward compatibility")
	}
}

func TestUndoHydrationCombinationAfterMovesPrunesEmptyGroups(t *testing.T) {
	start := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	h := undoHydrationHarness(t, start)
	first := h.d.document
	first.editor.SetText("Primeira fala")
	second := h.d.addDocument()
	second.editor.SetText("Segunda fala")
	originalGroup := first.group
	if err := h.d.applyCombinedText(h.d.combineSources(), "Texto combinado"); err != nil {
		t.Fatal(err)
	}
	other := h.d.addGroup()
	other.editor.SetText("Outra fala")
	last := h.d.addGroup()
	last.editor.SetText("Última fala")
	emptyAfterUndo := other.group
	h.d.moveDocumentTab(first.item, other.group, 0)
	h.d.moveDocumentTab(other.item, last.group, 1)
	reopened := reopenUndoWorkspace(t, h.d.directory, start.Add(10*time.Second))
	if len(reopened.d.groups) != 2 || slices.Contains(reopened.d.groups, originalGroup) ||
		reopened.d.closedTab.document.group != emptyAfterUndo {
		t.Fatal("moved combination fixture did not hydrate its current group")
	}
	reopened.d.undoCloseDocument()
	if len(reopened.d.groups) != 2 || reopened.d.groups[0] != originalGroup ||
		slices.Contains(reopened.d.groups, emptyAfterUndo) || len(reopened.d.documents) != 4 ||
		reopened.d.documents[0].id != first.id || reopened.d.documents[1].id != second.id {
		t.Fatal("Undo of a moved combination left an empty group or lost original group/tab order")
	}
	if _, exists := reopened.d.groupNames[emptyAfterUndo]; exists {
		t.Fatal("empty moved group kept its name metadata")
	}
	if _, exists := reopened.d.groupAutomaticNames[emptyAfterUndo]; exists {
		t.Fatal("empty moved group kept its automatic name marker")
	}
	if _, exists := reopened.d.groupNameWidths[emptyAfterUndo]; exists {
		t.Fatal("empty moved group kept its width metadata")
	}
	again := reopenUndoWorkspace(t, h.d.directory, start.Add(15*time.Second))
	if len(again.d.groups) != 2 || len(again.d.documents) != 4 || again.d.closedTab != nil {
		t.Fatal("Undo of a moved combination persisted an invalid workspace")
	}
}

func TestUndoHydrationClearedHistoricalGroupCannotReturnAfterMovingCombination(t *testing.T) {
	for _, action := range []string{"close", "clear"} {
		t.Run(action, func(t *testing.T) {
			start := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
			h := undoHydrationHarness(t, start)
			first := h.d.document
			first.editor.SetText("Primeira fala")
			second := h.d.addDocument()
			second.editor.SetText("Segunda fala")
			historicalGroup := first.group
			if err := h.d.applyCombinedText(h.d.combineSources(), "Texto combinado preservado"); err != nil {
				t.Fatal(err)
			}
			h.d.addDocument() // Keeps the historical group present after moving its combined tab.
			target := h.d.addGroup()
			other := h.d.addGroup()
			otherClosed := h.d.addDocument()
			h.d.closeDocument(otherClosed.item)
			h.d.moveDocumentTab(first.item, target.group, 0)
			h.d.deps.now = func() time.Time { return start.Add(20 * time.Second) }
			if action == "close" {
				h.d.closeGroup(historicalGroup)
			} else {
				h.d.clearDocuments(historicalGroup)
			}
			if len(h.d.closedTabs) != 1 || h.d.closedTab.document.id != otherClosed.id {
				t.Fatal("explicit group removal kept its moved recovery or discarded another group's Undo")
			}
			reopened := reopenUndoWorkspace(t, h.d.directory, start.Add(21*time.Second))
			if len(reopened.d.closedTabs) != 1 || reopened.d.closedTab.document.id != otherClosed.id {
				t.Fatal("group recovery removal was not persisted")
			}
			reopened.d.undoCloseDocument()
			for _, doc := range reopened.d.documents {
				if doc.id == second.id || doc.id == first.id &&
					(doc.group != target.group || doc.editor.Text != "Texto combinado preservado") {
					t.Fatal("Undo resurrected a cleared historical source or changed the surviving combined text")
				}
			}
			if len(reopened.d.groupDocuments(other.group)) != 2 {
				t.Fatal("the unrelated group's recoverable tab was lost")
			}
			again := reopenUndoWorkspace(t, h.d.directory, start.Add(22*time.Second))
			if again.d.closedTab != nil {
				t.Fatal("discarded/consumed group recovery returned after a second reopen")
			}
		})
	}
}

func TestUndoHydrationRejectsDuplicateIdentitiesAndInvalidAutomaticGroupMarkers(t *testing.T) {
	start := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	h := undoHydrationHarness(t, start)
	first := h.d.document
	first.editor.SetText("Texto recuperável")
	h.d.addDocument()
	h.d.closeDocument(first.item)
	var original workspaceSnapshot
	if err := store.ReadJSON(filepath.Join(h.d.directory, "workspace-go-v1.json"), &original); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		name   string
		change func(*workspaceSnapshot)
	}{
		{"duplicate ordinary identity", func(snapshot *workspaceSnapshot) {
			snapshot.ClosedTabs = append(snapshot.ClosedTabs, snapshot.ClosedTabs[0])
		}},
		{"ordinary identity overlaps an open tab", func(snapshot *workspaceSnapshot) {
			snapshot.ClosedTabs[0].State = snapshot.Tabs[0]
		}},
		{"invalid automatic group marker", func(snapshot *workspaceSnapshot) {
			snapshot.ClosedTabs[0].GroupAutomaticName = "Grupo 999"
		}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			snapshot, err := detachedWorkspace(original)
			if err != nil {
				t.Fatal(err)
			}
			scenario.change(&snapshot)
			reopened := undoHydrationHarness(t, start.Add(time.Second))
			path := filepath.Join(reopened.d.directory, "workspace-go-v1.json")
			if err := store.WriteJSON(path, snapshot); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(path)
			if !reopened.d.loadWorkspace() || !reopened.d.workspaceInvalid || len(reopened.d.closedTabs) != 0 {
				t.Fatal("invalid active recovery was offered in the UI")
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("invalid recovery file was overwritten")
			}
		})
	}
}
