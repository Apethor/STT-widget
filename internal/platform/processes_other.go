//go:build !windows

package platform

import "os/exec"

// Hide is a no-op on platforms that do not create Windows console windows.
func Hide(*exec.Cmd) {}
