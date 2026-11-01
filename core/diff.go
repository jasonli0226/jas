package core

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	diffContext  = 3         // context lines around each change
	maxDiffLines = 400       // lines kept across all hunks
	maxDiffBytes = 64 << 10  // Text bytes kept across all hunks
	maxLCSCells  = 2_000_000 // old×new middle lines before the LCS gives up
	tabWidth     = 4
)

// MaxDiffSource caps the old plus new text a diff is computed from; a larger
// change shows no diff.
const MaxDiffSource = 4 << 20

// Change is what a mutating tool changed (or would change), as raw text.
type Change struct {
	Path     string
	Old, New string // whole file before/after; Old is "" for a new file
	// HideRemoved: removed lines are screen-only (a write over an existing
	// file, whose old content the model may never have read).
	HideRemoved bool
	// ScreenOnly: the whole diff is screen-only, never logged (an edit, whose
	// context lines are file content the model never sent).
	ScreenOnly bool
}

// DiffLine is one display row of a diff.
type DiffLine struct {
	Kind  string   `json:"k"`           // " " context, "-" removed, "+" added
	Old   int      `json:"o,omitempty"` // 1-based old line number ("-" and " ")
	New   int      `json:"n,omitempty"` // 1-based new line number ("+" and " ")
	Text  string   `json:"t"`           // redacted, escaped, tabs expanded
	Spans [][2]int `json:"s,omitempty"` // changed-word rune ranges [start,end) in Text
}

// Diff is a redacted, capped, line-numbered diff of one file.
type Diff struct {
	Path      string       `json:"path"`
	Hunks     [][]DiffLine `json:"hunks"`
	Truncated bool         `json:"truncated,omitempty"`
}

var (
	pemBeginRe = regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)
	pemEndRe   = regexp.MustCompile(`-----END [A-Z ]*PRIVATE KEY-----`)
)

// diffOp is one aligned line: kind ' ', '-' or '+', with 0-based indexes into
// the old and new lines (-1 where the side has no line).
type diffOp struct {
	kind byte
	i, j int
}

// ComputeDiff diffs c.Old against c.New. Lines are aligned raw, then only
// the lines kept in hunks are redacted (a line inside a PEM private-key block
// becomes a marker, the others go through redact) and escaped, so no secret
// or control sequence reaches Text. redact may be nil.
//
// It returns nil when no line changed, which includes changes that are
// invisible line by line (CRLF only, a trailing newline only), and when
// old and new together exceed MaxDiffSource.
func ComputeDiff(c Change, redact func(string) string) *Diff {
	if c.Old == c.New || len(c.Old)+len(c.New) > MaxDiffSource {
		return nil
	}
	a, b := splitLines(c.Old), splitLines(c.New)
	old := shownSide{lines: a, key: privateKeyLines(a), redact: redact}
	upd := shownSide{lines: b, key: privateKeyLines(b), redact: redact}
	hunks := groupHunks(alignLines(a, b), old, upd)
	if len(hunks) == 0 {
		return nil
	}
	for _, h := range hunks {
		markSpans(h)
	}
	d := &Diff{Path: c.Path}
	d.Hunks, d.Truncated = capHunks(hunks)
	return d
}

// AddedOnly returns a copy of d without removed lines.
func (d *Diff) AddedOnly() *Diff {
	if d == nil {
		return nil
	}
	out := &Diff{Path: d.Path, Truncated: d.Truncated}
	for _, h := range d.Hunks {
		var kept []DiffLine
		for _, l := range h {
			if l.Kind != "-" {
				kept = append(kept, l)
			}
		}
		if len(kept) > 0 {
			out.Hunks = append(out.Hunks, kept)
		}
	}
	return out
}

// Lines is the total number of lines across all hunks.
func (d *Diff) Lines() int {
	if d == nil {
		return 0
	}
	n := 0
	for _, h := range d.Hunks {
		n += len(h)
	}
	return n
}

// FormatDiffPlain renders d as plain rows ("%6d %s %s"), with "  ⋮" between
// hunks and "  … diff truncated" last when d was capped.
func FormatDiffPlain(d *Diff) []string {
	if d == nil {
		return nil
	}
	var rows []string
	for i, h := range d.Hunks {
		if i > 0 {
			rows = append(rows, "  ⋮")
		}
		for _, l := range h {
			n := l.New
			if l.Kind == "-" {
				n = l.Old
			}
			rows = append(rows, fmt.Sprintf("%6d %s %s", n, l.Kind, l.Text))
		}
	}
	if d.Truncated {
		rows = append(rows, "  … diff truncated")
	}
	return rows
}

// shownSide is one side's raw lines, prepared for display only when a hunk
// keeps them.
type shownSide struct {
	lines  []string
	key    []bool // line i lies in a PEM private-key block
	redact func(string) string
}

// text is line i redacted and escaped. All non-PEM redaction rules are
// single-line, so redacting one line at a time is enough; a future
// multi-line rule must be handled here, like the PEM block mask.
func (s shownSide) text(i int) string {
	if s.key[i] {
		return mark("private_key")
	}
	l := s.lines[i]
	if s.redact != nil {
		l = s.redact(l)
	}
	return sanitizeLine(l)
}

// splitLines splits on \n, drops one trailing \r per line, and does not count
// the empty element after a final newline as a line.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "\n")
	if parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	for i, p := range parts {
		parts[i] = strings.TrimSuffix(p, "\r")
	}
	return parts
}

// privateKeyLines marks every line of a PEM private-key block, from BEGIN
// to END (or to the end of text), so each shows as one marker line and the
// numbering stays right.
func privateKeyLines(lines []string) []bool {
	key := make([]bool, len(lines))
	in := false
	for i, l := range lines {
		rest := l
		if !in {
			if !strings.Contains(l, "PRIVATE KEY-----") {
				continue
			}
			loc := pemBeginRe.FindStringIndex(l)
			if loc == nil {
				continue
			}
			in, rest = true, l[loc[1]:]
		}
		key[i] = true
		if pemEndRe.MatchString(rest) {
			in = false
		}
	}
	return key
}

// sanitizeLine escapes control and format runes as Sanitize does, but keeps
// spaces and expands tabs, so indentation survives.
func sanitizeLine(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\t':
			b.WriteString(strings.Repeat(" ", tabWidth))
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r):
			if r < 0x100 {
				fmt.Fprintf(&b, "\\x%02x", r)
			} else {
				fmt.Fprintf(&b, "\\u%04x", r)
			}
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// alignLines aligns a and b: common prefix and suffix, then an LCS of the
// middle, or all removed then all added when the middle is too large.
func alignLines(a, b []string) []diffOp {
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	ops := make([]diffOp, 0, len(a)+len(b))
	for k := 0; k < pre; k++ {
		ops = append(ops, diffOp{' ', k, k})
	}
	ops = append(ops, alignMiddle(a[pre:len(a)-suf], b[pre:len(b)-suf], pre)...)
	for k := suf; k > 0; k-- {
		ops = append(ops, diffOp{' ', len(a) - k, len(b) - k})
	}
	return ops
}

// alignMiddle aligns the untrimmed middle; off is its index in both inputs.
func alignMiddle(a, b []string, off int) []diffOp {
	if len(a)*len(b) > maxLCSCells {
		ops := make([]diffOp, 0, len(a)+len(b))
		for i := range a {
			ops = append(ops, diffOp{'-', off + i, -1})
		}
		for j := range b {
			ops = append(ops, diffOp{'+', -1, off + j})
		}
		return ops
	}
	return lcsOps(a, b, off)
}

// lcsOps walks an LCS table, preferring removals so each change reads as a
// run of "-" lines followed by a run of "+" lines.
func lcsOps(a, b []string, off int) []diffOp {
	n, m := len(a), len(b)
	w := m + 1
	tbl := make([]int32, (n+1)*w) // tbl[i*w+j] = LCS length of a[i:], b[j:]
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				tbl[i*w+j] = tbl[(i+1)*w+j+1] + 1
			} else {
				tbl[i*w+j] = max(tbl[(i+1)*w+j], tbl[i*w+j+1])
			}
		}
	}
	ops := make([]diffOp, 0, n+m)
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && a[i] == b[j]:
			ops = append(ops, diffOp{' ', off + i, off + j})
			i, j = i+1, j+1
		case j == m || (i < n && tbl[(i+1)*w+j] >= tbl[i*w+j+1]):
			ops = append(ops, diffOp{'-', off + i, -1})
			i++
		default:
			ops = append(ops, diffOp{'+', -1, off + j})
			j++
		}
	}
	return ops
}

// groupHunks keeps each change with diffContext lines around it, merging
// hunks whose context touches or overlaps.
func groupHunks(ops []diffOp, a, b shownSide) [][]DiffLine {
	var hunks [][]DiffLine
	start, end := -1, -1 // current hunk range [start,end) in ops
	flush := func() {
		if start >= 0 {
			hunks = append(hunks, toLines(ops[start:end], a, b))
		}
	}
	for k, op := range ops {
		if op.kind == ' ' {
			continue
		}
		lo, hi := max(0, k-diffContext), min(len(ops), k+diffContext+1)
		if start >= 0 && lo <= end {
			end = hi
			continue
		}
		flush()
		start, end = lo, hi
	}
	flush()
	return hunks
}

// toLines prepares the lines of one hunk. A context line shows its new text,
// masked when it lies in a private-key block on either side.
func toLines(ops []diffOp, a, b shownSide) []DiffLine {
	out := make([]DiffLine, len(ops))
	for k, op := range ops {
		l := DiffLine{Kind: string(op.kind)}
		switch {
		case op.j < 0:
			l.Old, l.Text = op.i+1, a.text(op.i)
		case op.i < 0:
			l.New, l.Text = op.j+1, b.text(op.j)
		case a.key[op.i]:
			l.Old, l.New, l.Text = op.i+1, op.j+1, mark("private_key")
		default:
			l.Old, l.New, l.Text = op.i+1, op.j+1, b.text(op.j)
		}
		out[k] = l
	}
	return out
}

// markSpans pairs each run of "-" lines with the following run of "+" lines,
// index by index, and marks the changed rune range of each pair.
func markSpans(h []DiffLine) {
	for k := 0; k < len(h); {
		if h[k].Kind != "-" {
			k++
			continue
		}
		rs := k
		for k < len(h) && h[k].Kind == "-" {
			k++
		}
		as := k
		for k < len(h) && h[k].Kind == "+" {
			k++
		}
		for p := 0; p < as-rs && p < k-as; p++ {
			markPair(&h[rs+p], &h[as+p])
		}
	}
}

// markPair sets the span left after the common rune prefix and suffix.
func markPair(del, add *DiffLine) {
	x, y := []rune(del.Text), []rune(add.Text)
	pre := 0
	for pre < len(x) && pre < len(y) && x[pre] == y[pre] {
		pre++
	}
	suf := 0
	for suf < len(x)-pre && suf < len(y)-pre && x[len(x)-1-suf] == y[len(y)-1-suf] {
		suf++
	}
	del.Spans = span(pre, len(x)-suf)
	add.Spans = span(pre, len(y)-suf)
}

func span(start, end int) [][2]int {
	if start >= end {
		return nil
	}
	return [][2]int{{start, end}}
}

// capHunks keeps at most maxDiffLines lines and maxDiffBytes of Text, cutting
// an over-long line to fit. It reports whether anything was dropped or cut.
func capHunks(hunks [][]DiffLine) ([][]DiffLine, bool) {
	var out [][]DiffLine
	lines, bytes := 0, 0
	for _, h := range hunks {
		var kept []DiffLine
		for _, l := range h {
			if lines == maxDiffLines || bytes == maxDiffBytes {
				return appendHunk(out, kept), true
			}
			if room := maxDiffBytes - bytes; len(l.Text) > room {
				kept = append(kept, cutLine(l, room))
				return appendHunk(out, kept), true
			}
			kept = append(kept, l)
			lines, bytes = lines+1, bytes+len(l.Text)
		}
		out = appendHunk(out, kept)
	}
	return out, false
}

func appendHunk(hunks [][]DiffLine, h []DiffLine) [][]DiffLine {
	if len(h) == 0 {
		return hunks
	}
	return append(hunks, h)
}

// cutLine cuts l.Text to at most n bytes on a rune boundary and clamps its
// spans to the shorter text.
func cutLine(l DiffLine, n int) DiffLine {
	for n > 0 && !utf8.RuneStart(l.Text[n]) {
		n--
	}
	l.Text = l.Text[:n]
	runes := utf8.RuneCountInString(l.Text)
	var spans [][2]int
	for _, s := range l.Spans {
		if s[0] < runes {
			spans = append(spans, [2]int{s[0], min(s[1], runes)})
		}
	}
	l.Spans = spans
	return l
}

// DiffFromPayload decodes a diff carried in an event payload. After the
// redacting UI's JSON round trip it is a map, not a *Diff. It returns nil
// when v is absent or malformed.
func DiffFromPayload(v any) *Diff {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var d Diff
	if err := json.Unmarshal(b, &d); err != nil || d.Path == "" && d.Hunks == nil {
		return nil
	}
	return &d
}
