package core

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/providers"
)

func textMsg(role providers.Role, n int) providers.Message {
	return providers.Message{Role: role, Content: []providers.ContentBlock{{Type: providers.BlockText, Text: strings.Repeat("x", n)}}}
}

func useMsg(id, name, input string) providers.Message {
	return providers.Message{Role: providers.RoleAssistant, Content: []providers.ContentBlock{
		{Type: providers.BlockToolUse, ToolUseID: id, Name: name, Input: json.RawMessage(input)},
	}}
}

func resultMsg(id, text string) providers.Message {
	return providers.Message{Role: providers.RoleUser, Content: []providers.ContentBlock{
		{Type: providers.BlockToolResult, ToolUseID: id, Text: text},
	}}
}

func TestCompactThreshold(t *testing.T) {
	tests := []struct{ window, reserve, want int }{
		{200000, 16384, 183616},
		{20000, 16384, 10000},
	}
	for _, tc := range tests {
		assert.Eq(t, compactThreshold(tc.window, tc.reserve), tc.want)
	}
}

func TestCompactKeep(t *testing.T) {
	tests := []struct{ window, reserve, keep, overhead, want int }{
		{200000, 16384, 20000, 0, 20000},
		{32768, 16384, 20000, 0, 8192},
		{2000, 16384, 1200, 0, 500},
		{2000, 16384, 1200, 400, 300}, // overhead shares the room under the trigger
		{2000, 16384, 1200, 5000, 0},
	}
	for _, tc := range tests {
		assert.Eq(t, compactKeep(tc.window, tc.reserve, tc.keep, tc.overhead), tc.want)
	}
}

func TestCutIndex_KeepsTailAndSkipsToolResults(t *testing.T) {
	// 40 bytes = 10 tokens per message; the tail reaches 40 tokens at index 2.
	msgs := []providers.Message{
		textMsg(providers.RoleUser, 40),
		useMsg("c1", "", `{"path":"`+strings.Repeat("y", 29)+`"}`),
		resultMsg("c1", strings.Repeat("x", 40)),
		textMsg(providers.RoleAssistant, 40),
		textMsg(providers.RoleUser, 40),
		textMsg(providers.RoleAssistant, 40),
	}
	assert.Eq(t, msgBytes(msgs[1]), 40)
	assert.Eq(t, cutIndex(msgs, 40), 1)
	assert.Eq(t, cutIndex(msgs, 30), 3) // a3 is a valid cut
}

func TestCutIndex_NoValidCut(t *testing.T) {
	assert.Eq(t, cutIndex([]providers.Message{textMsg(providers.RoleUser, 4000)}, 100), 0)
	assert.Eq(t, cutIndex([]providers.Message{textMsg(providers.RoleUser, 40)}, 1000), 0)
	msgs := []providers.Message{summaryMessage("S"), textMsg(providers.RoleUser, 40)}
	assert.Eq(t, cutIndex(msgs, 1000), 0)
	assert.Eq(t, cutIndex(msgs, 5), 0) // would cut at 1, right after the summary
}

func TestIsSummary(t *testing.T) {
	s, ok := IsSummary(summaryMessage("hello"))
	assert.Eq(t, ok, true)
	assert.Eq(t, s, "hello")
	_, ok = IsSummary(textMsg(providers.RoleUser, 3))
	assert.Eq(t, ok, false)
}

func TestSummaryRequest_Shape(t *testing.T) {
	long := strings.Repeat("é", summaryToolClip+5)
	msgs := []providers.Message{
		textMsg(providers.RoleUser, 3),
		useMsg("c1", "read", `{ "path": "a.go" }`),
		resultMsg("c1", long),
	}
	req := summaryRequest("m", msgs, "", "the db")
	assert.Eq(t, req.Model, "m")
	assert.Eq(t, len(req.Tools), 0)
	assert.Eq(t, req.System, "You summarize a coding-agent conversation so it can continue in a fresh context. Do not call tools. Reply with the summary only.")
	assert.Eq(t, len(req.Messages), 1)
	text := req.Messages[0].Content[0].Text
	assert.Contains(t, text, "<conversation>\n[user]: xxx\n[tool call] read({\"path\":\"a.go\"})\n[tool result]: "+strings.Repeat("é", summaryToolClip)+"…[clipped]\n</conversation>\n\n")
	assert.Contains(t, text, "## Goal\n## Constraints & Preferences")
	assert.Contains(t, text, "\nPay special attention to: the db")
	assert.Eq(t, strings.Contains(text, "previous-summary"), false)
}

func TestSummaryRequest_NoClipWhenShort(t *testing.T) {
	req := summaryRequest("m", []providers.Message{resultMsg("c", "ok")}, "", "")
	text := req.Messages[0].Content[0].Text
	assert.Contains(t, text, "[tool result]: ok\n")
	assert.Eq(t, strings.Contains(text, "clipped"), false)
	assert.Eq(t, strings.Contains(text, "Pay special"), false)
}

func TestSummaryRequest_IteratesOnPrevious(t *testing.T) {
	req := summaryRequest("m", []providers.Message{textMsg(providers.RoleUser, 3)}, "OLD", "")
	assert.Contains(t, req.Messages[0].Content[0].Text, "<previous-summary>\nOLD\n</previous-summary>")
}

func TestFileLists_MergesAndPrefersModified(t *testing.T) {
	prev := withFileLists("S", []string{"a.go"}, []string{"z.go"})
	msgs := []providers.Message{
		useMsg("1", "read", `{"path":"b.go"}`),
		useMsg("2", "edit", `{"path":"a.go","old":"x"}`),
		useMsg("3", "grep", `{"pattern":"p"}`),
		useMsg("4", "ls", `{"path":"b.go"}`),
		useMsg("5", "write", `{"path":"n.go"}`),
		useMsg("6", "bash", `{"path":"ignored"}`),
	}
	read, modified := fileLists(prev, msgs)
	assert.Eq(t, strings.Join(read, ","), "b.go")
	assert.Eq(t, strings.Join(modified, ","), "a.go,n.go,z.go")
}

func TestWithFileLists_ReplacesOldBlocks(t *testing.T) {
	once := withFileLists("sum", []string{"r.go"}, []string{"m.go"})
	twice := withFileLists(once, []string{"r.go"}, []string{"m.go"})
	assert.Eq(t, twice, "sum\n\n<read-files>\nr.go\n</read-files>\n<modified-files>\nm.go\n</modified-files>")
	assert.Eq(t, withFileLists(once, nil, nil), "sum")
}

func TestCutIndex_WalkBackEdges(t *testing.T) {
	// Every message is a tool-results message: the walk-back reaches 0.
	all := []providers.Message{resultMsg("a", "x"), resultMsg("b", "x"), resultMsg("c", "x")}
	assert.Eq(t, cutIndex(all, 1), 0)

	// The candidate lands on a tool-results message and walks back to index 0.
	head := []providers.Message{
		resultMsg("a", strings.Repeat("x", 40)),
		textMsg(providers.RoleAssistant, 40),
	}
	assert.Eq(t, cutIndex(head, 15), 0)

	// After a summary the walk-back stops at the floor and finds nothing.
	msgs := []providers.Message{
		summaryMessage("S"),
		useMsg("c1", "", `{}`),
		resultMsg("c1", strings.Repeat("x", 40)),
		textMsg(providers.RoleAssistant, 40),
	}
	assert.Eq(t, cutIndex(msgs, 15), 0) // candidate 2 is tool-results, walks back to 1
}
