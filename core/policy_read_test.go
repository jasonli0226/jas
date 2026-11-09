package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
)

func readRootFixture(t *testing.T) (ExecPolicy, string) {
	t.Helper()
	root, skill := realpath(t, t.TempDir()), realpath(t, t.TempDir())
	assert.NoErr(t, os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("x"), 0o644))
	assert.NoErr(t, os.WriteFile(filepath.Join(skill, ".env"), []byte("K=v"), 0o644))
	return ExecPolicy{Root: root, ReadRoots: []string{skill}}, skill
}

func TestResolveRead_AllowsReadRoot(t *testing.T) {
	p, skill := readRootFixture(t)
	got, err := p.ResolveRead(filepath.Join(skill, "SKILL.md"))
	assert.NoErr(t, err)
	assert.Eq(t, got, filepath.Join(skill, "SKILL.md"))
	_, err = p.Resolve(filepath.Join(skill, "SKILL.md")) // mutating tools stay confined
	assert.Contains(t, err.Error(), "path outside project root")
}

func TestResolveRead_DenyFloorAppliesInReadRoot(t *testing.T) {
	p, skill := readRootFixture(t)
	_, err := p.ResolveRead(filepath.Join(skill, ".env"))
	if err == nil {
		t.Fatal("expected .env in a read root to be denied")
	}
	assert.Contains(t, err.Error(), "denied")
}

func TestResolveRead_RefusesOutsideAndRelativeEscapes(t *testing.T) {
	p, skill := readRootFixture(t)
	other := realpath(t, t.TempDir())
	assert.NoErr(t, os.WriteFile(filepath.Join(other, "x"), []byte("x"), 0o644))
	paths := []string{filepath.Join(other, "x"), "../x"}
	if err := os.Symlink(filepath.Join(other, "x"), filepath.Join(skill, "link")); err == nil {
		paths = append(paths, filepath.Join(skill, "link"))
	}
	for _, path := range paths {
		if _, err := p.ResolveRead(path); err == nil {
			t.Fatalf("ResolveRead(%q): expected error", path)
		}
	}
}

func TestReadScope_RootsDenyAtReadRoot(t *testing.T) {
	p, skill := readRootFixture(t)
	assert.Eq(t, p.ReadScope(filepath.Join(skill, "SKILL.md")).Root, skill)
	assert.Eq(t, p.ReadScope(filepath.Join(p.Root, "a.go")).Root, p.Root)
}

func TestResolveRead_ExpandsTildeToHome(t *testing.T) {
	home := realpath(t, t.TempDir())
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	skill := filepath.Join(home, ".agents", "skills", "a")
	assert.NoErr(t, os.MkdirAll(skill, 0o755))
	assert.NoErr(t, os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("x"), 0o644))
	p := ExecPolicy{Root: realpath(t, t.TempDir()), ReadRoots: []string{skill}}
	got, err := p.ResolveRead("~/.agents/skills/a/SKILL.md")
	assert.NoErr(t, err)
	assert.Eq(t, got, filepath.Join(skill, "SKILL.md"))
	if _, err := p.ResolveRead("~/elsewhere"); err == nil {
		t.Fatal("~ reached outside the read roots")
	}
}
