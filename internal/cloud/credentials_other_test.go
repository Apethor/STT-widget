//go:build !windows

package cloud

import (
	"os/exec"
	"testing"
)

func seedProcessOptions(*exec.Cmd) {}

func assertHiddenProcess(t *testing.T, _ *exec.Cmd) { t.Helper() }

func assertPreservedProcessOptions(t *testing.T, _ *exec.Cmd) { t.Helper() }

func credentialHelperVisibleConsole() bool { return false }

func assertHelperExited(t *testing.T, _ int) { t.Helper() }
