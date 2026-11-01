package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
)

func middleDiff() *core.Diff {
	return core.ComputeDiff(core.Change{
		Path: "f.txt",
		Old:  "a\nb\nc\nd\ne\nf\ng\nh\n",
		New:  "a\nb\nc\nD\ne\nf\ng\nh\n",
	}, nil)
}

func plainRows(rows []string) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = strings.TrimRight(ansi.Strip(r), " ")
	}
	return out
}

func TestRenderDiff_RowsAndNumbers(t *testing.T) {
	got := plainRows(renderDiff(middleDiff(), 40, 0))
	eqStrings(t, got, []string{
		"     1   a", "     2   b", "     3   c",
		"     4 - d", "     4 + D",
		"     5   e", "     6   f", "     7   g",
	})
}

func TestRenderDiff_StyleB(t *testing.T) {
	rows := renderDiff(middleDiff(), 40, 0)
	assert.Contains(t, rows[3], bgSeq(diffDelBg))
	assert.Contains(t, rows[3], bgSeq(diffDelWordBg))
	assert.Contains(t, rows[4], bgSeq(diffAddBg))
	assert.Contains(t, rows[4], bgSeq(diffAddWordBg))
	assert.NotContains(t, rows[0], "48;")
}

func TestRenderDiff_HunkSeparator(t *testing.T) {
	d := &core.Diff{Hunks: [][]core.DiffLine{
		{{Kind: " ", Old: 1, New: 1, Text: "a"}},
		{{Kind: " ", Old: 9, New: 9, Text: "b"}},
	}}
	eqStrings(t, plainRows(renderDiff(d, 40, 0)), []string{"     1   a", "  ⋮", "     9   b"})
}

func TestRenderDiff_CapsLines(t *testing.T) {
	var hunk []core.DiffLine
	for i := 1; i <= 100; i++ {
		hunk = append(hunk, core.DiffLine{Kind: "+", New: i, Text: fmt.Sprint("l", i)})
	}
	got := plainRows(renderDiff(&core.Diff{Hunks: [][]core.DiffLine{hunk}}, 40, 30))
	assert.Eq(t, len(got), 31)
	assert.Eq(t, got[30], "… 70 more lines")
}

func TestRenderDiff_NarrowWidth(t *testing.T) {
	for _, w := range []int{1, 5, 10} {
		for _, r := range plainRows(renderDiff(middleDiff(), w, 0)) {
			assert.Eq(t, len([]rune(r)) <= w, true)
		}
	}
}

func TestRenderDiff_TruncatedNote(t *testing.T) {
	d := middleDiff()
	d.Truncated = true
	got := plainRows(renderDiff(d, 40, 0))
	assert.Eq(t, got[len(got)-1], "… diff truncated")
}

func TestRenderDiff_NilAndEmpty(t *testing.T) {
	assert.Eq(t, len(renderDiff(nil, 40, 0)), 0)
	assert.Eq(t, len(renderDiff(&core.Diff{}, 40, 0)), 0)
}
