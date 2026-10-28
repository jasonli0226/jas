//go:build unix

package tools

import (
	"context"
	"os/exec"
	"syscall"
)

// extraBaseEnv are platform-specific names the shell child inherits on unix.
var extraBaseEnv []string

// shellInvocation describes the shell used for the tool description.
func shellInvocation() string { return "sh -c" }

// shellSpawn builds the shell command. On unix the child runs in its own
// process group so cancel and cleanup can kill the whole tree. killGroup
// kills that group; ESRCH when it is already gone is fine.
func shellSpawn(tctx context.Context, command string) (*exec.Cmd, func(pid int)) {
	cmd := exec.CommandContext(tctx, "sh", "-c", command)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	return cmd, func(pid int) { _ = syscall.Kill(-pid, syscall.SIGKILL) }
}
