package desktop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ai-talker/internal/config"
	"ai-talker/internal/store"
)

func TestSecondLaunchRequestsActivationAndReturnsSuccess(t *testing.T) {
	directory := t.TempDir()
	first, err := acquireInstance(directory)
	if err != nil || first == nil {
		t.Fatalf("a primeira instância não adquiriu a trava: %v", err)
	}
	t.Cleanup(func() {
		if err := first.close(); err != nil {
			t.Error(err)
		}
	})
	// Even a nil service is irrelevant to a second launch: no window or cloud
	// setup should be required to ask the already running process to activate.
	if err := Run(context.Background(), config.Default(), directory, nil, ""); err != nil {
		t.Fatalf("a segunda abertura deveria terminar com sucesso: %v", err)
	}
	activated, err := first.consumeActivation()
	if err != nil || !activated {
		t.Fatalf("a primeira instância não recebeu a ativação: %v", err)
	}
	activated, err = first.consumeActivation()
	if err != nil || activated {
		t.Fatal("a solicitação de ativação não foi consumida uma única vez")
	}
	if err := first.close(); err != nil {
		t.Fatal(err)
	}
	next, err := acquireInstance(directory)
	if err != nil || next == nil {
		t.Fatalf("a trava não foi liberada ao encerrar: %v", err)
	}
	if err := next.close(); err != nil {
		t.Fatal(err)
	}
}

func TestSecondLaunchExplainsAnOlderRunningRelease(t *testing.T) {
	directory := t.TempDir()
	first, err := acquireInstance(directory)
	if err != nil || first == nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.close() })
	if err := store.WriteJSON(filepath.Join(directory, "gui-instance.json"), map[string]any{
		"version": "0.9.0", "revision": "old", "executable": "older.exe", "pid": os.Getpid(),
	}); err != nil {
		t.Fatal(err)
	}
	err = Run(context.Background(), config.Default(), directory, nil, "")
	if err == nil || !strings.Contains(err.Error(), "0.9.0") ||
		!strings.Contains(err.Error(), "feche") {
		t.Fatal("abrir outra versão reativou a antiga sem explicar como carregar a atualização", err)
	}
	activated, err := first.consumeActivation()
	if err != nil || !activated {
		t.Fatal("a janela anterior não foi ativada ou suas tarefas foram interrompidas")
	}
}

func TestSecondLaunchExplainsALegacyInstanceWithoutVersionMetadata(t *testing.T) {
	directory := t.TempDir()
	first, err := acquireInstance(directory)
	if err != nil || first == nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.close() })
	_ = os.Remove(filepath.Join(directory, "gui-instance.json"))
	err = Run(context.Background(), config.Default(), directory, nil, "")
	if err == nil || !strings.Contains(err.Error(), "identificação") ||
		!strings.Contains(err.Error(), "feche") {
		t.Fatal("a abertura da versão nova não explicou a instância anterior sem identificação", err)
	}
}

func TestActivationFailureDoesNotStopTick(t *testing.T) {
	h := newHarness(t, nil, nil)
	instance, err := acquireInstance(h.d.directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := instance.close(); err != nil {
			t.Error(err)
		}
	})
	h.d.instance = instance
	if err := os.MkdirAll(filepath.Join(instance.marker, "bloqueio"), 0o700); err != nil {
		t.Fatal(err)
	}
	h.d.guard(h.d.tick)()
	if h.d.closing || h.d.clearButton.Disabled() || h.d.errorLabel.Visible() {
		t.Fatal("a falha temporária do marcador interrompeu o loop")
	}
}

func TestActivationMarkerReopensHiddenJobWithoutCancellingIt(t *testing.T) {
	h := newHarness(t, nil, nil)
	instance, err := acquireInstance(h.d.directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := instance.close(); err != nil {
			t.Error(err)
		}
	})
	h.d.instance = instance
	h.d.busy = true
	h.d.hidden = true
	h.d.closeAfterJob = true
	if err := os.WriteFile(instance.marker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	h.d.tick()
	h.settle()
	if h.d.hidden || h.d.closeAfterJob || !h.d.busy || h.d.ctx.Err() != nil {
		t.Fatal("reabrir pelo launcher cancelou a tarefa ou manteve o fechamento automático")
	}
	requireAbsent(t, instance.marker)
}

func TestRunRejectsInvalidArgumentsAndReleasesLockOnFailure(t *testing.T) {
	directory := t.TempDir()
	if err := Run(nil, config.Default(), directory, nil, ""); err == nil {
		t.Fatal("Run deveria rejeitar contexto nil")
	}
	if err := Run(context.Background(), config.Default(), "", nil, ""); err == nil {
		t.Fatal("Run deveria rejeitar pasta vazia")
	}
	if err := Run(context.Background(), config.Default(), directory, nil, ""); err == nil {
		t.Fatal("Run deveria rejeitar um serviço ausente na primeira abertura")
	}
	instance, err := acquireInstance(directory)
	if err != nil || instance == nil {
		t.Fatalf("uma falha de inicialização deixou a trava presa: %v", err)
	}
	if err := instance.close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = Run(ctx, config.Default(), directory, runnerFunc(nil), "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run deveria devolver o contexto cancelado: %v", err)
	}
}
