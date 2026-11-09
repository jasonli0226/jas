package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/jasonli0226/jas/internal/assert"
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
