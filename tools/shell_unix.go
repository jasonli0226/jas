//go:build unix

package tools

import (
	"context"
	"os"
	"os/exec"
	"syscall"
)

// extraBaseEnv are platform-specific names the shell child inherits on unix.
var extraBaseEnv []string

// envCaseFold: unix environment names are case-sensitive.
const envCaseFold = false

// shellInvocation describes the shell used for the tool description.
func shellInvocation() string { return "sh -c" }

// procTree is the shell's process group: the child runs in its own group so
// cancel and cleanup can kill the whole tree.
type procTree struct{ pid int }

// attach records the started shell; its group already exists (Setpgid).
func (t *procTree) attach(p *os.Process) error {
	t.pid = p.Pid
	return nil
}

// kill kills the group; ESRCH when it is already gone is fine.
func (t *procTree) kill() {
	if t.pid > 0 {
		_ = syscall.Kill(-t.pid, syscall.SIGKILL)
	}
}

func (t *procTree) close() {}

// shellSpawn builds the shell command and the tree that owns its processes.
func shellSpawn(tctx context.Context, command string) (*exec.Cmd, *procTree, error) {
	cmd := exec.CommandContext(tctx, "sh", "-c", command)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	return cmd, &procTree{}, nil
}
