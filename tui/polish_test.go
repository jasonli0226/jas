package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/providers/fake"
)

func TestFormatTokens_RoundsUpToMillions(t *testing.T) {
	assert.Eq(t, formatTokens(999_949), "999.9k")
	assert.Eq(t, formatTokens(999_950), "1.0M")
	assert.Eq(t, formatTokens(999_999), "1.0M")
}

func TestOutcome_SingularCounts(t *testing.T) {
	assert.Eq(t, outcome("ls", "ok", "a\n"), "(1 entry)")
	assert.Eq(t, outcome("read", "ok", "a\n"), "(1 line)")
	assert.Eq(t, outcome("grep", "ok", "a\n"), "(1 match)")
	assert.Eq(t, outcome("ls", "ok", "a\nb\n"), "(2 entries)")
}

func TestMdStream_FenceWithInfoDoesNotCloseFence(t *testing.T) {
	var m mdStream
	got := feedAll(&m, "```md\n", "```go\n", "x\n", "```\n")
	assert.Eq(t, len(got), 1)
	assert.Eq(t, got[0], "```md\n```go\nx\n```")
}

func TestCleanText_KeepsZeroWidthJoiner(t *testing.T) {
	family := "👨‍👩‍👧"
	assert.Eq(t, cleanText(family), family)
	assert.Eq(t, cleanText("a‮b"), "ab") // bidi override still goes
}

func TestToolRow_KeepsCommandSpacing(t *testing.T) {
	row := subject("shell", map[string]any{"command": "echo  a\tb\nc"})
	assert.Eq(t, row, "echo  a b c")
	assert.Eq(t, subject("grep", map[string]any{"pattern": "a  b"}), "a  b")
}

func TestTranscript_ReasoningLinesHaveNoTrailingPadding(t *testing.T) {
	tr := Transcript{ShowThinking: true}
	out := tr.Reasoning("a much longer line\nab\n\n", t0)
	tr.Reasoning("pending long line\nxy", t0)
	for _, block := range append(out, tr.Live()) {
		for _, l := range strings.Split(ansi.Strip(block), "\n") {
			if strings.HasSuffix(l, " ") {
				t.Fatalf("trailing padding in %q", l)
			}
		}
	}
}

func TestChat_CommandAtApprovalPromptKeepsItOpen(t *testing.T) {
	b := newTestBackend(t, shellTurn("go test ./..."), fake.Turn{Text: "ok"})
	c := newTestChatOpts(t, b, Options{Level: core.ShellOnly})
	c.submit("test it")
	c.pumpUntilApproval()
	c.submit("/model")
	assert.Contains(t, c.view(), "approve shell: go test ./...? [y/N/always]")
	assert.Contains(t, c.m.hint, "answer the approval first")
	assert.Eq(t, len(c.m.queued), 0) // not sent as steering
	c.submit("y")
	assert.NotContains(t, c.view(), "approve shell")
}

func TestClassifyReply(t *testing.T) {
	cases := map[string]reply{
		"y": replyYes, " YES ": replyYes, "n": replyNo, "": replyNo, "no": replyNo,
		"always": replyAlways, "a": replyAlways, " A ": replyAlways, "farm": replyFarm, "f": replyFree, "no, use make": replyFree, "yep": replyFree,
	}
	for text, want := range cases {
		assert.Eq(t, classifyReply(text), want)
	}
	assert.Eq(t, replySteers("farm"), true) // only a farm part's prompt offers farm
	assert.Eq(t, replySteers("n"), false)
}

func TestHelp_SaysCtrlCClosesPicker(t *testing.T) {
	assert.Contains(t, helpText, "Esc or Ctrl+C cancel")
}

func TestGate_PromptIsRedactedThenSanitized(t *testing.T) {
	msgs := make(chan tea.Msg, 16)
	redact := func(s string) string { return strings.ReplaceAll(s, "sk-secret", "[REDACTED]") }
	g := NewGate(core.ShellOnly, false, redact, func(m tea.Msg) { msgs <- m })
	res := allowAsync(context.Background(), g, toolNamed(t, "shell"), shellArgs("curl -H 'k: sk-secret'  x\x1b[2J"))
	a := nextApproval(t, msgs)
	assert.NotContains(t, a.prompt, "sk-secret")
	assert.Contains(t, a.prompt, "curl -H 'k: [REDACTED]'  x\\x1b[2J")
	a.reply <- "n"
	<-res
}
