package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const goodNote = "---\nname: x\ndescription: a fact\ntype: user\n---\nbody\n"

func newGuard(t *testing.T) Guard {
	t.Helper()
	d, err := DirsFor(t.TempDir(), "/work/repo")
	if err != nil {
		t.Fatal(err)
	}
	return Guard{Dirs: d, Mode: ModeOn, Redact: func(s string) string { return strings.ReplaceAll(s, "sk-secret", "[redacted]") }}
}

func TestGuard_Check(t *testing.T) {
	g := newGuard(t)
	ro := g
	ro.Mode = ModeRead
	hl := g
	hl.Headless = true
	p, q := g.Dirs.Profile, g.Dirs.Project
	tests := []struct {
		name    string
		g       Guard
		tool    string
		path    string
		existed bool
		content string
		want    string
	}{
		{"headless", hl, "write", filepath.Join(p, "x.md"), false, goodNote, "memory is read-only in this run: no one can approve the write"},
		{"read mode", ro, "write", filepath.Join(p, "x.md"), false, goodNote, "memory is read-only in this session (memory: read)"},
		{"deeper folder", g, "write", filepath.Join(p, "sub", "x.md"), false, goodNote, "memory notes belong directly in"},
		{"non-md", g, "write", filepath.Join(p, "x.txt"), false, goodNote, "memory holds .md notes only"},
		{"index whole write", g, "write", filepath.Join(p, "MEMORY.md"), true, "- [x](x.md)\n", "change MEMORY.md with edit, not a whole-file write"},
		{"bad note", g, "write", filepath.Join(p, "x.md"), false, "no frontmatter", "memory note needs frontmatter"},
		{"name mismatch", g, "write", filepath.Join(p, "y.md"), false, goodNote, ""},
		{"secret in note", g, "write", filepath.Join(p, "x.md"), false, goodNote + "sk-secret\n", "memory notes cannot hold secrets; describe where the secret lives instead"},
		{"secret in index", g, "edit", filepath.Join(q, "MEMORY.md"), true, "- sk-secret\n", "memory notes cannot hold secrets"},
		{"ok note", g, "write", filepath.Join(p, "x.md"), false, goodNote, ""},
		{"ok project note", g, "write", filepath.Join(q, "x.md"), true, goodNote, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.g.Check(tt.tool, tt.path, tt.existed, tt.content)
			if tt.name == "name mismatch" {
				if err == nil {
					t.Fatal("want error for name != file name")
				}
				return
			}
			if tt.want == "" {
				if err != nil {
					t.Fatalf("want nil, got %v", err)
				}
				return
			}
			if err == nil || !strings.HasPrefix(err.Error(), tt.want) {
				t.Fatalf("want prefix %q, got %v", tt.want, err)
			}
		})
	}
}

func TestGuard_NilRedactSkipsSecrets(t *testing.T) {
	g := newGuard(t)
	g.Redact = nil
	if err := g.Check("write", filepath.Join(g.Dirs.Profile, "x.md"), false, goodNote+"sk-secret\n"); err != nil {
		t.Fatal(err)
	}
}

func TestGuard_OffAllows(t *testing.T) {
	g := newGuard(t)
	g.Mode = ModeOff
	if err := g.Check("write", filepath.Join(g.Dirs.Profile, "x.txt"), false, ""); err != nil {
		t.Fatal(err)
	}
}

func TestGuard_OtherProjectRefused(t *testing.T) {
	g := newGuard(t)
	other := filepath.Join(g.Dirs.Profile, "projects", "-other")
	for _, p := range []string{filepath.Join(other, "x.md"), filepath.Join(g.Dirs.Project, "sub", "x.md")} {
		err := g.Check("write", p, false, goodNote)
		if err == nil || !strings.HasPrefix(err.Error(), "memory notes belong directly in") {
			t.Errorf("%s: got %v", p, err)
		}
	}
}

func TestGuard_CreatesProjectIndex(t *testing.T) {
	g := newGuard(t)
	if err := g.Check("write", filepath.Join(g.Dirs.Project, "MEMORY.md"), false, "- [x](x.md)\n"); err != nil {
		t.Fatal(err)
	}
}

func TestGuard_EditIndexAllowed(t *testing.T) {
	g := newGuard(t)
	if err := g.Check("edit", filepath.Join(g.Dirs.Profile, "MEMORY.md"), true, "- [x](x.md)\n"); err != nil {
		t.Fatal(err)
	}
}

func TestGuard_OutsideMemoryIgnored(t *testing.T) {
	g := newGuard(t)
	g.Mode = ModeRead
	if err := g.Check("write", "/work/repo/main.go", false, "package main"); err != nil {
		t.Fatal(err)
	}
	if g.Holds("/work/repo/main.go") {
		t.Fatal("Holds outside memory")
	}
}

func TestGuard_SymlinkedDirs(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "home")
	if err := os.Symlink(real, link); err != nil {
		t.Skip(err)
	}
	d, err := DirsFor(filepath.Join(link, ".jas", "profiles", "p"), "/work/repo")
	if err != nil {
		t.Fatal(err)
	}
	g := Guard{Dirs: d, Mode: ModeRead}
	resolvedProfile := filepath.Join(real, ".jas", "profiles", "p", "memory")
	if !g.Holds(filepath.Join(resolvedProfile, "x.md")) {
		t.Fatal("Holds must see through the symlinked home (profile dir missing)")
	}
	if err := g.Check("write", filepath.Join(resolvedProfile, "x.md"), false, goodNote); err == nil {
		t.Fatal("read mode must refuse")
	}
	g.Mode = ModeOn
	if err := g.Check("write", filepath.Join(resolvedProfile, "x.md"), false, goodNote); err != nil {
		t.Fatalf("folder rule on resolved dirs: %v", err)
	}
	projKey := filepath.Base(d.Project)
	if err := g.Check("write", filepath.Join(resolvedProfile, "projects", projKey, "x.md"), false, goodNote); err != nil {
		t.Fatalf("project dir: %v", err)
	}
}

func TestParseMode(t *testing.T) {
	for s, want := range map[string]Mode{"on": ModeOn, "read": ModeRead, "off": ModeOff} {
		got, err := ParseMode(s)
		if err != nil || got != want {
			t.Errorf("%s: %v %v", s, got, err)
		}
	}
	if _, err := ParseMode("bogus"); err == nil {
		t.Error("want error")
	}
}

func TestResolveMode(t *testing.T) {
	tests := []struct {
		base, project string
		mode          Mode
		hide          bool
	}{
		{"on", "", ModeOn, false}, {"on", "on", ModeOn, false}, {"on", "read", ModeRead, false}, {"on", "off", ModeRead, true},
		{"read", "", ModeRead, false}, {"read", "on", ModeRead, false}, {"read", "read", ModeRead, false}, {"read", "off", ModeRead, false},
		{"off", "", ModeOff, false}, {"off", "on", ModeOff, false}, {"off", "read", ModeOff, false}, {"off", "off", ModeOff, false},
	}
	for _, tt := range tests {
		m, h := ResolveMode(tt.base, tt.project)
		if m != tt.mode || h != tt.hide {
			t.Errorf("base %q project %q: got %v,%v want %v,%v", tt.base, tt.project, m, h, tt.mode, tt.hide)
		}
	}
}
