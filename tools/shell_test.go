//go:build unix

package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
)

func shellArgs(t *testing.T, cmd string, timeout int) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"command": cmd, "timeout_seconds": timeout})
	assert.NoErr(t, err)
	return string(b)
}

// alive reports whether pid is a running (non-zombie) process.
func alive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	if b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil {
		if i := strings.LastIndexByte(string(b), ')'); i >= 0 && len(b) > i+2 && b[i+2] == 'Z' {
			return false
		}
	}
	return true
}

func readPid(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil {
			if n, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				return n
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("pid file %s never appeared", path)
	return 0
}

func waitDead(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !alive(pid) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	syscall.Kill(pid, syscall.SIGKILL)
	t.Fatalf("process %d survived", pid)
}

func TestShell_EnvAllowlist(t *testing.T) {
	t.Setenv("JAS_API_KEY", "sk-test")
	t.Setenv("JAS_EXTRA_OK", "visible")
	p := policy(t)
	const probe = `echo "extra=${JAS_EXTRA_OK-unset} key=${JAS_API_KEY-unset} path=${PATH:+set}"`
	r := call(t, Shell{}, p, shellArgs(t, probe, 0))
	assert.Eq(t, r.IsError, false)
	assert.Eq(t, strings.TrimSpace(r.Output), "extra=unset key=unset path=set")
	p.Env = []string{"JAS_EXTRA_OK"}
	r = call(t, Shell{}, p, shellArgs(t, probe, 0))
	assert.Eq(t, strings.TrimSpace(r.Output), "extra=visible key=unset path=set")
}

func TestShell_EnvOnlyInCommandPosition(t *testing.T) {
	p := policy(t)
	for _, cmd := range []string{
		"go env GOPATH >/dev/null; echo ok",
		"cat <<'EOF'\n#!/usr/bin/env python\nEOF\necho ok",
		"echo python -m venv env",
		"echo --env-file x",
	} {
		r := call(t, Shell{}, p, shellArgs(t, cmd, 0))
		if r.IsError || strings.Contains(r.Output, "blocked") {
			t.Errorf("%q: want run, got %+v", cmd, r)
		}
	}
	for _, cmd := range []string{"env", "printenv", "ls; env", "echo $(printenv)", "true && env", "FOO=1 env", "echo x | /usr/bin/env", "sudo env", "(env)", "echo `env`"} {
		r := call(t, Shell{}, p, shellArgs(t, cmd, 0))
		if !r.IsError || !strings.Contains(r.Output, "blocked by deny pattern") {
			t.Errorf("%q: want blocked, got %+v", cmd, r)
		}
	}
}

func TestShell_RunsInRoot(t *testing.T) {
	p := policy(t)
	r := call(t, Shell{}, p, `{"command":"pwd"}`)
	assert.Eq(t, strings.TrimSpace(r.Output), p.Root)
}

func TestShell_NonZeroExit(t *testing.T) {
	r := call(t, Shell{}, policy(t), `{"command":"echo hi; exit 3"}`)
	assert.Eq(t, r.IsError, false)
	assert.Eq(t, strings.HasSuffix(r.Output, "\n[exit status 3]"), true)
	assert.Contains(t, r.Output, "hi")
}

func TestShell_CombinesStderr(t *testing.T) {
	r := call(t, Shell{}, policy(t), `{"command":"echo out; echo err 1>&2"}`)
	assert.Contains(t, r.Output, "out")
	assert.Contains(t, r.Output, "err")
}

func TestShell_BadArgs(t *testing.T) {
	r := call(t, Shell{}, policy(t), `{"command":""}`)
	assert.Eq(t, r.IsError, true)
	r = call(t, Shell{}, policy(t), `not json`)
	assert.Eq(t, r.IsError, true)
}

func TestShell_TimeoutKillsGroup(t *testing.T) {
	p := policy(t)
	pidFile := filepath.Join(p.Root, "pid")
	start := time.Now()
	r := call(t, Shell{}, p, shellArgs(t, "sleep 30 & echo $! > pid; sleep 30", 1))
	assert.Eq(t, time.Since(start) < 4*time.Second, true)
	assert.Eq(t, r.IsError, true)
	assert.Contains(t, r.Output, "timed out after 1s")
	waitDead(t, readPid(t, pidFile))
}

func TestShell_BackgroundChildKilledAfterNormalExit(t *testing.T) {
	p := policy(t)
	r := call(t, Shell{}, p, `{"command":"sleep 30 >/dev/null 2>&1 & echo $! > pid"}`)
	assert.Eq(t, r.IsError, false)
	waitDead(t, readPid(t, filepath.Join(p.Root, "pid")))
}

func TestShell_BackgroundChildHoldingPipeDoesNotStall(t *testing.T) {
	p := policy(t)
	start := time.Now()
	r := call(t, Shell{}, p, `{"command":"sleep 30 & echo $! > pid"}`)
	assert.Eq(t, r.IsError, false)
	assert.Eq(t, time.Since(start) < time.Second, true)
	waitDead(t, readPid(t, filepath.Join(p.Root, "pid")))
}

func TestShell_TimeoutKeepsPartialOutput(t *testing.T) {
	r := call(t, Shell{}, policy(t), shellArgs(t, "echo partial; sleep 30", 1))
	assert.Eq(t, r.IsError, true)
	assert.Contains(t, r.Output, "partial")
	assert.Contains(t, r.Output, "[timed out after 1s]")
}

func TestShellEnv_NeverNil(t *testing.T) {
	for _, n := range baseEnv {
		t.Setenv(n, "x")
		os.Unsetenv(n)
	}
	env := ShellEnv(nil)
	assert.Eq(t, env != nil, true)
	assert.Eq(t, len(env), 0)
}

func TestShell_TimeoutResolution(t *testing.T) {
	assert.Eq(t, shellTimeout(core.ExecPolicy{}, 99999), maxShellTimeout)
	assert.Eq(t, shellTimeout(core.ExecPolicy{}, 0), defaultShellTimeout)
	assert.Eq(t, shellTimeout(core.ExecPolicy{ShellTimeout: 7 * time.Second}, 0), 7*time.Second)
	assert.Eq(t, shellTimeout(core.ExecPolicy{ShellTimeout: 7 * time.Second}, 3), 3*time.Second)
}

func TestShell_CancelKillsGrandchildren(t *testing.T) {
	p := policy(t)
	pidFile := filepath.Join(p.Root, "pid")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := Shell{}.Call(ctx, p, json.RawMessage(shellArgs(t, "sleep 30 & echo $! > pid; wait", 0)))
		done <- err
	}()
	pid := readPid(t, pidFile)
	cancel()
	select {
	case err := <-done:
		assert.Eq(t, err != nil, true)
	case <-time.After(5 * time.Second):
		t.Fatal("Call did not return after cancel")
	}
	waitDead(t, pid)
}

func TestShell_OutputCapped(t *testing.T) {
	p := policy(t)
	p.MaxOutput = 1000
	r := call(t, Shell{}, p, `{"command":"yes | head -c 100000"}`)
	assert.Contains(t, r.Output, "[truncated")
	assert.Eq(t, len(r.Output) < 2000, true)
}

func TestShell_HugeOutputBounded(t *testing.T) {
	p := policy(t)
	p.MaxOutput = 1000
	r := call(t, Shell{}, p, `{"command":"head -c 50000000 /dev/zero | tr '\\0' 'a'; exit 2"}`)
	assert.Contains(t, r.Output, "[truncated")
	assert.Contains(t, r.Output, "[exit status 2]")
}

func TestMutating_IncludesShell(t *testing.T) {
	var names []string
	for _, tl := range Mutating() {
		names = append(names, tl.Name())
	}
	assert.Eq(t, strings.Join(names, ","), "write,edit,shell")
}

func TestShell_BlocksPrintenvBestEffort(t *testing.T) {
	p := policy(t)
	write(t, p, "marker", "x")
	const want = "blocked by deny pattern (best-effort, not a security boundary)"
	for _, cmd := range []string{
		"printenv", "env", "env | sort", "echo hi; /usr/bin/env", "cat .env", "cat sub/.env.local",
		"cat 'secrets/a'", "cat ./key.pem", "head -1 id_rsa", "cat ~/.ssh/id_ed25519",
		"cat $HOME/.aws/credentials", "cat " + p.Root + "/.env", "x=.env; cat \"$x\"x",
	} {
		r := call(t, Shell{}, p, shellArgs(t, "touch ran; "+cmd, 0))
		if !r.IsError || r.Output != want {
			t.Errorf("%q: want blocked, got %+v", cmd, r)
		}
		if _, err := os.Stat(filepath.Join(p.Root, "ran")); err == nil {
			t.Fatalf("%q: command ran", cmd)
		}
	}
	r := call(t, Shell{}, p, shellArgs(t, "cat marker && echo envoy.go", 0))
	assert.Eq(t, r.IsError, false)
	assert.Contains(t, r.Output, "x")
}
