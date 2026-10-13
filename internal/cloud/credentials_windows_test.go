//go:build windows

package cloud

import (
	"errors"
	"os/exec"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

func seedProcessOptions(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags:    windows.CREATE_NEW_PROCESS_GROUP,
		NoInheritHandles: true,
	}
}

func assertHiddenProcess(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.HideWindow ||
		cmd.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Error("credential process is not configured to remain hidden")
	}
}

func assertPreservedProcessOptions(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if cmd.SysProcAttr.CreationFlags&windows.CREATE_NEW_PROCESS_GROUP == 0 ||
		!cmd.SysProcAttr.NoInheritHandles {
		t.Error("existing Windows process options were replaced")
	}
}

func credentialHelperVisibleConsole() bool {
	kernel := windows.NewLazySystemDLL("kernel32.dll")
	// GetConsoleWindow and IsWindowVisible report through their return values;
	// their documented contracts do not define GetLastError on success.
	handle, _, _ := kernel.NewProc("GetConsoleWindow").Call()
	if handle == 0 {
		return false
	}
	visible, _, _ := windows.NewLazySystemDLL("user32.dll").NewProc("IsWindowVisible").Call(handle)
	return visible != 0
}

func assertHelperExited(t *testing.T, pid int) {
	t.Helper()
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return
	}
	if err != nil {
		t.Fatal("query synthetic helper:", err)
	}
	defer func() {
		if err := windows.CloseHandle(handle); err != nil {
			t.Error(err)
		}
	}()
	state, err := windows.WaitForSingleObject(handle, 0)
	if err != nil {
		t.Fatal(err)
	}
	if state != windows.WAIT_OBJECT_0 {
		t.Errorf("synthetic credential helper %d survived cloud.Close", pid)
	}
}
