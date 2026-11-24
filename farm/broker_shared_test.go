package farm

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
)

func TestBroker_SharedSkillsSummary(t *testing.T) {
	const suffix = " (in profile p's skills, shared with every profile that extends it)"
	h := &countHost{a: AnswerFarm}
	b := NewBroker(BrokerOptions{Level: core.ShellOnly, Host: h.ask})
	g := mustGate(t, b, "p1", &recLog{})
	args := json.RawMessage(`{"path":"skills/x/SKILL.md"}`)
	write := brokerTool{name: "write", kind: core.Mutating}
	_, _ = g.Allow(context.Background(), core.AskAlwaysShared(write, "p"), args, nil)
	_, _ = g.Allow(context.Background(), core.AskAlways(write), args, nil)
	assert.Eq(t, h.count(), 2)
	assert.Eq(t, h.reqs[0].Summary, "skills/x/SKILL.md"+suffix)
	assert.Eq(t, h.reqs[1].Summary, "skills/x/SKILL.md")
}
