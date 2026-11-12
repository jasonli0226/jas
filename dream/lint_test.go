package dream

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/memory"
)

func note(name string, body string) string {
	return "---\nname: " + name + "\ndescription: d\ntype: user\n---\n" + body
}

func put(t *testing.T, store, rel, content string) {
	t.Helper()
	p := filepath.Join(store, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func lint(store string) []Issue {
	return Lint(store, func(s string) string { return strings.ReplaceAll(s, "SECRET", "[redacted]") },
		func(string) bool { return true })
}

func want(t *testing.T, got []Issue, exp ...Issue) {
	t.Helper()
	if len(got) != len(exp) {
		t.Fatalf("issues = %+v, want %+v", got, exp)
	}
	for i := range exp {
		if got[i].Kind != exp[i].Kind || got[i].Path != exp[i].Path || (exp[i].Detail != "" && got[i].Detail != exp[i].Detail) {
			t.Fatalf("issue %d = %+v, want %+v", i, got[i], exp[i])
		}
	}
}

func lines(n int, prefix string) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString("- [" + prefix + "](" + prefix + ".md) — x\n")
	}
	return b.String()
}

func TestLint_Frontmatter(t *testing.T) {
	s := t.TempDir()
	put(t, s, "a.md", "---\nname: a\ndescription: d\n---\n")
	put(t, s, "MEMORY.md", "- [A](a.md) — x\n")
	want(t, lint(s), Issue{Kind: "frontmatter", Path: "a.md"})
}

func TestLint_MissingTargetAndOrphan(t *testing.T) {
	s := t.TempDir()
	put(t, s, "MEMORY.md", "- [A](a.md) — x\n")
	put(t, s, "b.md", note("b", ""))
	want(t, lint(s), Issue{"missing-target", "MEMORY.md", "a.md"}, Issue{Kind: "orphan", Path: "b.md"})
}

func TestLint_DanglingLink(t *testing.T) {
	s := t.TempDir()
	key := memory.Key("/p")
	put(t, s, "MEMORY.md", "- [A](a.md) — x\n- [B](b.md) — x\n")
	put(t, s, "a.md", note("a", "see [[nope]] and [[nope]]"))
	put(t, s, "b.md", note("b", "x"))
	put(t, s, "projects/"+key+"/MEMORY.md", "- [C](c.md) — x\n")
	put(t, s, "projects/"+key+"/c.md", note("c", "[[b]]"))
	want(t, lint(s), Issue{"dangling-link", "a.md", "nope"})
}

func overCaps(store string) []string {
	var out []string
	for _, i := range lint(store) {
		if i.Kind == "over-cap" {
			out = append(out, i.Path)
		}
	}
	return out
}

func TestLint_OverCap(t *testing.T) {
	key := memory.Key("/p")
	s := t.TempDir()
	put(t, s, "MEMORY.md", lines(150, "a"))
	put(t, s, "projects/"+key+"/MEMORY.md", lines(60, "b"))
	if got := overCaps(s); len(got) != 1 || got[0] != "projects/"+key+"/MEMORY.md" {
		t.Fatalf("over-cap = %v", got)
	}

	s = t.TempDir()
	put(t, s, "MEMORY.md", lines(201, "a"))
	put(t, s, "projects/"+key+"/MEMORY.md", lines(1, "b"))
	if got := overCaps(s); len(got) != 1 || got[0] != "MEMORY.md" {
		t.Fatalf("over-cap = %v", got)
	}

	s = t.TempDir()
	put(t, s, "MEMORY.md", lines(150, "a"))
	put(t, s, "projects/"+key+"/MEMORY.md", lines(50, "b"))
	if got := overCaps(s); len(got) != 0 {
		t.Fatalf("exactly at the cap reported: %v", got)
	}
}

func TestLint_Shadowed(t *testing.T) {
	s := t.TempDir()
	key := memory.Key("/p")
	put(t, s, "MEMORY.md", "- [A](a.md) — x\n")
	put(t, s, "a.md", note("a", ""))
	put(t, s, "projects/"+key+"/MEMORY.md", "- [A](a.md) — x\n")
	put(t, s, "projects/"+key+"/a.md", note("a", ""))
	want(t, lint(s), Issue{"shadowed", "projects/" + key + "/a.md", "a"})
}

func TestLint_NotAllowed(t *testing.T) {
	s := t.TempDir()
	put(t, s, "x.txt", "x")
	put(t, s, "sub/y.md", note("y", ""))
	want(t, lint(s), Issue{Kind: "not-allowed", Path: "sub/y.md"}, Issue{Kind: "not-allowed", Path: "x.txt"})
}

func TestLint_ProjectGone(t *testing.T) {
	s := t.TempDir()
	key := memory.Key("/gone/repo")
	put(t, s, "projects/"+key+"/MEMORY.md", "")
	got := Lint(s, nil, func(p string) bool { return p != "/gone/repo" })
	want(t, got, Issue{"project-gone", "projects/" + key, "/gone/repo"})
}

func TestLint_HashedKeyNotGone(t *testing.T) {
	s := t.TempDir()
	key := memory.Key("/" + strings.Repeat("a", 300))
	put(t, s, "projects/"+key+"/MEMORY.md", "")
	got := Lint(s, nil, func(string) bool { return false })
	want(t, got)
}

func TestLint_Secret(t *testing.T) {
	s := t.TempDir()
	put(t, s, "MEMORY.md", "- [A](a.md) — x\n")
	put(t, s, "a.md", note("a", "token SECRET"))
	want(t, lint(s), Issue{Kind: "secret", Path: "a.md"})
}
