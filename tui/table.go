package tui

import (
	"regexp"
	"slices"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// minTableCol is the narrowest column a table may be squeezed to; below it
// the table prints as its raw rows instead.
const minTableCol = 3

var (
	tableSepRe  = regexp.MustCompile(`^\|?(\s*:?-+:?\s*\|)*\s*:?-+:?\s*\|?$`)
	tableColSep = dimStyle.Render(" │ ")
)

type colAlign int

const (
	alignLeft colAlign = iota
	alignCenter
	alignRight
)

func isTableRow(line string) bool { return strings.HasPrefix(strings.TrimSpace(line), "|") }

// tableAt reports how many lines of lines, from i, form a table: a header
// row, a separator row, then the body rows that follow. 0 means no table.
func tableAt(lines []string, i int) int {
	if i+1 >= len(lines) || !isTableRow(lines[i]) || !tableSepRe.MatchString(strings.TrimSpace(lines[i+1])) {
		return 0
	}
	n := 2
	for i+n < len(lines) && isTableRow(lines[i+n]) {
		n++
	}
	return n
}

// splitCells splits a table row on unescaped pipes, dropping the outer ones.
func splitCells(row string) []string {
	row = strings.TrimSpace(row)
	row = strings.TrimPrefix(row, "|")
	if strings.HasSuffix(row, "|") && !strings.HasSuffix(row, `\|`) {
		row = row[:len(row)-1]
	}
	var cells []string
	var b strings.Builder
	for i := 0; i < len(row); i++ {
		switch {
		case row[i] == '\\' && i+1 < len(row) && row[i+1] == '|':
			b.WriteByte('|')
			i++
		case row[i] == '|':
			cells = append(cells, strings.TrimSpace(b.String()))
			b.Reset()
		default:
			b.WriteByte(row[i])
		}
	}
	return append(cells, strings.TrimSpace(b.String()))
}

func parseAlign(cell string) colAlign {
	left, right := strings.HasPrefix(cell, ":"), strings.HasSuffix(cell, ":")
	switch {
	case left && right:
		return alignCenter
	case right:
		return alignRight
	}
	return alignLeft
}

// renderTable lays out a markdown table (header, separator, body rows) with
// aligned columns, wrapping cells so each line fits width (0 = no limit).
// A table that cannot fit even at minTableCol per column prints raw.
func renderTable(lines []string, width int) string {
	header := splitCells(lines[0])
	n := len(header)
	aligns := make([]colAlign, n)
	for c, cell := range splitCells(lines[1]) {
		if c < n {
			aligns[c] = parseAlign(cell)
		}
	}
	rows := [][]string{styledRow(header, n)}
	for _, l := range lines[2:] {
		rows = append(rows, styledRow(splitCells(l), n))
	}
	widths, ok := columnWidths(rows, width)
	if !ok {
		out := make([]string, len(lines))
		for i, l := range lines {
			out[i] = inline(l)
		}
		return strings.Join(out, "\n")
	}
	wrapped := make([][][]string, len(rows))
	final := make([]int, n)
	for r, row := range rows {
		wrapped[r] = make([][]string, n)
		for c, cell := range row {
			wrapped[r][c] = strings.Split(ansi.Wrap(cell, widths[c], ""), "\n")
			for _, l := range wrapped[r][c] {
				final[c] = max(final[c], ansi.StringWidth(l))
			}
		}
	}
	var out []string
	for r, row := range wrapped {
		out = append(out, rowLines(row, final, aligns, r == 0)...)
		if r == 0 {
			out = append(out, separatorLine(final))
		}
	}
	return strings.Join(out, "\n")
}

// styledRow pads or trims cells to n and applies inline markdown.
func styledRow(cells []string, n int) []string {
	row := make([]string, n)
	for c := range row {
		if c < len(cells) {
			row[c] = inline(cells[c])
		}
	}
	return row
}

// columnWidths returns each column's width limit. When the natural widths
// overflow width, narrow columns keep theirs and wide ones share the rest.
func columnWidths(rows [][]string, width int) ([]int, bool) {
	n := len(rows[0])
	natural := make([]int, n)
	sum := 0
	for c := range natural {
		for _, row := range rows {
			natural[c] = max(natural[c], ansi.StringWidth(row[c]))
		}
		natural[c] = max(natural[c], 1)
		sum += natural[c]
	}
	if width <= 0 {
		return natural, true
	}
	avail := width - ansi.StringWidth(tableColSep)*(n-1)
	if sum <= avail {
		return natural, true
	}
	if avail < n*minTableCol {
		return nil, false
	}
	order := make([]int, n)
	for c := range order {
		order[c] = c
	}
	// Narrowest first, so each column's fair share grows as small ones settle.
	slices.SortStableFunc(order, func(a, b int) int { return natural[a] - natural[b] })
	widths := make([]int, n)
	left := avail
	for k, c := range order {
		widths[c] = min(natural[c], left/(n-k))
		left -= widths[c]
	}
	return widths, true
}

// rowLines renders one table row, as tall as its tallest wrapped cell.
func rowLines(cells [][]string, widths []int, aligns []colAlign, header bool) []string {
	height := 0
	for _, c := range cells {
		height = max(height, len(c))
	}
	out := make([]string, height)
	for i := range out {
		parts := make([]string, len(cells))
		for c, cell := range cells {
			s := ""
			if i < len(cell) {
				s = cell[i]
			}
			if header && s != "" {
				s = boldStyle.Render(s)
			}
			parts[c] = pad(s, widths[c], aligns[c])
		}
		out[i] = strings.Join(parts, tableColSep)
	}
	return out
}

func pad(s string, width int, a colAlign) string {
	gap := max(width-ansi.StringWidth(s), 0)
	switch a {
	case alignRight:
		return strings.Repeat(" ", gap) + s
	case alignCenter:
		return strings.Repeat(" ", gap/2) + s + strings.Repeat(" ", gap-gap/2)
	}
	return s + strings.Repeat(" ", gap)
}

func separatorLine(widths []int) string {
	parts := make([]string, len(widths))
	for c, w := range widths {
		parts[c] = strings.Repeat("─", w)
	}
	return dimStyle.Render(strings.Join(parts, "─┼─"))
}
