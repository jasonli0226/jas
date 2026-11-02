//go:build unix

package core_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/internal/testutil"
	"github.com/jasonli0226/jas/tools"
)

func check(t *testing.T, s core.CheckSpec) core.CheckResult {
	t.Helper()
	if s.Dir == "" {
		s.Dir = t.TempDir()
	}
	return core.RunCheck(context.Background(), s, tools.RunShell)
}

func TestRunCheck_PassAndFail(t *testing.T) {
	assert.Eq(t, check(t, core.CheckSpec{Cmd: "true"}).ExitCode, 0)
	assert.Eq(t, check(t, core.CheckSpec{Cmd: "exit 3"}).ExitCode, 3)
}

func TestRunCheck_TimeoutKillsGroup(t *testing.T) {
	start := time.Now()
	r := check(t, core.CheckSpec{Cmd: "sleep 30 & sleep 30", Timeout: 200 * time.Millisecond})
	if time.Since(start) > 5*time.Second {
		t.Fatalf("not killed: %v", time.Since(start))
	}
	assert.Eq(t, r.TimedOut, true)
	assert.Eq(t, r.ExitCode, -1)
}

func TestRunCheck_KeepsOutputTail(t *testing.T) {
	r := check(t, core.CheckSpec{Cmd: "head -c 10000 /dev/zero | tr '\\0' x; echo END", MaxOutput: 100})
	if !strings.HasSuffix(strings.TrimSpace(r.Output), "END") || len(r.Output) > 100+len(core.CheckTruncatedMarker) {
		t.Fatalf("bad tail (%d bytes): %q", len(r.Output), r.Output)
	}
}

func TestLogCheck_Redacts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	log, err := core.OpenEventLog(path, nil, nil)
	assert.NoErr(t, err)
	red := &core.Redactor{Exact: []string{"sk-secret-value-123456"}}
	s := core.CheckSpec{Cmd: "echo sk-secret-value-123456", Dir: t.TempDir()}
	r := core.RunCheck(context.Background(), s, tools.RunShell)
	assert.NoErr(t, core.LogCheck(log, s, r, red.Redact))
	assert.NoErr(t, log.Close())
	evs := testutil.ReadEvents(t, path)
	ev := evs[len(evs)-1]
	assert.Eq(t, ev["type"], any("check_ran"))
	assert.NotContains(t, ev["output"].(string), "sk-secret-value-123456")
	assert.Eq(t, ev["exit_code"], any(0.0))
}
