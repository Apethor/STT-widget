package platform

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"reflect"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestHidePreservesProcessOptions(t *testing.T) {
	t.Parallel()
	options := &syscall.SysProcAttr{
		CmdLine:                    "credential-helper --json",
		CreationFlags:              windows.CREATE_NEW_PROCESS_GROUP,
		NoInheritHandles:           true,
		AdditionalInheritedHandles: []syscall.Handle{123, 456},
		ParentProcess:              syscall.Handle(789),
		ProcessAttributes:          &syscall.SecurityAttributes{Length: 24, InheritHandle: 1},
		ThreadAttributes:           &syscall.SecurityAttributes{Length: 24, InheritHandle: 1},
	}
	want := *options
	want.HideWindow = true
	want.CreationFlags |= windows.CREATE_NO_WINDOW
	var output bytes.Buffer
	command := exec.Command("not-executed")
	command.SysProcAttr = options
	command.Stdout = &output
	command.Stderr = &output

	Hide(command)
	Hide(command)
	if command.SysProcAttr != options {
		t.Fatal("Hide replaced the existing SysProcAttr")
	}
	if !reflect.DeepEqual(*options, want) {
		t.Fatalf("process options = %+v, want %+v", *options, want)
	}
	if command.Stdout != &output || command.Stderr != &output {
		t.Fatal("Hide changed the command's output streams")
	}
}

func TestHideWithNoOptions(t *testing.T) {
	t.Parallel()
	command := exec.Command("not-executed")
	Hide(command)
	if command.SysProcAttr == nil || !command.SysProcAttr.HideWindow {
		t.Fatal("Hide did not set HideWindow")
	}
	if command.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatal("Hide did not add CREATE_NO_WINDOW")
	}
	Hide(nil)
}

func TestHidePreventsConsoleWindow(t *testing.T) {
	t.Parallel()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "-test.run=^TestHiddenProcessHelper$")
	command.Env = append(os.Environ(), "AI_TALKER_PLATFORM_HIDDEN_HELPER=1")
	Hide(command)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("hidden helper: %v\n%s", err, output)
	}
}

func TestHiddenProcessHelper(t *testing.T) {
	if os.Getenv("AI_TALKER_PLATFORM_HIDDEN_HELPER") != "1" {
		t.Skip("subprocess helper")
	}
	window, _, _ := kernel32.NewProc("GetConsoleWindow").Call()
	if window != 0 {
		t.Fatal("hidden child process owns a console window")
	}
}
