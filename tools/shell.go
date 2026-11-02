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
	"regexp"
	"runtime"
	"strings"
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
// extra names, taken from os.Environ(). An extra entry of the form KEY=VALUE
// sets KEY to VALUE instead, replacing any inherited value.
func ShellEnv(extra []string) []string {
	names := append(append([]string{}, baseEnv...), extraBaseEnv...)
	var set []string
	for _, e := range extra {
		if strings.Contains(e, "=") {
			set = append(set, e)
			continue
		}
		names = append(names, e)
	}
	env := filterEnv(os.Environ(), names, envCaseFold)
	if len(set) == 0 {
		return env
	}
	key := func(kv string) string {
		k, _, _ := strings.Cut(kv, "=")
		if envCaseFold {
			return strings.ToUpper(k)
		}
		return k
	}
	override := map[string]bool{}
	for _, kv := range set {
		override[key(kv)] = true
	}
	out := make([]string, 0, len(env)+len(set))
	for _, kv := range env {
		if !override[key(kv)] {
			out = append(out, kv)
		}
	}
	return append(out, set...)
}

// filterEnv keeps the environ entries whose name is in names. With fold set,
// names compare case-insensitively (Windows spells PATH as Path). The result
// is never nil: a nil Cmd.Env would make the child inherit everything.
func filterEnv(environ, names []string, fold bool) []string {
	key := func(n string) string { return n }
	if fold {
		key = strings.ToUpper
	}
	allowed := make(map[string]bool, len(names))
	for _, n := range names {
		allowed[key(n)] = true
	}
	env := []string{}
	for _, kv := range environ {
		if name, _, ok := strings.Cut(kv, "="); ok && name != "" && allowed[key(name)] {
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

var envCmdRe = regexp.MustCompile(`(?m)(?:^|[;|&(\x60]|\$\()\s*(?:\w+=\S*\s+)*(?:(?:sudo|exec)\s+)*(?:[./~][^\s;|&()\x60]*/)?(?:env|printenv)(?:\s|$|[;|&)\x60])`)

// shellTokenSep splits a command line into candidate path words.
func shellTokenSep(r rune) bool {
	return strings.ContainsRune(" \t\r\n;|&<>()\"'`=,{}", r)
}

// blockedCommand is the shell's best-effort deny filter: it refuses commands
// that run env/printenv (in command position) or mention any word matching the deny floor. It is NOT a
// security boundary; a shell can always construct paths this does not see.
func blockedCommand(p core.ExecPolicy, cmd string) bool {
	if envCmdRe.MatchString(cmd) {
		return true
	}
	home, _ := os.UserHomeDir()
	cmd = normalizeCmd(cmd, home, runtime.GOOS == "windows")
	for _, tok := range strings.FieldsFunc(cmd, shellTokenSep) {
		if p.DeniedToken(tok) {
			return true
		}
	}
	return false
}

var userProfileRe = regexp.MustCompile(`(?i)%USERPROFILE%`)

// normalizeCmd undoes the cheapest ways to hide a path from the deny filter:
// it expands the home variable and drops the shell's escape character (\ for
// sh; ^ for cmd.exe, where \ is the path separator and must stay).
func normalizeCmd(cmd, home string, windows bool) string {
	if windows {
		if home != "" {
			cmd = userProfileRe.ReplaceAllLiteralString(cmd, home)
		}
		return strings.ReplaceAll(cmd, "^", "")
	}
	if home != "" {
		cmd = strings.NewReplacer("${HOME}", home, "$HOME", home).Replace(cmd)
	}
	return strings.ReplaceAll(cmd, `\`, "")
}

// Shell runs a command in the platform shell in the project root.
type Shell struct{}

func (Shell) Name() string { return "shell" }
func (Shell) Description() string {
	return "Run a shell command (" + shellInvocation() + ") in the project root. Returns combined stdout and stderr, and the exit status when non-zero."
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
	if blockedCommand(p, a.Command) {
		return errResult("blocked by deny pattern (best-effort, not a security boundary)")
	}
	timeout := shellTimeout(p, a.Timeout)
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	limit := p.OutputLimit()
	out := &cappedBuffer{max: limit * captureFactor}
	_, runErr := RunShell(tctx, p.Root, a.Command, ShellEnv(p.Env), out)
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

// RunShell runs cmd through the platform shell in dir with env, writing
// stdout and stderr to out. The whole process tree is killed when the shell
// exits or ctx ends, so grandchildren never outlive the call. It returns the
// exit code (-1 when the command did not run to an exit), and an error that
// is an *exec.ExitError for a non-zero exit, or why the command could not run.
func RunShell(ctx context.Context, dir, cmdline string, env []string, out io.Writer) (int, error) {
	// Own the pipe (instead of cmd.Stdout = writer) so Wait returns as soon as
	// the shell exits, even when a background grandchild still holds the pipe.
	pr, pw, err := os.Pipe()
	if err != nil {
		return -1, err
	}
	defer pr.Close()
	cmd, tree, err := shellSpawn(ctx, cmdline)
	if err != nil {
		pw.Close()
		return -1, err
	}
	defer tree.close()
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout = pw
	cmd.Stderr = pw
	cmd.WaitDelay = shellWaitDelay

	copied := make(chan struct{})
	go func() {
		defer close(copied)
		_, _ = io.Copy(out, pr)
	}()
	startErr := cmd.Start()
	pw.Close()
	if startErr != nil {
		return -1, startErr
	}
	if err := tree.attach(cmd.Process); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return -1, err
	}
	runErr := cmd.Wait()
	// Grandchildren must not outlive the call: kill the tree on every path
	// (already gone is fine), which also closes the pipe.
	tree.kill()
	select {
	case <-copied:
	case <-time.After(shellWaitDelay):
	}
	if ctx.Err() != nil {
		return -1, ctx.Err()
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		return exitErr.ExitCode(), runErr
	}
	if runErr != nil {
		return -1, runErr
	}
	return 0, nil
}
