package tui

import (
	"regexp"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/jasonli0226/jas/internal/assert"
)

var sgrRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

func feedAll(m *mdStream, deltas ...string) []string {
	var out []string
	for _, d := range deltas {
		out = append(out, m.Feed(d)...)
	}
	return out
}

func TestMarkdown_SplitsBlocksAcrossDeltas(t *testing.T) {
	var tr Transcript
	var got []string
	for _, d := range []string{"para one\n", "\npara", " two"} {
		got = append(got, tr.Text(d, t0)...)
	}
	assert.Eq(t, len(got), 1)
	assert.Eq(t, ansi.Strip(got[0]), "para one")
	assert.Contains(t, ansi.Strip(tr.Live()), "para two")
}

func TestMarkdown_FenceHoldsBlankLines(t *testing.T) {
	var m mdStream
	assert.Eq(t, len(feedAll(&m, "```go\na\n\nb\n")), 0)
	got := m.Feed("```\n")
	assert.Eq(t, len(got), 1)
	out := ansi.Strip(renderBlock(got[0]))
	assert.Contains(t, out, "a")
	assert.Contains(t, out, "b")
	assert.Contains(t, out, "╭")
}

func TestMarkdown_BoldAndLists(t *testing.T) {
	out := renderBlock("- **x** y")
	assert.NotContains(t, out, "**")
	plain := ansi.Strip(out)
	assert.Eq(t, plain, "• x y")
	assert.Eq(t, ansi.Strip(renderBlock("1. one\ncont")), "1. one\n  cont")
	assert.Eq(t, ansi.Strip(renderBlock("## Title")), "Title")
}

func TestMarkdown_HighlightUsesDistinctColours(t *testing.T) {
	out := highlight(`x := "s" + 42 // c`)
	assert.Eq(t, ansi.Strip(out), `x := "s" + 42 // c`)
	seen := map[string]bool{}
	for _, m := range sgrRe.FindAllString(out, -1) {
		if m != "\x1b[m" && m != "\x1b[0m" {
			seen[m] = true
		}
	}
	assert.Eq(t, len(seen), 3)
}

func TestMarkdown_StripsControlChars(t *testing.T) {
	var tr Transcript
	got := tr.Text("a\x1b[2Jb\n\n", t0)
	assert.Eq(t, got[0], "a[2Jb")
}
