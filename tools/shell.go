//go:build unix

package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/jasonli0226/jas/core"
)

const (
	defaultShellTimeout = 120 * time.Second
	maxShellTimeout     = 600 * time.Second
	shellWaitDelay      = 2 * time.Second
	// captureFactor bounds captured bytes to this multiple of the output limit.
	captureFactor = 4
)

// baseEnv are the variable names every shell child inherits.
var baseEnv = []string{"PATH", "HOME", "USER", "LANG", "TERM", "TMPDIR"}

// ShellEnv builds the child environment: only the base allowlist plus the
// extra names, taken from os.Environ().
func ShellEnv(extra []string) []string {
	allowed := make(map[string]bool, len(baseEnv)+len(extra))
	for _, n := range baseEnv {
		allowed[n] = true
	}
	for _, n := range extra {
		allowed[n] = true
	}
	// Non-nil on purpose: a nil Cmd.Env would make the child inherit everything.
	env := []string{}
	for _, kv := range os.Environ() {
		if name, _, ok := strings.Cut(kv, "="); ok && allowed[name] {
			env = append(env, kv)
		}
	}
	return env
}

// cappedBuffer keeps the first max bytes written and counts the rest.
type cappedBuffer struct {
	buf     bytes.Buffer
	max     int
	dropped int64
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	room := c.max - c.buf.Len()
	if room > len(p) {
		room = len(p)
	}
	if room > 0 {
		c.buf.Write(p[:room])
	}
	c.dropped += int64(len(p) - max(room, 0))
	return len(p), nil
}

// shellTimeout resolves the effective timeout: per-call arg, else policy, else default; capped.
func shellTimeout(p core.ExecPolicy, secs int) time.Duration {
	d := defaultShellTimeout
	if p.ShellTimeout > 0 {
		d = p.ShellTimeout
	}
	if secs > 0 {
		d = time.Duration(secs) * time.Second
	}
	return min(d, maxShellTimeout)
}

// Shell runs a command with sh -c in the project root.
type Shell struct{}

func (Shell) Name() string { return "shell" }
func (Shell) Description() string {
	return "Run a shell command (sh -c) in the project root. Returns combined stdout and stderr, and the exit status when non-zero."
}
func (Shell) Kind() core.Kind { return core.Mutating }
func (Shell) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{` +
		`"command":{"type":"string","description":"The command line to run."},` +
		`"timeout_seconds":{"type":"integer","description":"Timeout in seconds (default 120, max 600)."}},` +
		`"required":["command"]}`)
}

func (Shell) Call(ctx context.Context, p core.ExecPolicy, args json.RawMessage) (core.Result, error) {
	var a struct {
		Command string `json:"command"`
		Timeout int    `json:"timeout_seconds"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return errResult("invalid arguments: %v", err)
	}
	if strings.TrimSpace(a.Command) == "" {
		return errResult("command is required")
	}
	timeout := shellTimeout(p, a.Timeout)
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	limit := p.OutputLimit()
	out := &cappedBuffer{max: limit * captureFactor}
	// Own the pipe (instead of cmd.Stdout = writer) so Wait returns as soon as
	// the shell exits, even when a background grandchild still holds the pipe.
	pr, pw, err := os.Pipe()
	if err != nil {
		return errResult("could not run command: %v", err)
	}
	defer pr.Close()
	cmd := exec.CommandContext(tctx, "sh", "-c", a.Command)
	cmd.Dir = p.Root
	cmd.Env = ShellEnv(p.Env)
	cmd.Stdout = pw
	cmd.Stderr = pw
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = shellWaitDelay

	copied := make(chan struct{})
	go func() {
		defer close(copied)
		_, _ = io.Copy(out, pr)
	}()
	startErr := cmd.Start()
	pw.Close()
	if startErr != nil {
		return errResult("could not run command: %v", startErr)
	}
	pgid := cmd.Process.Pid
	runErr := cmd.Wait()
	// Grandchildren must not outlive the call: kill the group on every path
	// (ESRCH when it is already gone is fine), which also closes the pipe.
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	select {
	case <-copied:
	case <-time.After(shellWaitDelay):
	}
	if ctx.Err() != nil {
		return core.Result{}, fmt.Errorf("shell: cancelled: %w", ctx.Err())
	}
	text := Truncate(out.buf.Bytes(), limit)
	if out.dropped > 0 && !strings.Contains(text, "[truncated") {
		text += fmt.Sprintf("\n[truncated: %d more bytes]", out.dropped)
	}
	if errors.Is(tctx.Err(), context.DeadlineExceeded) {
		return core.Result{Output: text + fmt.Sprintf("\n[timed out after %s]", timeout), IsError: true}, nil
	}
	var exitErr *exec.ExitError
	if runErr != nil && !errors.As(runErr, &exitErr) {
		return errResult("could not run command: %v", runErr)
	}
	if exitErr != nil {
		text += fmt.Sprintf("\n[exit status %d]", exitErr.ExitCode())
	}
	return core.Result{Output: text}, nil
}
