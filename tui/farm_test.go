package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/jasonli0226/jas/farm"
	"github.com/jasonli0226/jas/internal/assert"
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
