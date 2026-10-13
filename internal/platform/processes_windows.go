package platform

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// Hide configures a command to run without a console window. It modifies the
// existing SysProcAttr, preserving all options besides enabling HideWindow and
// adding CREATE_NO_WINDOW. Call it before starting the command.
func Hide(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NO_WINDOW
}
