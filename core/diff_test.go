package core_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
)

func middleEdit() *core.Diff {
	return core.ComputeDiff(core.Change{
		Path: "f.txt",
		Old:  "a\nb\nc\nd\ne\nf\ng\nh\n",
		New:  "a\nb\nc\nD\ne\nf\ng\nh\n",
	}, nil)
}

func allLines(d *core.Diff) []core.DiffLine {
	var out []core.DiffLine
	for _, h := range d.Hunks {
		out = append(out, h...)
	}
	return out
}

func TestComputeDiff_EditMiddleLine(t *testing.T) {
	d := middleEdit()
	assert.Eq(t, d.Path, "f.txt")
	assert.Eq(t, len(d.Hunks), 1)
	ctx := func(n int, s string) core.DiffLine { return core.DiffLine{Kind: " ", Old: n, New: n, Text: s} }
	want := []core.DiffLine{
		ctx(1, "a"), ctx(2, "b"), ctx(3, "c"),
		{Kind: "-", Old: 4, Text: "d", Spans: [][2]int{{0, 1}}},
		{Kind: "+", New: 4, Text: "D", Spans: [][2]int{{0, 1}}},
		ctx(5, "e"), ctx(6, "f"), ctx(7, "g"),
	}
	if !reflect.DeepEqual(d.Hunks[0], want) {
		t.Fatalf("hunk:\n got %+v\nwant %+v", d.Hunks[0], want)
	}
	assert.Eq(t, d.Truncated, false)
}

func TestComputeDiff_NewFileAllAdded(t *testing.T) {
	d := core.ComputeDiff(core.Change{Path: "n", New: "x\ny\n"}, nil)
	assert.Eq(t, len(d.Hunks), 1)
	h := d.Hunks[0]
	assert.Eq(t, len(h), 2)
	for i, l := range h {
		assert.Eq(t, l.Kind, "+")
		assert.Eq(t, l.New, i+1)
		assert.Eq(t, l.Old, 0)
	}
	assert.Eq(t, h[0].Text, "x")
	assert.Eq(t, h[1].Text, "y")
}

func TestComputeDiff_EqualIsNil(t *testing.T) {
	if d := core.ComputeDiff(core.Change{Old: "same\n", New: "same\n"}, nil); d != nil {
		t.Fatalf("want nil, got %+v", d)
	}
}

func TestComputeDiff_CRLF(t *testing.T) {
	d := core.ComputeDiff(core.Change{Old: "a\r\nb\r\n", New: "a\r\nB\r\n"}, nil)
	var changed []core.DiffLine
	for _, l := range allLines(d) {
		assert.NotContains(t, l.Text, "\r")
		if l.Kind != " " {
			changed = append(changed, l)
		}
	}
	assert.Eq(t, len(changed), 2)
	assert.Eq(t, changed[0].Kind, "-")
	assert.Eq(t, changed[0].Old, 2)
	assert.Eq(t, changed[0].Text, "b")
	assert.Eq(t, changed[1].Kind, "+")
	assert.Eq(t, changed[1].New, 2)
	assert.Eq(t, changed[1].Text, "B")
}

func TestComputeDiff_NoTrailingNewlineAndEmpty(t *testing.T) {
	d := core.ComputeDiff(core.Change{Old: "a", New: "a\nb"}, nil)
	var plus []core.DiffLine
	for _, l := range allLines(d) {
		assert.Eq(t, l.Kind != "-", true)
		if l.Kind == "+" {
			plus = append(plus, l)
		}
	}
	assert.Eq(t, len(plus), 1)
	assert.Eq(t, plus[0].New, 2)
	assert.Eq(t, plus[0].Text, "b")
	if d := core.ComputeDiff(core.Change{}, nil); d != nil {
		t.Fatalf("empty: want nil, got %+v", d)
	}
}

func numbered(n int, edit map[int]string) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		if s, ok := edit[i]; ok {
			b.WriteString(s + "\n")
			continue
		}
		fmt.Fprintf(&b, "line %d\n", i)
	}
	return b.String()
}

func TestComputeDiff_TwoHunksFarApart(t *testing.T) {
	d := core.ComputeDiff(core.Change{
		Old: numbered(50, nil),
		New: numbered(50, map[int]string{2: "two", 40: "forty"}),
	}, nil)
	assert.Eq(t, len(d.Hunks), 2)
}

func TestComputeDiff_MergesOverlappingContext(t *testing.T) {
	d := core.ComputeDiff(core.Change{
		Old: numbered(50, nil),
		New: numbered(50, map[int]string{10: "ten", 16: "sixteen"}),
	}, nil)
	assert.Eq(t, len(d.Hunks), 1)
}

func TestComputeDiff_PrivateKeyMaskedLinePreserving(t *testing.T) {
	src := "x\n-----BEGIN RSA PRIVATE KEY-----\nMIIEow\nAAAA\n-----END RSA PRIVATE KEY-----\ny\n"
	d := core.ComputeDiff(core.Change{New: src}, nil)
	ls := allLines(d)
	assert.Eq(t, len(ls), 6)
	assert.Eq(t, ls[0].Text, "x")
	for _, l := range ls[1:5] {
		assert.Eq(t, l.Text, "[REDACTED:private_key]")
	}
	assert.Eq(t, ls[5].Text, "y")
	assert.Eq(t, ls[5].New, 6)
}

func TestComputeDiff_PrivateKeyUnterminatedMasksToEnd(t *testing.T) {
	src := "x\n-----BEGIN PRIVATE KEY-----\nMIIEow\nAAAA\n"
	d := core.ComputeDiff(core.Change{New: src}, nil)
	ls := allLines(d)
	assert.Eq(t, len(ls), 4)
	for _, l := range ls[1:] {
		assert.Eq(t, l.Text, "[REDACTED:private_key]")
	}
}

func TestComputeDiff_RedactAppliedPerLine(t *testing.T) {
	redact := func(s string) string { return strings.ReplaceAll(s, "sk-secret", "[REDACTED:exact]") }
	d := core.ComputeDiff(core.Change{Old: "k = 1\n", New: "k = sk-secret\n"}, redact)
	for _, l := range allLines(d) {
		assert.NotContains(t, l.Text, "sk-secret")
	}
}

func TestComputeDiff_EscapesControlKeepsIndent(t *testing.T) {
	d := core.ComputeDiff(core.Change{New: "\tx\x1b[31m\n"}, nil)
	assert.Eq(t, d.Hunks[0][0].Text, "    x\\x1b[31m")
}

func textBytes(d *core.Diff) int {
	n := 0
	for _, l := range allLines(d) {
		n += len(l.Text)
	}
	return n
}

func TestComputeDiff_CapsLinesAndBytes(t *testing.T) {
	d := core.ComputeDiff(core.Change{New: numbered(10000, nil)}, nil)
	assert.Eq(t, d.Lines(), 400)
	assert.Eq(t, d.Truncated, true)

	d = core.ComputeDiff(core.Change{New: strings.Repeat("x", 1<<20) + "\n"}, nil)
	assert.Eq(t, d.Truncated, true)
	if n := textBytes(d); n > 64<<10 || n == 0 {
		t.Fatalf("text bytes = %d, want 1..%d", n, 64<<10)
	}

	d = core.ComputeDiff(core.Change{New: "a" + strings.Repeat("é", 1<<19) + "\n"}, nil)
	l := d.Hunks[0][0]
	assert.Eq(t, utf8.ValidString(l.Text), true)
	assert.Eq(t, len(l.Text) <= 64<<10, true)
}

func TestComputeDiff_LargeRewriteFallback(t *testing.T) {
	var o, n strings.Builder
	for i := 0; i < 3000; i++ {
		fmt.Fprintf(&o, "old %d\n", i)
		fmt.Fprintf(&n, "new %d\n", i)
	}
	d := core.ComputeDiff(core.Change{Old: o.String(), New: n.String()}, nil)
	assert.Eq(t, d.Lines(), 400)
	assert.Eq(t, d.Truncated, true)
	for i, l := range allLines(d) {
		assert.Eq(t, l.Kind, "-")
		assert.Eq(t, l.Old, i+1)
	}
}

func TestDiff_AddedOnlyDropsRemoved(t *testing.T) {
	d := middleEdit()
	a := d.AddedOnly()
	assert.Eq(t, a.Lines(), 7)
	for _, l := range allLines(a) {
		assert.Eq(t, l.Kind != "-", true)
	}
	assert.Eq(t, d.Lines(), 8) // original untouched
}

func TestFormatDiffPlain(t *testing.T) {
	rows := core.FormatDiffPlain(middleEdit())
	assert.Eq(t, strings.Join(rows, "\n"), strings.Join([]string{
		"     1   a", "     2   b", "     3   c",
		"     4 - d", "     4 + D",
		"     5   e", "     6   f", "     7   g",
	}, "\n"))

	two := core.ComputeDiff(core.Change{
		Old: numbered(50, nil),
		New: numbered(50, map[int]string{2: "two", 40: "forty"}),
	}, nil)
	two.Truncated = true
	rows = core.FormatDiffPlain(two)
	got := strings.Join(rows, "\n")
	assert.Contains(t, got, "\n  ⋮\n")
	assert.Eq(t, rows[len(rows)-1], "  … diff truncated")
}

func TestDiffFromPayload(t *testing.T) {
	d := middleEdit()
	b, err := json.Marshal(d)
	assert.NoErr(t, err)
	var v any
	assert.NoErr(t, json.Unmarshal(b, &v))
	got := core.DiffFromPayload(v)
	if !reflect.DeepEqual(got, d) {
		t.Fatalf("got %+v, want %+v", got, d)
	}
	if core.DiffFromPayload(nil) != nil || core.DiffFromPayload("x") != nil || core.DiffFromPayload(map[string]any{"hunks": 3}) != nil {
		t.Fatal("absent or malformed payload must give nil")
	}
}

// shownContext is core's diffContext: context lines around each change.
const shownContext = 3

func TestComputeDiff_LargeFileRedactsOnlyShownLines(t *testing.T) {
	const n = 100_000
	calls := 0
	redact := func(s string) string { calls++; return s }
	start := time.Now()
	d := core.ComputeDiff(core.Change{
		Old: numbered(n, nil),
		New: numbered(n, map[int]string{n / 2: "changed"}),
	}, redact)
	if el := time.Since(start); el > 2*time.Second {
		t.Fatalf("one-line edit in a %d-line file took %v", n, el)
	}
	ls := allLines(d)
	assert.Eq(t, len(ls), 2*shownContext+2)
	assert.Eq(t, calls <= len(ls), true)
	for _, l := range ls {
		if l.Kind == "+" {
			assert.Eq(t, l.New, n/2)
			assert.Eq(t, l.Text, "changed")
		}
	}
}

func TestComputeDiff_OverSourceCapIsNil(t *testing.T) {
	big := strings.Repeat("a\n", core.MaxDiffSource/2)
	if d := core.ComputeDiff(core.Change{Old: big, New: big + "b\n"}, nil); d != nil {
		t.Fatal("want nil over the source cap")
	}
}

func TestComputeDiff_PrivateKeyPartlyInHunkMasked(t *testing.T) {
	var key []string
	key = append(key, "-----BEGIN RSA PRIVATE KEY-----")
	for i := 0; i < 10; i++ {
		key = append(key, fmt.Sprintf("SECRETBODY%02d", i))
	}
	key = append(key, "-----END RSA PRIVATE KEY-----")
	old := "a\nb\n" + strings.Join(key, "\n") + "\nc\nd\n"
	// Change the line after END: the hunk's context reaches into the key
	// body but not its BEGIN line.
	d := core.ComputeDiff(core.Change{Old: old, New: strings.Replace(old, "\nc\n", "\nC\n", 1)}, nil)
	ls := allLines(d)
	masked := 0
	for _, l := range ls {
		assert.NotContains(t, l.Text, "SECRETBODY")
		assert.NotContains(t, l.Text, "PRIVATE KEY")
		if l.Text == "[REDACTED:private_key]" {
			masked++
		}
	}
	assert.Eq(t, masked, shownContext)
	assert.Eq(t, ls[len(ls)-2].Text, "C")
	assert.Eq(t, ls[len(ls)-2].New, 2+len(key)+1)
}

func TestComputeDiff_ContextMaskedWhenKeyOnOldSide(t *testing.T) {
	old := "-----BEGIN PRIVATE KEY-----\nSECRETBODY\n-----END PRIVATE KEY-----\n"
	d := core.ComputeDiff(core.Change{Old: old, New: "SECRETBODY\n"}, nil)
	for _, l := range allLines(d) {
		assert.NotContains(t, l.Text, "SECRETBODY")
	}
}
