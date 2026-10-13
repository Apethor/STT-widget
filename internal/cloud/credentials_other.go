//go:build !windows

package cloud

import (
	"context"
	"os/exec"
)

func configureCredentialCommand(context.Context, *exec.Cmd, string) {}
