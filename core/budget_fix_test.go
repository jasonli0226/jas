package core_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/providers"
)

func TestBudget_ObservationAdjustedAfterElision(t *testing.T) {
	b := &core.Budget{Window: 1000, ElideAt: 0.6, Keep: 6}
	msgs := toolTurns(10, 100)
	b.Observe(1100, len(msgs))
	assert.Eq(t, b.WouldExceed(msgs), true)
	out, st := b.MaybeElide(msgs)
	if st == nil {
		t.Fatal("expected elision")
	}
	assert.Eq(t, st.TokensBefore, 1100)
	// Everything was observed, so the adjusted observation equals TokensAfter exactly.
	assert.Eq(t, b.Count(out), st.TokensAfter)
	assert.Eq(t, b.WouldExceed(out), false)
}

func addResult(base []providers.Message, id string, size int) []providers.Message {
	in, _ := json.Marshal(map[string]string{"path": id})
	return append(append([]providers.Message{}, base...),
		providers.Message{Role: providers.RoleAssistant, Content: []providers.ContentBlock{{Type: providers.BlockToolUse, ToolUseID: id, Name: "read", Input: in}}},
		providers.Message{Role: providers.RoleUser, Content: []providers.ContentBlock{{Type: providers.BlockToolResult, ToolUseID: id, Text: strings.Repeat("y", size)}}})
}

func TestBudget_HysteresisBetweenPasses(t *testing.T) {
	b := &core.Budget{Window: 1000, ElideAt: 0.6, Keep: 6}
	msgs := toolTurns(10, 100)
	b.Observe(1100, len(msgs))
	out, st := b.MaybeElide(msgs)
	if st == nil {
		t.Fatal("expected first pass")
	}
	if b.Count(out) < 600 {
		t.Fatalf("setup: count %d should stay above the threshold", b.Count(out))
	}
	small := addResult(out, "s1", 40)
	if _, st2 := b.MaybeElide(small); st2 != nil {
		t.Fatalf("small addition re-elided: %+v", st2)
	}
	big := addResult(small, "s2", 2000) // >= 10% of the window
	if _, st3 := b.MaybeElide(big); st3 == nil {
		t.Fatal("expected elision after >=10% window growth")
	}
}

func TestBudget_StubArgIsOneLine(t *testing.T) {
	b := &core.Budget{Window: 100, ElideAt: 0.1, Keep: 0}
	in, _ := json.Marshal(map[string]string{"command": "echo a\n\tb\x00c\r\n  d " + strings.Repeat("z", 200)})
	msgs := []providers.Message{
		{Role: providers.RoleAssistant, Content: []providers.ContentBlock{{Type: providers.BlockToolUse, ToolUseID: "k", Name: "shell", Input: in}}},
		{Role: providers.RoleUser, Content: []providers.ContentBlock{{Type: providers.BlockToolResult, ToolUseID: "k", Text: strings.Repeat("out\n", 50)}}},
	}
	out, st := b.MaybeElide(msgs)
	if st == nil {
		t.Fatal("expected elision")
	}
	got := out[1].Content[0].Text
	if strings.ContainsAny(got, "\n\r\t\x00") {
		t.Fatalf("stub not one line: %q", got)
	}
	assert.Contains(t, got, "[elided: shell echo a b c d zzz")
	assert.Contains(t, got, " 50 lines]")
	arg := strings.TrimSuffix(strings.TrimPrefix(got, "[elided: shell "), " 50 lines]")
	assert.Eq(t, len([]rune(arg)), 80)
}
