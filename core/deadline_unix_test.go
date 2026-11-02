//go:build unix

package core_test

import (
	"context"
	"testing"
	"time"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/providers/fake"
	"github.com/jasonli0226/jas/tools"
)

func TestRun_DeadlineKillsShell(t *testing.T) {
	r := newRig(t, []fake.Turn{
		{Calls: []fake.Call{{Name: "shell", Args: `{"command":"sleep 30"}`}}},
		{Text: "never"},
	}, tools.Shell{})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := core.Run(ctx, r.cfg)
	if time.Since(start) > 5*time.Second {
		t.Fatalf("shell not killed: took %v", time.Since(start))
	}
	assert.Eq(t, exitCode(t, err), core.ExitBudget)
	assert.Eq(t, lastEnded(t, r.events(t))["reason"], any("timeout"))
}
