package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/farm"
	"github.com/jasonli0226/jas/internal/assert"
)

// Regression tests for the final branch review's findings.

func TestReview1_EscAnswersFarmApprovalAsDenial(t *testing.T) {
	c, f := farmWithAgents(t, Options{})
	c.typeText("draft")
	got := farmAllow(t, f, "a")
	c.pumpApproval()
	c.key("esc")
	assert.Eq(t, <-got, core.Denied)
	assert.Eq(t, c.m.approval == nil, true)
	assert.Eq(t, c.m.input.Value(), "draft")
	assert.Eq(t, c.m.farm.agent("a").needsApproval, false)
	again := farmAllow(t, f, "b") // the prompt queue is free again
	c.pumpApproval()
	c.submit("y")
	assert.Eq(t, <-again, core.Granted)
}

func TestReview2_ApprovalOverQuestionKeepsDraft(t *testing.T) {
	c, f := farmWithAgents(t, Options{})
	c.typeText("hello")
	ans := farmAsk(f, "retry, drop or abort? ", []string{"retry", "drop", "abort"})
	c.pumpAsk()
	got := farmAllow(t, f, "a")
	c.pumpApproval()
	c.submit("y")
	<-got
	c.submit("drop")
	assert.Eq(t, <-ans, "drop")
	assert.Eq(t, c.m.input.Value(), "hello")
}

func TestReview2_PlanArrivingDuringApprovalIsKept(t *testing.T) {
	c, f := farmWithAgents(t, Options{})
	f.run.finish(farmResult("ok", 0))
	c.drain()
	f.run = newFakeRun()
	got := farmAllow(t, f, "a")
	c.pumpApproval()
	c.update(farmPlannedMsg{plan: f.plan}) // the planner finishes under the open approval
	c.submit("n")
	<-got
	if c.m.picker == nil || c.m.picker.title != "farm plan" {
		t.Fatalf("plan review lost: %+v", c.m.picker)
	}
}

func TestReview2_HandBackDuringApprovalReachesDraft(t *testing.T) {
	c, f := farmWithAgents(t, Options{})
	c.update(modKey('1', tea.ModCtrl))
	c.typeText("half")
	got := farmAllow(t, f, "b")
	c.pumpApproval()
	f.hooks.Undelivered("a", []string{"late"})
	c.drain()
	c.submit("y")
	<-got
	v := c.m.input.Value()
	if !strings.Contains(v, "half") || !strings.Contains(v, "late") {
		t.Fatalf("input %q", v)
	}
}

func TestReview3_ApprovalClosedWhenPartEnds(t *testing.T) {
	c, f := farmWithAgents(t, Options{})
	c.typeText("keep me")
	farmAllow(t, f, "a")
	c.pumpApproval()
	f.event(farm.EvPartEnded, map[string]any{"part": "a", "status": "dropped"})
	c.drain()
	assert.Eq(t, c.m.approval == nil, true)
	assert.Eq(t, c.m.input.Value(), "keep me")
}

func TestReview5_FarmEndClosesQuestion(t *testing.T) {
	c, f := farmWithAgents(t, Options{})
	c.typeText("mine")
	farmAsk(f, "retry, drop or abort? ", []string{"retry", "drop", "abort"})
	c.pumpAsk()
	f.run.finish(farmResult("interrupted", 130))
	c.drain()
	assert.Eq(t, c.m.ask == nil, true)
	assert.Eq(t, len(c.m.askPending), 0)
	assert.Eq(t, c.m.input.Value(), "mine")
}

func TestReview6_DecisionNoteShown(t *testing.T) {
	c, f := farmWithAgents(t, Options{})
	f.event(farm.EvOwnerDecision, map[string]any{"choice": "keep", "note": "main checkout has uncommitted changes; keeping the branches"})
	c.drain()
	assert.Contains(t, strings.Join(c.m.Printed(), "\n"), "main checkout has uncommitted changes")
}

// errRun fails every Steer.
type errRun struct{ *fakeRun }

func (errRun) Steer(string, string) error { return farm.ErrPartNotRunning }

func TestReview7_SteerFailureGivesTextBack(t *testing.T) {
	b := newTestBackend(t)
	f := newFakeFarmer()
	b.farmer = f
	c := newTestChat(t, b)
	f.run = newFakeRun()
	startFarm(t, c)
	c.m.farm.run = errRun{f.run}
	f.event("part_started", map[string]any{"part": "a", "role": "coder"})
	c.drain()
	c.update(modKey('1', tea.ModCtrl))
	mark := len(c.m.Screen())
	c.submit("use slog")
	c.drain()
	assert.Eq(t, c.m.input.Value(), "use slog")
	assert.Eq(t, len(c.m.farm.agent("a").queued), 0)
	assert.Contains(t, c.screenSince(mark), "not running")
}

func TestReview8_RepliesShownInAgentView(t *testing.T) {
	c, f := farmWithAgents(t, Options{})
	f.event(farm.EvPartEnded, map[string]any{"part": "a", "status": "done"})
	c.drain()
	c.update(modKey('1', tea.ModCtrl))
	mark := len(c.m.Screen())
	c.submit("more")
	assert.Contains(t, c.screenSince(mark), "a is done")
	assert.Eq(t, c.m.input.Value(), "more") // a refused steer keeps the text
	c.key("ctrl+c")
	mark = len(c.m.Screen())
	c.submit("/farm")
	assert.Contains(t, c.screenSince(mark), "#  part")
}

func TestReview9_FlipBackAfterNewShowsOnlyNewSession(t *testing.T) {
	c, _ := farmChatTurns(t)
	startFarm(t, c)
	c.update(noticeMsg{"old session line"})
	c.submit("/new")
	c.update(modKey('1', tea.ModCtrl))
	mark := len(c.m.Screen())
	c.update(modKey('0', tea.ModCtrl))
	if strings.Contains(c.screenSince(mark), "old session line") {
		t.Fatal("flip back redrew the previous session")
	}
}

func TestReview10_FarmGrantsEndWithTheFarm(t *testing.T) {
	c, f := farmWithAgents(t, Options{})
	got := farmAllow(t, f, "a")
	c.pumpApproval()
	c.submit("farm")
	assert.Eq(t, <-got, core.Granted)
	f.run.finish(farmResult("ok", 0))
	c.drain()
	f.run = newFakeRun()
	c.submit(`/farm "next"`)
	c.key("enter")
	c.drain()
	again := farmAllow(t, f, "a") // same command in the next farm must ask again
	c.pumpApproval()
	c.submit("n")
	assert.Eq(t, <-again, core.Denied)
}

func TestReview11_EventsBeforeStartReturnsAreKept(t *testing.T) {
	b := newTestBackend(t)
	f := newFakeFarmer()
	f.early = []farmEventMsg{
		{typ: farm.EvFarmStarted, m: map[string]any{"farm_tokens": 5000000}},
		{typ: farm.EvPartStarted, m: map[string]any{"part": "a", "role": "coder", "worktree": "/wt/a"}},
	}
	b.farmer = f
	c := newTestChat(t, b)
	startFarm(t, c)
	assert.Contains(t, c.view(), "/ 5.0M")
	assert.Eq(t, c.m.farm.agent("a").status, agentRunning)
	assert.Eq(t, c.m.farm.agent("a").worktree, "/wt/a")
}

func TestReview12_CtrlZeroPrintsEveryHeldBlock(t *testing.T) {
	c, _ := farmWithAgents(t, Options{})
	c.update(tea.WindowSizeMsg{Width: 80, Height: 12})
	c.update(modKey('1', tea.ModCtrl))
	for i := range 30 {
		c.update(noticeMsg{"held line " + string(rune('A'+i))})
	}
	mark := len(c.m.Screen())
	c.update(modKey('0', tea.ModCtrl))
	out := c.screenSince(mark)
	if !strings.Contains(out, "held line A") || !strings.Contains(out, "held line "+string(rune('A'+29))) {
		t.Fatalf("held output cut on Ctrl+0:\n%s", out)
	}
}

// A part that stops (here on its token budget) says so in its own view, so
// the last tool output does not look like a hang.
func TestPartEndShownInAgentView(t *testing.T) {
	c, f := farmWithAgents(t, Options{})
	c.update(modKey('1', tea.ModCtrl))
	mark := len(c.m.Screen())
	f.event(farm.EvPartEnded, map[string]any{"part": "a", "status": "failed", "reason": "budget", "tokens": 1463913})
	c.drain()
	out := c.screenSince(mark)
	assert.Contains(t, out, "part ended: failed (budget)")
	assert.Contains(t, out, "1.5M tok")
}

func TestPartCheckDeferredMarksRow(t *testing.T) {
	c, f := farmWithAgents(t, Options{})
	f.event(farm.EvPartCheckDeferred, map[string]any{"part": "a", "round": 1, "reason": "needs b"})
	c.drain()
	assert.Eq(t, c.m.farm.agent("a").check, "deferred")
	assert.Contains(t, strings.Join(c.m.Printed(), "\n"), "part_check deferred to the merged check: needs b")
}
