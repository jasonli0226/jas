package core_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/providers"
)

func textMsg(role providers.Role, n int) providers.Message {
	return providers.Message{Role: role, Content: []providers.ContentBlock{{Type: providers.BlockText, Text: strings.Repeat("x", n)}}}
}

func TestBudget_EstimateBeforeUsage(t *testing.T) {
	b := &core.Budget{Window: 100000}
	assert.Eq(t, b.Count([]providers.Message{textMsg(providers.RoleUser, 4000)}), 1000)
}

func TestBudget_UsagePlusEstimate(t *testing.T) {
	b := &core.Budget{Window: 100000}
	msgs := []providers.Message{textMsg(providers.RoleUser, 9999), textMsg(providers.RoleAssistant, 9999), textMsg(providers.RoleUser, 9999),
		textMsg(providers.RoleAssistant, 400), textMsg(providers.RoleUser, 400)}
	b.Observe(5000, 3)
	assert.Eq(t, b.Count(msgs), 5200)
	b.Observe(0, 5) // no usage: keep last observation
	assert.Eq(t, b.Count(msgs), 5200)
	b.Window = 5100
	assert.Eq(t, b.WouldExceed(msgs), true)
	b.Window = 5200
	assert.Eq(t, b.WouldExceed(msgs), false)
}

// toolTurns builds n assistant tool_use / user tool_result pairs after a prompt.
func toolTurns(n, size int) []providers.Message {
	msgs := []providers.Message{textMsg(providers.RoleUser, 10)}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("c%d", i)
		in, _ := json.Marshal(map[string]string{"path": fmt.Sprintf("f%d.go", i)})
		msgs = append(msgs,
			providers.Message{Role: providers.RoleAssistant, Content: []providers.ContentBlock{{Type: providers.BlockToolUse, ToolUseID: id, Name: "read", Input: in}}},
			providers.Message{Role: providers.RoleUser, Content: []providers.ContentBlock{{Type: providers.BlockToolResult, ToolUseID: id, Text: strings.Repeat("line\n", size)}}},
		)
	}
	return msgs
}

func results(msgs []providers.Message) []string {
	var out []string
	for _, m := range msgs {
		for _, b := range m.Content {
			if b.Type == providers.BlockToolResult {
				out = append(out, b.Text)
			}
		}
	}
	return out
}

func TestBudget_SkillStubAsksForReread(t *testing.T) {
	b := &core.Budget{Window: 1000, ElideAt: 0.6, Keep: 6}
	msgs := toolTurns(10, 100)
	msgs[1].Content[0].Input = json.RawMessage(`{"path":"/s/review/SKILL.md"}`)
	got, st := b.MaybeElide(msgs)
	if st == nil {
		t.Fatal("expected elision")
	}
	assert.Eq(t, results(got)[0], "[elided: read /s/review/SKILL.md 100 lines; skill instructions: read the file again before following the skill]")
	assert.Eq(t, results(got)[1], "[elided: read f1.go 100 lines]")
}

func TestBudget_NoElideBelowThreshold(t *testing.T) {
	b := &core.Budget{Window: 1000000, ElideAt: 0.6, Keep: 6}
	msgs := toolTurns(10, 10)
	got, st := b.MaybeElide(msgs)
	if st != nil {
		t.Fatalf("stats = %+v", st)
	}
	if &got[0] != &msgs[0] {
		t.Fatal("expected same backing slice")
	}
}

func TestBudget_ElidesInOneChunkKeepsLastK(t *testing.T) {
	b := &core.Budget{Window: 1000, ElideAt: 0.6, Keep: 6}
	msgs := toolTurns(10, 100)
	orig := results(msgs)
	got, st := b.MaybeElide(msgs)
	if st == nil {
		t.Fatal("expected elision")
	}
	assert.Eq(t, st.Count, 4)
	if st.TokensAfter >= st.TokensBefore {
		t.Fatalf("tokens not reduced: %+v", st)
	}
	for i, r := range results(got) {
		if i < 4 {
			assert.Eq(t, r, fmt.Sprintf("[elided: read f%d.go 100 lines]", i))
		} else {
			assert.Eq(t, r, orig[i])
		}
	}
	for i, r := range results(msgs) { // input untouched
		assert.Eq(t, r, orig[i])
	}
	assert.Eq(t, got[0].Content[0].Text, msgs[0].Content[0].Text)
	assert.Eq(t, string(got[1].Content[0].Input), string(msgs[1].Content[0].Input))
}

func TestBudget_PrefixStableBetweenPasses(t *testing.T) {
	b := &core.Budget{Window: 1000, ElideAt: 0.6, Keep: 6}
	msgs, st := b.MaybeElide(toolTurns(10, 100))
	if st == nil {
		t.Fatal("expected elision")
	}
	b.Observe(100, len(msgs)) // now far below threshold
	next := append(append([]providers.Message{}, msgs...), textMsg(providers.RoleAssistant, 20))
	got, st2 := b.MaybeElide(next)
	if st2 != nil {
		t.Fatalf("unexpected second pass: %+v", st2)
	}
	prev, _ := json.Marshal(msgs)
	cur, _ := json.Marshal(got[:len(msgs)])
	assert.Eq(t, string(cur), string(prev))
}

func TestBudget_AlreadyElidedNotRecounted(t *testing.T) {
	b := &core.Budget{Window: 1000, ElideAt: 0.6, Keep: 6}
	msgs, _ := b.MaybeElide(toolTurns(10, 100))
	_, st := b.MaybeElide(msgs) // nothing new to elide
	if st != nil {
		t.Fatalf("stats = %+v", st)
	}
}

func TestBudget_RebaseForgetsObservation(t *testing.T) {
	b := &core.Budget{Window: 100000}
	msgs := []providers.Message{textMsg(providers.RoleUser, 4000), textMsg(providers.RoleAssistant, 400)}
	b.Observe(5000, 1)
	assert.Eq(t, b.Count(msgs), 5100)
	b.Rebase()
	assert.Eq(t, b.Count(msgs), 1100)
}
