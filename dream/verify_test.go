package dream

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestVerify_Unexplained(t *testing.T) {
	base := map[string]string{"a.md": "1", "b.md": "1", "c.md": "1"}
	prop := map[string]string{"a.md": "2", "b.md": "2", "d.md": "1"}
	ch := []Change{{Kind: "fix", Paths: []string{"proposed/a.md"}}, {Kind: "fix", Paths: []string{"./b.md", "d.md"}}}
	f := Verify(base, prop, ch, nil, nil, nil, false)
	if !slices.Equal(f.Unexplained, []string{"c.md"}) {
		t.Fatalf("got %v", f.Unexplained)
	}
	if f.Counts.Changed != 2 || f.Counts.Added != 1 || f.Counts.Removed != 1 {
		t.Fatalf("counts %+v", f.Counts)
	}
}

func TestVerify_UnverifiedCitation(t *testing.T) {
	seqs := Seqs{"s1": {1: true, 0: true}}
	ch := []Change{{Kind: "fix", Evidence: []string{"s1#1", "s1#99", "s9#1", "s1#0", "junk"}}}
	f := Verify(nil, nil, ch, seqs, nil, nil, false)
	if !slices.Equal(f.Unverified[0], []string{"s1#99", "s9#1", "s1#0", "junk"}) {
		t.Fatalf("got %v", f.Unverified)
	}
}

func TestVerify_UnsupportedAdd(t *testing.T) {
	seqs := Seqs{"s1": {1: true}}
	ch := []Change{
		{Kind: "add", Evidence: []string{"s1#99"}},
		{Kind: "fix"},
		{Kind: "correct", Evidence: []string{"s1#1"}},
		{Kind: "promote"},
	}
	f := Verify(nil, nil, ch, seqs, nil, nil, false)
	if !slices.Equal(f.Unsupported, []int{0}) || f.Counts.Promote != 1 {
		t.Fatalf("got %v %+v", f.Unsupported, f.Counts)
	}
}

func notes(n int) map[string]string {
	m := map[string]string{"MEMORY.md": "x"}
	for i := 0; i < n; i++ {
		m[string(rune('a'+i))+".md"] = "h"
	}
	return m
}

func TestVerify_LargeRemoval(t *testing.T) {
	base := notes(9)
	for _, tc := range []struct {
		removed int
		want    bool
	}{{4, true}, {3, false}} {
		p := notes(9)
		for i := 0; i < tc.removed; i++ {
			delete(p, string(rune('a'+i))+".md")
		}
		if got := Verify(base, p, nil, nil, nil, nil, false).LargeRemoval; got != tc.want {
			t.Fatalf("removed %d: %v", tc.removed, got)
		}
	}
}

func TestVerify_PartialBestEffort(t *testing.T) {
	if !Verify(nil, nil, nil, nil, nil, nil, true).BestEffort {
		t.Fatal("want best effort")
	}
}

func TestVerify_LintCounts(t *testing.T) {
	before := []Issue{{KindOrphan, "a.md", ""}, {KindFrontmatter, "b.md", ""}}
	after := []Issue{{KindFrontmatter, "b.md", ""}, {KindDanglingLink, "c.md", "x"}}
	c := Verify(nil, nil, nil, nil, before, after, false).Counts
	if c.LintFixed != 1 || c.LintOpen != 1 {
		t.Fatalf("%+v", c)
	}
}

func TestRenderReport_Order(t *testing.T) {
	d := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	m := Meta{ID: "20261113T000000-abcd", Profile: "default", Partial: true,
		Window: Window{Since: d, Until: d, Sessions: []string{"s1"}}}
	logs := []LogRef{{SessionID: "s1", Root: "/r/proj", Started: d}}
	ch := []Change{{Kind: "add", Paths: []string{"n.md"}, Reason: "why", Evidence: []string{"s1#1", "s7#2"}}}
	f := Verify(map[string]string{"x.md": "1"}, map[string]string{}, ch, Seqs{"s1": {1: true}}, nil, nil, true)
	out := RenderReport(m, "glm", 123, ch, f, logs)
	hdr := strings.Index(out, "partial")
	w := strings.Index(out, "## Warnings")
	c := strings.Index(out, "## Changes")
	if !(hdr >= 0 && hdr < w && w < c) {
		t.Fatalf("order wrong:\n%s", out)
	}
	for _, want := range []string{"Best-effort", "`s1#1` (/r/proj, 2026-11-10)", "`s7#2` unverified", "Unexplained changes"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
}

func TestRenderReport_SanitizesModelText(t *testing.T) {
	d := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	m := Meta{ID: "20261113T000000-abcd", Profile: "default", Window: Window{Since: d, Until: d}}
	esc := "\x1b]0;x\x07\x1b[2J"
	ch := []Change{{Kind: "add", Paths: []string{"n" + esc + ".md"}, Reason: "why" + esc + "\n## Lint", Evidence: []string{"s1#1" + esc}}}
	f := Verify(map[string]string{}, map[string]string{"n.md": "1"}, ch, Seqs{}, nil,
		[]Issue{{KindOrphan, "o" + esc + ".md", esc}}, false)
	out := RenderReport(m, "glm", 1, ch, f, []LogRef{{SessionID: "s1", Root: "/r" + esc}})
	if strings.ContainsAny(out, "\x1b\x07") {
		t.Fatalf("report holds escapes: %q", out)
	}
	if strings.Count(out, "\n## Lint") != 1 {
		t.Fatalf("a reason opened a section:\n%s", out)
	}
}

func TestNewWarnings(t *testing.T) {
	ch := []Change{
		{Kind: "fix", Paths: []string{"a.md"}, Reason: "ok", Evidence: []string{"s1#1"}},
		{Kind: "add", Paths: []string{"b.md"}, Reason: "uncited"},
	}
	f := Verify(map[string]string{"x.md": "1"}, map[string]string{"a.md": "2", "b.md": "3"}, ch, Seqs{"s1": {1: true}}, nil, nil, true)
	w := NewWarnings(f, ch)
	if !w.Any() || !w.BestEffort || !w.LargeRemoval || len(w.Unexplained) != 1 || w.Unexplained[0] != "x.md" {
		t.Fatalf("warnings = %+v", w)
	}
	if len(w.Unsupported) != 1 || w.Unsupported[0].Kind != "add" || w.Unsupported[0].Reason != "uncited" {
		t.Fatalf("unsupported = %+v", w.Unsupported)
	}
	if (Warnings{BestEffort: true}).Any() {
		t.Fatal("best-effort alone is no warning")
	}
}
