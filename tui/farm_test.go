package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/jasonli0226/jas/farm"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/providers/fake"
)

func farmChat(t *testing.T) (*testChat, *fakeFarmer) {
	t.Helper()
	b := newTestBackend(t)
	f := newFakeFarmer()
	b.farmer = f
	return newTestChat(t, b), f
}

func TestFarm_StartApprovesAndShowsRows(t *testing.T) {
	c, f := farmChat(t)
	startFarm(t, c)
	assert.Eq(t, f.started, "/plans/f1.yaml")
	f.event(farm.EvPartStarted, map[string]any{"part": "a", "role": "coder"})
	c.drain()
	v := c.view()
	for _, want := range []string{"1 a", "● running", "2 b", "· queued", "farm "} {
		if !strings.Contains(v, want) {
			t.Fatalf("view lacks %q:\n%s", want, v)
		}
	}
}

func TestFarm_RefusalPrefillsInput(t *testing.T) {
	c, f := farmChat(t)
	f.plan = FarmPlan{ID: "f1", Task: "fix x", Refusal: "small_change", Suggest: "run it in the chat: fix x"}
	c.submit(`/farm "fix x"`)
	assert.Eq(t, c.m.picker == nil, true)
	assert.Contains(t, strings.Join(c.printedNoBanner(), "\n"), "planner refused: small_change")
	assert.Eq(t, c.m.input.Value(), "fix x")
}

func TestFarm_RowsCollapseWhenShort(t *testing.T) {
	c, _ := farmChat(t)
	startFarm(t, c)
	c.update(tea.WindowSizeMsg{Width: 80, Height: 8})
	v := c.view()
	if strings.Contains(v, "1 a") || !strings.Contains(v, "farm ") {
		t.Fatalf("rows not collapsed on a short terminal:\n%s", v)
	}
}

func TestFarm_DashboardColumns(t *testing.T) {
	c, f := farmChat(t)
	startFarm(t, c)
	f.event(farm.EvPartStarted, map[string]any{"part": "a", "role": "coder"})
	c.drain()
	c.submit("/farm")
	out := strings.Join(c.printedNoBanner(), "\n")
	for _, want := range []string{"#  part", "a", "● running", "· queued"} {
		if !strings.Contains(out, want) {
			t.Fatalf("dashboard lacks %q:\n%s", want, out)
		}
	}
}

func TestFarm_UnavailableExplains(t *testing.T) {
	c, f := farmChat(t)
	f.available, f.why = false, "project not trusted"
	c.submit(`/farm "fix x"`)
	assert.Contains(t, strings.Join(c.printedNoBanner(), "\n"), "project not trusted")
	assert.Eq(t, c.m.farm == nil, true)
}

func farmChatTurns(t *testing.T, turns ...fake.Turn) (*testChat, *fakeFarmer) {
	t.Helper()
	b := newTestBackend(t, turns...)
	f := newFakeFarmer()
	b.farmer = f
	return newTestChat(t, b), f
}

func endFarm(c *testChat, f *fakeFarmer) {
	f.run.mu.Lock()
	f.run.man.Parts = map[string]*farm.PartState{
		"a": {Status: "done", Diffstat: "1 file changed"}, "b": {Status: "failed", FailureReason: "check"}}
	f.run.man.FinalChoice = "keep"
	f.run.mu.Unlock()
	f.run.finish(farm.Result{Code: 6, Reason: "part_failed", Branch: "jas/farm/f1/integrate"})
	c.drain()
}

func TestFarmSummary_Fields(t *testing.T) {
	man := farm.Manifest{FarmID: "f1", FinalChoice: "keep", Parts: map[string]*farm.PartState{
		"a": {Status: "done", Diffstat: "1 file changed"}, "b": {Status: "failed", FailureReason: "check"}}}
	s := farmSummary(man, farm.Result{Code: 6, Reason: "part_failed", Branch: "jas/farm/f1/integrate"}, []string{"a", "b"})
	for _, want := range []string{"farm f1 ended: part_failed (exit 6)", "branch jas/farm/f1/integrate", "final choice keep",
		"a done (1 file changed)", "b failed (check)"} {
		if !strings.Contains(s, want) {
			t.Fatalf("summary lacks %q:\n%s", want, s)
		}
	}
}

func TestFarmEnd_SummaryAttachedToNextMessage(t *testing.T) {
	c, f := farmChatTurns(t, fake.Turn{Text: "reviewed"}, fake.Turn{Text: "again"})
	startFarm(t, c)
	endFarm(c, f)
	assert.Eq(t, c.m.running(), false) // the summary never starts a turn on its own
	assert.Contains(t, strings.Join(c.m.Printed(), "\n"), "farm f1 ended: part_failed")
	c.submit("review it")
	c.pump()
	reqs := c.b.prov.Requests
	if len(reqs) == 0 {
		t.Fatal("no request")
	}
	got := requestText(reqs[0])
	if !strings.Contains(got, "farm f1 ended: part_failed") || !strings.Contains(got, "review it") {
		t.Fatalf("first request %q", got)
	}
	assert.Contains(t, strings.Join(c.m.Printed(), "\n"), "(farm summary attached)")
	c.submit("again")
	c.pump()
	last := c.b.prov.Requests[len(c.b.prov.Requests)-1]
	if newest := last.Messages[len(last.Messages)-1]; strings.Contains(newest.Content[0].Text, "farm f1 ended") {
		t.Fatal("summary attached twice")
	}
}

func TestFarmEnd_SurvivesNewSession(t *testing.T) {
	c, f := farmChatTurns(t, fake.Turn{Text: "ok"})
	startFarm(t, c)
	c.update(modKey('1', tea.ModCtrl))
	old := c.m.live
	c.submit("/new") // the harness runs the open synchronously
	if c.m.live == old {
		t.Fatal("no new session")
	}
	assert.Eq(t, c.m.viewing, "")
	assert.Contains(t, c.view(), "1 a")
	endFarm(c, f)
	c.submit("hi")
	c.pump()
	assert.Contains(t, requestText(c.b.prov.Requests[0]), "farm f1 ended")
}
