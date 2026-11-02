package core

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"
)

// CheckTruncatedMarker starts a check output whose head was dropped.
const CheckTruncatedMarker = "[earlier output truncated]\n"

// defaultCheckOutput is the output tail kept when CheckSpec.MaxOutput is 0.
const defaultCheckOutput = 32 * 1024

// CheckSpec is a project check command jas runs itself (the farm verdict,
// spec runs): unlike a model's shell call, its exit code decides the outcome.
type CheckSpec struct {
	Cmd       string
	Dir       string
	Env       []string      // the shell's allowlisted environment
	Timeout   time.Duration // 0 = none
	MaxOutput int           // bytes kept from the end of the output; 0 = 32 KiB
}

// CheckResult is how a check ended. ExitCode is -1 when the command never
// ran to an exit (it could not start, or it timed out or was cancelled).
type CheckResult struct {
	ExitCode int
	Duration time.Duration
	Output   string
	TimedOut bool
}

// RunCheck runs s.Cmd with run (tools.RunShell; core does not import tools)
// and keeps the tail of its output, where test failures usually are.
func RunCheck(ctx context.Context, s CheckSpec, run func(ctx context.Context, dir, cmd string, env []string, out io.Writer) (int, error)) CheckResult {
	if s.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.Timeout)
		defer cancel()
	}
	limit := s.MaxOutput
	if limit <= 0 {
		limit = defaultCheckOutput
	}
	out := &tailBuffer{max: limit}
	start := time.Now()
	code, err := run(ctx, s.Dir, s.Cmd, s.Env, out)
	r := CheckResult{ExitCode: code, Duration: time.Since(start), Output: out.String()}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		r.TimedOut, r.ExitCode = true, -1
	}
	if err != nil && code == 0 { // could not run: never report success
		r.ExitCode = -1
	}
	return r
}

// LogCheck writes check_ran with the redacted output tail.
func LogCheck(log EventLog, s CheckSpec, r CheckResult, redact func(string) string) error {
	out := r.Output
	if redact != nil {
		out = redact(out)
	}
	return log.Append(EvCheckRan, map[string]any{
		"cmd": s.Cmd, "dir": s.Dir, "exit_code": r.ExitCode, "duration_ms": r.Duration.Milliseconds(),
		"timed_out": r.TimedOut, "output": out,
	})
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	mu      sync.Mutex
	max     int
	buf     []byte
	dropped bool
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.max; over > 0 {
		t.buf = append(t.buf[:0], t.buf[over:]...)
		t.dropped = true
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.dropped {
		return CheckTruncatedMarker + string(t.buf)
	}
	return string(t.buf)
}
