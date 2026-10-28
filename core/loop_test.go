package core_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/internal/testutil"
	"github.com/jasonli0226/jas/providers"
	"github.com/jasonli0226/jas/providers/fake"
	"github.com/jasonli0226/jas/tools"
)

// blockTool is a read-only tool that waits for ctx cancellation.
type blockTool struct{ started chan struct{} }

func (blockTool) Name() string            { return "block" }
func (blockTool) Description() string     { return "blocks" }
func (blockTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (blockTool) Kind() core.Kind         { return core.ReadOnly }
func (b blockTool) Call(ctx context.Context, _ core.ExecPolicy, _ json.RawMessage) (core.Result, error) {
	close(b.started)
	<-ctx.Done()
	return core.Result{}, ctx.Err()
}

type rig struct {
	cfg     core.RunConfig
	prov    *fake.Provider
	logPath string
	root    string
}

func newRig(t *testing.T, turns []fake.Turn, extra ...core.Tool) *rig {
	t.Helper()
	root := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "events.jsonl")
	log, err := core.OpenEventLog(logPath, nil, nil)
	assert.NoErr(t, err)
	t.Cleanup(func() { _ = log.Close() })
	prov := &fake.Provider{Turns: turns}
	reg := core.NewRegistry(append(tools.ReadOnly(), extra...)...)
	ex := &core.Executor{Reg: reg, Policy: core.ExecPolicy{Root: root}, Gate: core.AllowAll{}, Log: log}
	return &rig{prov: prov, logPath: logPath, root: root, cfg: core.RunConfig{
		Provider: prov, Model: "m", Prompt: "do it", System: core.BuildSystem(core.BasePrompt, ""),
		Exec: ex, Log: log, UI: core.NewQuietUI(), MaxTurns: 10,
		SessionMeta: map[string]any{"base_url": "http://x", "approve": "never", "root": root, "context_files": []string{}},
	}}
}

func (r *rig) events(t *testing.T) []map[string]any {
	t.Helper()
	return testutil.ReadEvents(t, r.logPath)
}

func types(evs []map[string]any) []string {
	var out []string
	for _, e := range evs {
		out = append(out, e["type"].(string))
	}
	return out
}

func lastEnded(t *testing.T, evs []map[string]any) map[string]any {
	t.Helper()
	last := evs[len(evs)-1]
	assert.Eq(t, last["type"].(string), "session_ended")
	return last
}

func exitCode(t *testing.T, err error) int {
	t.Helper()
	var ee *core.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("want *ExitError, got %v", err)
	}
	return ee.Code
}

func TestRun_TextOnlyFinal(t *testing.T) {
	r := newRig(t, []fake.Turn{{Text: "hello", Usage: providers.Usage{PromptTokens: 7, CompletionTokens: 2}}})
	sum, err := core.Run(context.Background(), r.cfg)
	assert.NoErr(t, err)
	assert.Eq(t, sum.Final, "hello")
	assert.Eq(t, sum.Turns, 1)
	evs := r.events(t)
	assert.Eq(t, strings.Join(types(evs), ","), "session_started,usage,assistant_message,session_ended")
	assert.Eq(t, evs[0]["model"].(string), "m")
	assert.Eq(t, evs[2]["final"].(bool), true)
	assert.Eq(t, evs[2]["text"].(string), "hello")
	end := lastEnded(t, evs)
	assert.Eq(t, end["exit_code"].(float64), 0.0)
	assert.Eq(t, end["status"].(string), "ok")
	assert.Eq(t, end["prompt_tokens"].(float64), 7.0)
}

func TestRun_ToolThenAnswer(t *testing.T) {
	r := newRig(t, []fake.Turn{
		{Calls: []fake.Call{{Name: "read", Args: `{"path":"main.go"}`}}},
		{Text: "done"},
	})
	assert.NoErr(t, os.WriteFile(filepath.Join(r.root, "main.go"), []byte("package SENTINEL"), 0o644))
	sum, err := core.Run(context.Background(), r.cfg)
	assert.NoErr(t, err)
	assert.Eq(t, sum.Turns, 2)
	assert.Eq(t, len(r.prov.Requests), 2)
	var found bool
	for _, m := range r.prov.Requests[1].Messages {
		for _, b := range m.Content {
			if b.Type == providers.BlockToolResult && strings.Contains(b.Text, "package SENTINEL") {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("tool_result with file content missing: %+v", r.prov.Requests[1].Messages)
	}
}

func TestRun_TurnCap(t *testing.T) {
	call := fake.Turn{Calls: []fake.Call{{Name: "ls", Args: `{}`}}}
	r := newRig(t, []fake.Turn{call, call, call})
	r.cfg.MaxTurns = 2
	_, err := core.Run(context.Background(), r.cfg)
	assert.Eq(t, exitCode(t, err), core.ExitTurnCap)
	end := lastEnded(t, r.events(t))
	assert.Eq(t, end["exit_code"].(float64), 4.0)
	assert.Eq(t, end["status"].(string), "turn_cap")
	assert.Eq(t, len(r.prov.Requests), 2)
}

func TestRun_ContextLengthError(t *testing.T) {
	r := newRig(t, []fake.Turn{{Err: providers.ErrContextLength}})
	_, err := core.Run(context.Background(), r.cfg)
	assert.Eq(t, exitCode(t, err), core.ExitContextFull)
	assert.Eq(t, lastEnded(t, r.events(t))["status"].(string), "context_full")
}

func TestRun_ProviderFailure(t *testing.T) {
	r := newRig(t, []fake.Turn{{Err: &providers.HTTPError{Status: 503, Body: "down"}}})
	_, err := core.Run(context.Background(), r.cfg)
	assert.Eq(t, exitCode(t, err), core.ExitFailure)
	assert.Eq(t, lastEnded(t, r.events(t))["status"].(string), "failed")
}

func TestRun_CtrlCSynthesizesCancelled(t *testing.T) {
	bt := blockTool{started: make(chan struct{})}
	r := newRig(t, []fake.Turn{{Calls: []fake.Call{
		{Name: "block", Args: `{}`}, {Name: "ls", Args: `{}`}, {Name: "read", Args: `{"path":"x"}`},
	}}}, bt)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { <-bt.started; cancel() }()
	_, err := core.Run(ctx, r.cfg)
	assert.Eq(t, exitCode(t, err), core.ExitInterrupted)
	evs := r.events(t)
	results := map[string]string{}
	started := 0
	for _, e := range evs {
		switch e["type"] {
		case "tool_started":
			started++
		case "tool_result":
			results[e["call_id"].(string)] = e["status"].(string)
		}
	}
	assert.Eq(t, len(results), 3)
	// ls/read run in the same parallel batch and may finish before the cancel;
	// the blocked call must be cancelled and none may be left without a result.
	assert.Eq(t, results["call_0_0"], "cancelled")
	assert.Eq(t, started, 3)
	end := lastEnded(t, evs)
	assert.Eq(t, end["exit_code"].(float64), 130.0)
	assert.Eq(t, end["status"].(string), "interrupted")
}

func TestRun_CancelMidStreamSynthesizesCancelled(t *testing.T) {
	r := newRig(t, []fake.Turn{{Calls: []fake.Call{{Name: "ls", Args: `{}`}}, Block: true}})
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	_, err := core.Run(ctx, r.cfg)
	assert.Eq(t, exitCode(t, err), core.ExitInterrupted)
	evs := r.events(t)
	var got []string
	for _, e := range evs {
		if e["type"] == "tool_result" {
			got = append(got, e["status"].(string))
		}
	}
	assert.Eq(t, strings.Join(got, ","), "cancelled")
	assert.Eq(t, lastEnded(t, evs)["status"].(string), "interrupted")
}

func TestRun_PromptPrefixStable(t *testing.T) {
	r := newRig(t, []fake.Turn{
		{Calls: []fake.Call{{Name: "ls", Args: `{}`}}},
		{Text: "ok"},
	})
	_, err := core.Run(context.Background(), r.cfg)
	assert.NoErr(t, err)
	a, b := r.prov.Requests[0], r.prov.Requests[1]
	assert.Eq(t, a.System, b.System)
	ta, _ := json.Marshal(a.Tools)
	tb, _ := json.Marshal(b.Tools)
	assert.Eq(t, string(ta), string(tb))
	if len(a.Tools) == 0 {
		t.Fatal("no tools offered")
	}
}

func TestBuildSystem(t *testing.T) {
	assert.Eq(t, core.BuildSystem("base", ""), "base")
	assert.Contains(t, core.BuildSystem("base", "ctx"), "ctx")
	assert.Contains(t, core.BasePrompt, "interrupted")
}
