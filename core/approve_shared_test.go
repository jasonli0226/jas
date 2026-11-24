package core_test

import (
	"encoding/json"
	"testing"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
)

func TestMustAskShared(t *testing.T) {
	w := toolByName(t, "write")
	shared := core.AskAlwaysShared(w, "p")
	assert.Eq(t, core.MustAskShared(shared), "p")
	assert.Eq(t, core.MustAsk(shared), true)
	assert.Eq(t, core.MustAskShared(core.AskAlways(w)), "")
	assert.Eq(t, core.MustAskShared(w), "")
}

func TestSummarizeTool_SharedSuffix(t *testing.T) {
	w := toolByName(t, "write")
	args := json.RawMessage(`{"path":"a/SKILL.md"}`)
	assert.Eq(t, core.SummarizeTool(core.AskAlways(w), args), "a/SKILL.md")
	assert.Eq(t, core.SummarizeTool(core.AskAlwaysShared(w, "p"), args),
		"a/SKILL.md (in profile p's skills, shared with every profile that extends it)")
}
