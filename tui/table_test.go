package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/jasonli0226/jas/internal/assert"
)

func stripLines(s string) string {
	lines := strings.Split(ansi.Strip(s), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return strings.Join(lines, "\n")
}

func TestTable_AlignsColumns(t *testing.T) {
	out := renderBlock("| piece | what |\n|---|---|\n| `server` | API |\n| client | **UI** board |", 0)
	assert.Eq(t, stripLines(out), strings.Join([]string{
		"piece  │ what",
		"───────┼─────────",
		"server │ API",
		"client │ UI board",
	}, "\n"))
	assert.Contains(t, out, boldStyle.Render("piece"))
	assert.Contains(t, out, codeSpanStyle.Render("server"))
}

func TestTable_TextAroundTable(t *testing.T) {
	out := renderBlock("How it fits\n| a | b |\n| - | - |\n| 1 | 2 |\nafter", 0)
	assert.Eq(t, stripLines(out), "How it fits\na │ b\n──┼──\n1 │ 2\nafter")
}

func TestTable_Alignment(t *testing.T) {
	out := renderBlock("| l | c | r |\n|:--|:-:|--:|\n| x | y | z |\n| long | long | long |", 0)
	assert.Eq(t, stripLines(out), strings.Join([]string{
		"l    │  c   │    r",
		"─────┼──────┼─────",
		"x    │  y   │    z",
		"long │ long │ long",
	}, "\n"))
}

func TestTable_RaggedRowsAndEscapedPipe(t *testing.T) {
	out := renderBlock("| a | b |\n|---|---|\n| x \\| y |\n| 1 | 2 | 3 |", 0)
	assert.Eq(t, stripLines(out), "a     │ b\n──────┼──\nx | y │\n1     │ 2")
}

func TestTable_WrapsCellsToWidth(t *testing.T) {
	out := renderBlock("| k | v |\n|---|---|\n| id | one two three four |", 14)
	got := stripLines(out)
	assert.Eq(t, got, strings.Join([]string{
		"k  │ v",
		"───┼────────",
		"id │ one two",
		"   │ three",
		"   │ four",
	}, "\n"))
	for _, l := range strings.Split(ansi.Strip(out), "\n") {
		if ansi.StringWidth(l) > 14 {
			t.Fatalf("line wider than 14: %q", l)
		}
	}
}

func TestTable_TooNarrowFallsBackToRows(t *testing.T) {
	raw := "| a | b | c |\n|---|---|---|\n| 1 | 2 | 3 |"
	assert.Eq(t, stripLines(renderBlock(raw, 6)), raw)
}

func TestTable_NeedsSeparator(t *testing.T) {
	assert.Eq(t, stripLines(renderBlock("| a | b |\n| 1 | 2 |", 0)), "| a | b |\n| 1 | 2 |")
}

// A table printed through the transcript stays under the marker and is not
// re-wrapped mid-row.
func TestTable_InTranscript(t *testing.T) {
	tr := Transcript{Width: 16}
	got := plain(tr.Text("| a | b |\n|---|---|\n| one two three | x |\n\n", t0))
	assert.Eq(t, len(got), 1)
	assert.Eq(t, got[0], strings.Join([]string{
		"● a       │ b",
		"  ────────┼──",
		"  one two │ x",
		"  three   │",
	}, "\n"))
}
