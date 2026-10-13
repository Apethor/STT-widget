package cloud

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func configureCredentialCommand(ctx context.Context, cmd *exec.Cmd, command string) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	// os/exec documents cmd.exe as an exception to its normal argv quoting.
	// The SDK wraps the complete credential_process string in one argument;
	// default argv escaping corrupts a quoted executable path on Windows.
	// /S removes just the added outer quotes, leaving the shared command intact.
	cmd.SysProcAttr.CmdLine = syscall.EscapeArg(cmd.Path) + ` /S /C "` + command + `"`
	cmd.Args = nil
	cmd.Cancel = func() error { return cancelCredentialCommand(ctx, cmd) }
}

func cancelCredentialCommand(_ context.Context, cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}
	processes, err := credentialProcessTree(uint32(cmd.Process.Pid))
	if err != nil {
		return errors.Join(err, cmd.Process.Kill())
	}
	// CommandContext normally kills only cmd.exe. Its credential-helper child
	// would survive shutdown. Use native calls, not another executable whose
	// startup can itself stall during CPU-heavy builds. Root comes exclusively
	// from this command's own Process, never from config.
	slices.Reverse(processes)
	var result error
	deadline := time.Now().Add(2 * time.Second)
	for _, pid := range processes {
		handle, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, pid)
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			continue // This process already exited.
		}
		if err != nil {
			result = errors.Join(result, err)
			continue
		}
		terminateErr := windows.TerminateProcess(handle, 1)
		remaining := max(0, time.Until(deadline).Milliseconds())
		state, waitErr := windows.WaitForSingleObject(handle, uint32(remaining))
		if waitErr == nil && state == windows.WAIT_OBJECT_0 {
			terminateErr = nil // An already-exited process may reject TerminateProcess.
		} else if waitErr == nil {
			waitErr = errors.New("o processo de credenciais não encerrou no prazo")
		}
		result = errors.Join(result, terminateErr, waitErr, windows.CloseHandle(handle))
	}
	return result
}

func credentialProcessTree(root uint32) ([]uint32, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	children := make(map[uint32][]uint32)
	err = windows.Process32First(snapshot, &entry)
	for err == nil {
		children[entry.ParentProcessID] = append(children[entry.ParentProcessID], entry.ProcessID)
		err = windows.Process32Next(snapshot, &entry)
	}
	if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		err = nil
	}
	err = errors.Join(err, windows.CloseHandle(snapshot))
	if err != nil {
		return nil, fmt.Errorf("listar os processos do helper de credenciais: %w", err)
	}
	result := []uint32{root}
	seen := map[uint32]bool{root: true}
	for index := 0; index < len(result); index++ {
		for _, child := range children[result[index]] {
			if !seen[child] {
				seen[child] = true
				result = append(result, child)
			}
		}
	}
	return result, nil
}
