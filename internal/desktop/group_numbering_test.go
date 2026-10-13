package desktop

import (
	"errors"
	"testing"
)

func TestNewGroupReusesSmallestFreeNameAndKeepsExistingNames(t *testing.T) {
	h := newHarness(t, nil, nil)
	first := h.d.document
	second := h.d.addGroup()
	if err := h.d.renameGroup(second.group, "Grupo 18"); err != nil {
		t.Fatal(err)
	}
	h.d.closeGroup(second.group)
	next := h.d.addGroup()
	if next == nil || next.group == second.group || h.d.groupName(next.group) != "Grupo 2" ||
		h.d.groupName(first.group) != "Grupo 1" {
		t.Fatal("o nome continuou o contador permanente ou uma identidade foi reutilizada")
	}
	if err := h.d.renameGroup(next.group, "Grupo 3"); err != nil {
		t.Fatal(err)
	}
	third := h.d.addGroup()
	if h.d.groupName(third.group) != "Grupo 2" || h.d.groupName(next.group) != "Grupo 3" {
		t.Fatal("a lacuna não foi reutilizada ou um nome existente mudou")
	}
}

func TestCustomGroupNameOccupiesItsNumberAndPersists(t *testing.T) {
	h := newHarness(t, nil, nil)
	second := h.d.addGroup()
	if err := h.d.renameGroup(second.group, "Aulas"); err != nil {
		t.Fatal(err)
	}
	third := h.d.addGroup()
	if err := h.d.renameGroup(third.group, "Grupo 2"); err != nil {
		t.Fatal(err)
	}
	next := h.d.addGroup()
	if h.d.groupName(next.group) != "Grupo 3" || h.d.groupName(second.group) != "Aulas" {
		t.Fatal("um nome personalizado foi sobrescrito ou duplicado")
	}
	restored := newHarness(t, nil, nil)
	restored.d.directory = h.d.directory
	if !restored.d.loadWorkspace() || restored.d.nextGroupName() != "Grupo 4" {
		t.Fatal("reabrir perdeu os nomes ocupados")
	}
}

func TestFailedNewGroupDoesNotReserveItsName(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.d.deps.writeWorkspace = func(string, workspaceSnapshot) error {
		return errors.New("disco indisponível")
	}
	if h.d.addGroup() != nil || len(h.d.groups) != 1 || h.d.nextGroupName() != "Grupo 2" {
		t.Fatal("a criação malsucedida consumiu um nome")
	}
}

func TestClosingOnlyGroupReusesFirstNameAndKeepsNewIdentity(t *testing.T) {
	h := newHarness(t, nil, nil)
	for range 3 {
		previous := h.d.document.group
		h.d.closeGroup(previous)
		if h.d.document.group <= previous || len(h.d.groups) != 1 ||
			h.d.groupName(h.d.document.group) != "Grupo 1" {
			t.Fatal("fechar o único grupo consumiu um nome ou reutilizou a identidade")
		}
	}
}
