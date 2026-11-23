package tui

import (
	"strings"
	"testing"

	"github.com/jasonli0226/jas/farm"
	"github.com/jasonli0226/jas/internal/assert"
)

func TestFarmState_ReviewerRow(t *testing.T) {
	c, f := farmWithAgents(t, Options{})
	f.event(farm.EvReviewStarted, map[string]any{"round": 1})
	c.drain()
	r := c.m.farm.agent("review")
	if r == nil || r.role != "reviewer" || r.status != agentRunning {
		t.Fatalf("reviewer row %+v", r)
	}
	n := len(c.m.farm.agents)
	f.event(farm.EvReviewEnded, map[string]any{"round": 1, "status": "done"})
	f.event(farm.EvReviewStarted, map[string]any{"round": 2})
	c.drain()
	if len(c.m.farm.agents) != n {
		t.Fatalf("second round added a row: %d != %d", len(c.m.farm.agents), n)
	}
	assert.Eq(t, c.m.farm.agent("review").status, agentRunning) // round 2 reopens it
	f.event(farm.EvReviewEnded, map[string]any{"round": 2, "status": "unfinished", "detail": "budget"})
	c.drain()
	assert.Eq(t, c.m.farm.agent("review").status, agentFailed)
	assert.Contains(t, strings.Join(c.m.Printed(), "\n"), "review: round 2 unfinished (budget)")
}
