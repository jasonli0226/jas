package core_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/providers"
	"github.com/jasonli0226/jas/providers/fake"
)

var lsCall = fake.Call{Name: "ls", Args: `{"path":"."}`}

// lastText is the text of the last message's last text block.
func lastText(msgs []providers.Message) string {
	if len(msgs) == 0 {
		return ""
	}
	content := msgs[len(msgs)-1].Content
	for i := len(content) - 1; i >= 0; i-- {
		if content[i].Type == providers.BlockText {
			return content[i].Text
		}
	}
	return ""
}

// With WrapUp, the last allowed turn asks for the answer with tools off, and
// an answer there ends the run ok (#195).
func TestRun_WrapUpAtTurnCap(t *testing.T) {
	r := newRig(t, []fake.Turn{{Calls: []fake.Call{lsCall}}, {Text: "the answer"}})
	r.cfg.MaxTurns, r.cfg.WrapUp = 2, true
	sum, err := core.Run(context.Background(), r.cfg)
	assert.NoErr(t, err)
	assert.Eq(t, sum.Final, "the answer")
	assert.Eq(t, r.prov.Requests[0].ToolChoice, "")
	last := r.prov.Requests[1]
	assert.Eq(t, last.ToolChoice, "none")
	assert.Contains(t, lastText(last.Messages), "turn limit")
	evs := r.events(t)
	wraps := eventsOf(evs, "wrap_up")
	assert.Eq(t, len(wraps), 1)
	assert.Eq(t, wraps[0]["reason"], "turn_cap")
	turns := eventsOf(evs, "turn_ended")
	assert.Eq(t, turns[0]["status"], "ok")
	assert.Eq(t, turns[0]["reason"], "wrapped up at the turn cap")
}

// Without WrapUp the last turn is an ordinary request.
func TestRun_NoWrapUpByDefault(t *testing.T) {
	r := newRig(t, []fake.Turn{{Calls: []fake.Call{lsCall}}, {Calls: []fake.Call{lsCall}}})
	r.cfg.MaxTurns = 2
	_, err := core.Run(context.Background(), r.cfg)
	assert.Eq(t, exitCode(t, err), core.ExitTurnCap)
	assert.Eq(t, r.prov.Requests[1].ToolChoice, "")
	assert.NotContains(t, lastText(r.prov.Requests[1].Messages), "turn limit")
	assert.Eq(t, len(eventsOf(r.events(t), "wrap_up")), 0)
}

// A tool call in the wrap-up reply is not run; the run ends at the cap as it
// would have, and the history stays consistent for resume.
func TestRun_WrapUpToolCallNotRun(t *testing.T) {
	r := newRig(t, []fake.Turn{{Calls: []fake.Call{lsCall}}, {Calls: []fake.Call{lsCall}}})
	r.cfg.MaxTurns, r.cfg.WrapUp = 2, true
	_, err := core.Run(context.Background(), r.cfg)
	assert.Eq(t, exitCode(t, err), core.ExitTurnCap)
	evs := r.events(t)
	assert.Eq(t, len(eventsOf(evs, "tool_started")), 1)
	results := eventsOf(evs, "tool_result")
	assert.Eq(t, len(results), 2)
	assert.Eq(t, results[1]["status"], "not_run")
}

// When the next request would cross the token cap, WrapUp retries it once as
// the wrap-up, with the output cap shrunk to the room left.
func TestRun_WrapUpAtTokenCap(t *testing.T) {
	r := newRig(t, []fake.Turn{{Text: "short answer"}})
	r.cfg.WrapUp, r.cfg.MaxOutput = true, 50000
	r.cfg.Tokens = core.NewTokenPool("run", 20000, nil)
	sum, err := core.Run(context.Background(), r.cfg)
	assert.NoErr(t, err)
	assert.Eq(t, sum.Final, "short answer")
	req := r.prov.Requests[0]
	assert.Eq(t, req.ToolChoice, "none")
	if req.MaxTokens < 1000 || req.MaxTokens >= 20000 {
		t.Fatalf("wrap-up max_tokens %d, want the room left under the cap", req.MaxTokens)
	}
	assert.Contains(t, lastText(req.Messages), "token budget")
	wraps := eventsOf(r.events(t), "wrap_up")
	assert.Eq(t, len(wraps), 1)
	assert.Eq(t, wraps[0]["reason"], "tokens")
	assert.Eq(t, wraps[0]["max_tokens"].(float64), float64(req.MaxTokens))
}

// With too little room left for an answer, the run stops at the cap as before.
func TestRun_WrapUpNeedsRoom(t *testing.T) {
	r := newRig(t, []fake.Turn{{Text: "never sent"}})
	r.cfg.WrapUp, r.cfg.MaxOutput = true, 50000
	r.cfg.Tokens = core.NewTokenPool("run", 300, nil)
	_, err := core.Run(context.Background(), r.cfg)
	assert.Eq(t, exitCode(t, err), core.ExitBudget)
	assert.Eq(t, len(r.prov.Requests), 0)
	assert.NotContains(t, strings.Join(types(r.events(t)), ","), "wrap_up")
}

// When the room left after the next request would drop below the output
// cap, WrapUp makes that request the wrap-up, so a reasoning model still has
// a full output cap for the answer (#219).
func TestRun_WrapUpBeforeTokenCap(t *testing.T) {
	r := newRig(t, []fake.Turn{{Text: "the answer"}})
	r.cfg.WrapUp, r.cfg.MaxOutput = true, 20000
	r.cfg.Tokens = core.NewTokenPool("run", 100000, nil)
	spent, err := r.cfg.Tokens.Reserve(65000)
	assert.NoErr(t, err)
	spent.Settle(65000)
	sum, err := core.Run(context.Background(), r.cfg)
	assert.NoErr(t, err)
	assert.Eq(t, sum.Final, "the answer")
	req := r.prov.Requests[0]
	assert.Eq(t, req.ToolChoice, "none")
	assert.Eq(t, req.MaxTokens, 20000)
	assert.Contains(t, lastText(req.Messages), "token budget")
	wraps := eventsOf(r.events(t), "wrap_up")
	assert.Eq(t, len(wraps), 1)
	assert.Eq(t, wraps[0]["reason"], "tokens")
}

// With more than the reserve left after the request, it stays ordinary.
func TestRun_NoEarlyWrapUpWithRoom(t *testing.T) {
	r := newRig(t, []fake.Turn{{Text: "the answer"}})
	r.cfg.WrapUp, r.cfg.MaxOutput = true, 20000
	r.cfg.Tokens = core.NewTokenPool("run", 100000, nil)
	_, err := core.Run(context.Background(), r.cfg)
	assert.NoErr(t, err)
	assert.Eq(t, r.prov.Requests[0].ToolChoice, "")
	assert.Eq(t, len(eventsOf(r.events(t), "wrap_up")), 0)
}

// A token wrap-up reply cut off at its output cap ends the run at the token
// cap (exit 7, reason tokens), not at max_output_tokens (#219).
func TestRun_TokenWrapUpCutOff(t *testing.T) {
	r := newRig(t, []fake.Turn{{Text: "half an ans", Finish: "length"}})
	r.cfg.WrapUp, r.cfg.MaxOutput = true, 50000
	r.cfg.Tokens = core.NewTokenPool("run", 20000, nil)
	sum, err := core.Run(context.Background(), r.cfg)
	assert.Eq(t, exitCode(t, err), core.ExitBudget)
	assert.Contains(t, err.Error(), "tokens")
	assert.Eq(t, sum.Final, "half an ans")
	evs := r.events(t)
	stops := eventsOf(evs, "budget_stop")
	assert.Eq(t, len(stops), 1)
	assert.Eq(t, stops[0]["reason"], "tokens")
	assert.Eq(t, stops[0]["pool"], "run")
}

// The early wrap-up measures the tightest pool in the chain, so a farm's
// parent pool can trigger it while the run's own pool has room.
func TestRun_WrapUpBeforeParentTokenCap(t *testing.T) {
	r := newRig(t, []fake.Turn{{Text: "the answer"}})
	r.cfg.WrapUp, r.cfg.MaxOutput = true, 20000
	farm := core.NewTokenPool("farm", 100000, nil)
	spent, err := farm.Reserve(65000)
	assert.NoErr(t, err)
	spent.Settle(65000)
	r.cfg.Tokens = core.NewTokenPool("agent", 0, farm)
	_, err = core.Run(context.Background(), r.cfg)
	assert.NoErr(t, err)
	assert.Eq(t, r.prov.Requests[0].ToolChoice, "none")
	assert.Eq(t, len(eventsOf(r.events(t), "wrap_up")), 1)
}
