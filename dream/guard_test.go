package dream

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func guardFixture(t *testing.T) (Guard, string) {
	t.Helper()
	ws := t.TempDir()
	ws, _ = filepath.EvalSymlinks(ws)
	prop := filepath.Join(ws, "proposed")
	if err := os.MkdirAll(filepath.Join(prop, "projects", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	return Guard{Proposed: prop, Redact: func(s string) string { return strings.ReplaceAll(s, "SECRET", "[redacted]") }}, ws
}

const goodNote = "---\nname: a-note\ndescription: one line\ntype: user\n---\nbody\n"

func TestGuard_OutsideProposedRefused(t *testing.T) {
	g, ws := guardFixture(t)
	for _, p := range []string{"evidence.md", "logs/s.md", "proposed-evil/a.md", "../a.md"} {
		err := g.Precheck("write", filepath.Join(ws, p))
		if err == nil || !strings.Contains(err.Error(), "dreams write only inside proposed/") {
			t.Errorf("%s: err = %v", p, err)
		}
	}
}

func TestGuard_IndexWholeWriteAllowed(t *testing.T) {
	g, _ := guardFixture(t)
	idx := filepath.Join(g.Proposed, "MEMORY.md")
	if err := os.WriteFile(idx, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := g.Check("write", idx, true, "- [a](a.md)\n"); err != nil {
		t.Fatal(err)
	}
	if err := g.Check("write", filepath.Join(g.Proposed, "memory.MD"), false, "anything"); err != nil {
		t.Fatalf("index variants are not notes: %v", err)
	}
}

func TestGuard_AnyProjectFolderAllowedNestedRefused(t *testing.T) {
	g, _ := guardFixture(t)
	for _, p := range []string{"a-note.md", "projects/x/a-note.md", "projects/brand-new/a-note.md"} {
		if err := g.Check("write", filepath.Join(g.Proposed, p), false, goodNote); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	for _, p := range []string{"projects/x/y/a-note.md", "other/a-note.md", "projects/a-note.md", "a-note.txt"} {
		if err := g.Precheck("write", filepath.Join(g.Proposed, p)); err == nil {
			t.Errorf("%s: want refusal", p)
		}
	}
}

func TestGuard_FrontmatterAndSecret(t *testing.T) {
	g, _ := guardFixture(t)
	p := filepath.Join(g.Proposed, "a-note.md")
	if err := g.Check("write", p, false, "no frontmatter"); err == nil {
		t.Error("want frontmatter error")
	}
	err := g.Check("write", p, false, goodNote+"SECRET\n")
	want := "memory notes cannot hold secrets; describe where the secret lives instead"
	if err == nil || err.Error() != want {
		t.Errorf("err = %v", err)
	}
}

func TestGuard_SymlinkedProposed(t *testing.T) {
	real := t.TempDir()
	real, _ = filepath.EvalSymlinks(real)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skip(err)
	}
	g := Guard{Proposed: link}
	if err := g.Check("write", filepath.Join(real, "a-note.md"), false, goodNote); err != nil {
		t.Fatal(err)
	}
}
