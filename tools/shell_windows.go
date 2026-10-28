//go:build windows

package tools

import (
	"context"
	"os/exec"
	"syscall"
)

// createNoWindow (CREATE_NO_WINDOW) keeps the shell from opening a console
// window.
const createNoWindow = 0x08000000

// extraBaseEnv are platform-specific names the shell child inherits on
// windows: SystemRoot is required by most Win32 programs, the rest by cmd.exe
// and common tooling.
var extraBaseEnv = []string{"SystemRoot", "USERPROFILE", "ComSpec", "windir", "TEMP", "TMP"}

// shellInvocation describes the shell used for the tool description.
func shellInvocation() string { return "cmd /d /c" }

// shellSpawn builds the shell command. On windows there is no process group;
// CommandContext's default cancel terminates the shell process and WaitDelay
// discards output inherited by stragglers, so killGroup is a no-op.
func shellSpawn(tctx context.Context, command string) (*exec.Cmd, func(pid int)) {
	cmd := exec.CommandContext(tctx, "cmd", "/d", "/c", command)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	return cmd, func(pid int) {}
}
