package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
)

func TestGate_SharedSkillsPrompt(t *testing.T) {
	const suffix = "in profile p's skills, shared with every profile that extends it"
	args := json.RawMessage(`{"path":"skills/x/SKILL.md","content":"x"}`)
	write := toolNamed(t, "write")
	g, msgs := newRecordingGate(core.AllMutating)
	res := allowAsync(context.Background(), g, core.AskAlwaysShared(write, "p"), args)
	a := nextApproval(t, msgs)
	assert.Contains(t, a.prompt, suffix)
	assert.Eq(t, strings.Count(a.prompt, suffix), 1)
	a.reply <- "n"
	<-res
	res = allowAsync(context.Background(), g, core.AskAlways(write), args)
	a = nextApproval(t, msgs)
	assert.Eq(t, strings.Contains(a.prompt, "shared with"), false)
	a.reply <- "n"
	<-res
}
