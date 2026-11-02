package core_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/providers"
	"github.com/jasonli0226/jas/providers/fake"
)

func TestRun_ContextFullStops(t *testing.T) {
	r := newRig(t, []fake.Turn{
		{Calls: []fake.Call{{Name: "read", Args: `{"path":"big.txt"}`}}},
		{Text: "never"},
	})
	assert.NoErr(t, os.WriteFile(filepath.Join(r.root, "big.txt"), []byte(strings.Repeat("0123456789\n", 5000)), 0o644))
	r.cfg.Budget = &core.Budget{Window: 1000, ElideAt: core.DefaultElideAt, Keep: core.DefaultKeepToolResults}
	_, err := core.Run(context.Background(), r.cfg)
	assert.Eq(t, exitCode(t, err), core.ExitContextFull)
	assert.Eq(t, len(r.prov.Requests), 1)
	end := lastEnded(t, r.events(t))
	assert.Eq(t, end["status"].(string), "context_full")
}

func TestRun_ElidesAndLogsEvent(t *testing.T) {
	var turns []fake.Turn
	for i := 0; i < 3; i++ {
		turns = append(turns, fake.Turn{Calls: []fake.Call{{Name: "read", Args: `{"path":"big.txt"}`}}, Usage: providers.Usage{PromptTokens: 1}})
	}
	turns = append(turns, fake.Turn{Text: "done"})
	r := newRig(t, turns)
	assert.NoErr(t, os.WriteFile(filepath.Join(r.root, "big.txt"), []byte(strings.Repeat("0123456789\n", 100)), 0o644))
	r.cfg.Budget = &core.Budget{Window: 500, ElideAt: 0.48, Keep: 1} // trigger 240 tokens
	_, err := core.Run(context.Background(), r.cfg)
	assert.NoErr(t, err)
	var ev map[string]any
	for _, e := range r.events(t) {
		if e["type"] == "elided" {
			ev = e
		}
	}
	if ev == nil {
		t.Fatal("no elided event")
	}
	for _, k := range []string{"count", "tokens_before", "tokens_after"} {
		if _, ok := ev[k]; !ok {
			t.Fatalf("missing %s: %v", k, ev)
		}
	}
	last := r.prov.Requests[len(r.prov.Requests)-1].Messages
	var stubs int
	for _, s := range results(last) {
		if strings.HasPrefix(s, "[elided: read big.txt") {
			stubs++
		}
	}
	if stubs == 0 {
		t.Fatal("no stubs sent to provider")
	}
}

func TestRun_HugeSystemPromptStopsBeforeFirstRequest(t *testing.T) {
	r := newRig(t, []fake.Turn{{Text: "never"}})
	r.cfg.System = core.BuildSystem(core.BasePrompt, strings.Repeat("rule\n", 4000))
	r.cfg.Budget = &core.Budget{Window: 2000, ElideAt: core.DefaultElideAt, Keep: core.DefaultKeepToolResults}
	_, err := core.Run(context.Background(), r.cfg)
	assert.Eq(t, exitCode(t, err), core.ExitContextFull)
	assert.Eq(t, len(r.prov.Requests), 0)
}
