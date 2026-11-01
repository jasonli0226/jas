package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/jasonli0226/jas/core"
)

const (
	diffNumWidth = 6 // width of the line-number column
	diffTextAt   = diffNumWidth + 3
	diffSep      = "  ⋮"
)

// renderDiff renders d in style B, at most maxLines diff rows (0 = no cap),
// each row cut to width (with "…") before it is styled. A line's Text is
// already redacted and escaped, so it is not sanitized again.
func renderDiff(d *core.Diff, width, maxLines int) []string {
	if d == nil {
		return nil
	}
	var out []string
	shown, total := 0, 0
	for _, h := range d.Hunks {
		total += len(h)
	}
	for i, h := range d.Hunks {
		for j, l := range h {
			if maxLines > 0 && shown >= maxLines {
				break
			}
			if i > 0 && j == 0 {
				out = append(out, dimStyle.Render(cutRow(diffSep, width)))
			}
			out = append(out, diffRow(l, width))
			shown++
		}
	}
	if shown < total {
		out = append(out, dimStyle.Render(cutRow(fmt.Sprintf("… %d more lines", total-shown), width)))
	}
	if d.Truncated && len(out) > 0 {
		out = append(out, dimStyle.Render(cutRow("… diff truncated", width)))
	}
	return out
}

func cutRow(s string, width int) string {
	if width > 0 {
		return ansi.Truncate(s, width, "…")
	}
	return s
}

// diffRow renders one line as "%6d k text": the number faint, a removed or
// added line on its band (padded to width in cells), changed words on the brighter one.
func diffRow(l core.DiffLine, width int) string {
	num := l.New
	if l.Kind == "-" {
		num = l.Old
	}
	row := []rune(cutRow(fmt.Sprintf("%*d %s %s", diffNumWidth, num, l.Kind, l.Text), width))
	var line, word lipgloss.Style
	switch l.Kind {
	case "-":
		line = lipgloss.NewStyle().Background(diffDelBg)
		word = lipgloss.NewStyle().Background(diffDelWordBg)
	case "+":
		line = lipgloss.NewStyle().Background(diffAddBg)
		word = lipgloss.NewStyle().Background(diffAddWordBg)
	}
	var b strings.Builder
	for _, seg := range diffSegments(row, l.Spans) {
		st := line
		switch seg.class {
		case segNum:
			st = line.Faint(true)
		case segWord:
			st = word
		}
		b.WriteString(st.Render(string(seg.text)))
	}
	if pad := width - ansi.StringWidth(string(row)); l.Kind != " " && pad > 0 {
		b.WriteString(line.Render(strings.Repeat(" ", pad)))
	}
	return b.String()
}

type segClass int

const (
	segNum segClass = iota
	segText
	segWord
)

type diffSegment struct {
	class segClass
	text  []rune
}

// diffSegments splits a row into runs of the number column, plain text and
// changed words. Span offsets are rune offsets into the line's Text.
func diffSegments(row []rune, spans [][2]int) []diffSegment {
	classOf := func(i int) segClass {
		if i < diffNumWidth {
			return segNum
		}
		for _, s := range spans {
			if i >= diffTextAt+s[0] && i < diffTextAt+s[1] {
				return segWord
			}
		}
		return segText
	}
	var segs []diffSegment
	for i, r := range row {
		c := classOf(i)
		if n := len(segs); n > 0 && segs[n-1].class == c {
			segs[n-1].text = append(segs[n-1].text, r)
			continue
		}
		segs = append(segs, diffSegment{c, []rune{r}})
	}
	return segs
}

// diffFromEvent decodes data["diff"] (a map after the redacting UI's JSON
// round trip) into a *core.Diff; nil when absent or malformed.
func diffFromEvent(data map[string]any) *core.Diff {
	return core.DiffFromPayload(data["diff"])
}
