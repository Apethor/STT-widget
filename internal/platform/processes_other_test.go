//go:build !windows

package platform

import (
	"os/exec"
	"reflect"
	"syscall"
	"testing"
)

func TestHidePreservesProcessOptions(t *testing.T) {
	t.Parallel()
	command := exec.Command("not-executed")
	options := &syscall.SysProcAttr{}
	command.SysProcAttr = options
	before := *options
	Hide(command)
	if command.SysProcAttr != options || !reflect.DeepEqual(*options, before) {
		t.Fatal("Hide changed process options on a non-Windows platform")
	}

	command.SysProcAttr = nil
	Hide(command)
	if command.SysProcAttr != nil {
		t.Fatal("Hide created process options on a non-Windows platform")
	}
	Hide(nil)
}
