package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
)

// inheritFixture is a moved jas home (JAS_HOME outside $HOME) holding the
// active profile c, its parent p and an unrelated profile other, each with
// a skill, plus p's memory and config.
type inheritFixture struct {
	home, own, parent, other string
}

func newInheritFixture(t *testing.T) inheritFixture {
	t.Helper()
	userHome := realpath(t, t.TempDir())
	t.Setenv("HOME", userHome)
	t.Setenv("USERPROFILE", userHome)
	home := realpath(t, t.TempDir())
	f := inheritFixture{
		home:   home,
		own:    filepath.Join(home, "profiles", "c", "skills"),
		parent: filepath.Join(home, "profiles", "p", "skills"),
		other:  filepath.Join(home, "profiles", "other", "skills"),
	}
	for _, dir := range []string{f.own, f.parent, f.other} {
		writeFile(t, filepath.Join(dir, "x", "SKILL.md"), "skill")
	}
	writeFile(t, filepath.Join(home, "profiles", "p", "memory", "MEMORY.md"), "- note\n")
	writeFile(t, filepath.Join(home, "profiles", "p", "config.json"), "{}")
	writeFile(t, filepath.Join(home, "auth.json"), "{}")
	return f
}

// policy lists the whole jas home as a read root: the worst case, so
// refusals come from the deny floor and not from confinement.
func (f inheritFixture) policy(t *testing.T) ExecPolicy {
	deny := filepath.ToSlash(f.home) + "/**"
	return ExecPolicy{
		Root:            realpath(t, t.TempDir()),
		ReadRoots:       []string{f.home},
		Deny:            []string{deny},
		HomeDeny:        deny,
		ProfileSkills:   f.own,
		InheritedSkills: []string{f.parent},
	}
}

func TestPolicy_InheritedSkillsReadable(t *testing.T) {
	f := newInheritFixture(t)
	p := f.policy(t)
	skill := filepath.Join(f.parent, "x", "SKILL.md")
	if p.ProfileScope().Denied(skill) {
		t.Fatal("inherited skill denied under ProfileScope")
	}
	got, err := p.ResolveRead(skill)
	assert.NoErr(t, err)
	assert.Eq(t, got, skill)
	assert.Eq(t, len(p.InheritedSkillsIgnored()), 0)

	// Without an own skills folder the scope roots at the parent's.
	p.ProfileSkills = ""
	if p.ProfileScope().Denied(skill) {
		t.Fatal("inherited skill denied with no own skills")
	}
	p.InheritedSkills = nil // no chain: the floor stays whole
	if !p.ProfileScope().Denied(skill) {
		t.Fatal("empty InheritedSkills lifted the floor")
	}
	if _, err := p.ResolveRead(skill); err == nil {
		t.Fatal("empty InheritedSkills: ResolveRead allowed")
	}
}

func TestPolicy_InheritedMemoryStaysDenied(t *testing.T) {
	f := newInheritFixture(t)
	p := f.policy(t)
	for _, path := range []string{
		filepath.Join(f.home, "profiles", "p", "memory", "MEMORY.md"),
		filepath.Join(f.home, "profiles", "p", "config.json"),
		filepath.Join(f.home, "auth.json"),
		filepath.Join(f.parent, "..", "config.json"),
	} {
		if !p.ProfileScope().Denied(path) {
			t.Fatalf("%s allowed under ProfileScope", path)
		}
		if _, err := p.ResolveRead(path); err == nil {
			t.Fatalf("ResolveRead(%s) allowed", path)
		}
		if _, err := p.Resolve(path); err == nil {
			t.Fatalf("Resolve(%s) allowed", path)
		}
	}
}

func TestPolicy_NonChainProfileSkillsDenied(t *testing.T) {
	f := newInheritFixture(t)
	p := f.policy(t)
	path := filepath.Join(f.other, "x", "SKILL.md")
	if !p.ProfileScope().Denied(path) {
		t.Fatal("non-chain profile skill allowed under ProfileScope")
	}
	if _, err := p.ResolveRead(path); err == nil {
		t.Fatal("ResolveRead of a non-chain profile skill allowed")
	}
	if p.InProfileSkills(path) {
		t.Fatal("non-chain profile skill counted as profile skills")
	}
}

func TestPolicy_WriteIntoInheritedSkills(t *testing.T) {
	f := newInheritFixture(t)
	p := f.policy(t)
	skill := filepath.Join(f.parent, "x", "SKILL.md")
	got, err := p.Resolve(skill)
	assert.NoErr(t, err)
	assert.Eq(t, got, skill)
	fresh, err := p.Resolve(filepath.Join(f.parent, "fresh", "SKILL.md"))
	assert.NoErr(t, err)
	if !p.InProfileSkills(got) || !p.InProfileSkills(fresh) {
		t.Fatal("write into inherited skills not reported as profile skills")
	}
	assert.Eq(t, p.SharedSkillsOwner(got), "p")
	own := filepath.Join(f.own, "x", "SKILL.md")
	if !p.InProfileSkills(own) {
		t.Fatal("own skills lost")
	}
	assert.Eq(t, p.SharedSkillsOwner(own), "")
	assert.Eq(t, p.SharedSkillsOwner(filepath.Join(f.other, "x", "SKILL.md")), "")
	writeFile(t, filepath.Join(f.parent, "x", ".env"), "K=v")
	if _, err := p.Resolve(filepath.Join(f.parent, "x", ".env")); err == nil {
		t.Fatal("inherited skill's .env writable")
	}
}

func TestPolicy_InheritedSkillsLinkIntoHomeIgnored(t *testing.T) {
	f := newInheritFixture(t)
	assert.NoErr(t, os.RemoveAll(f.parent))
	symlinkOrSkip(t, f.other, f.parent)
	p := f.policy(t)
	why := p.InheritedSkillsIgnored()
	assert.Eq(t, len(why), 1)
	assert.Contains(t, why[0], f.parent)
	assert.Contains(t, why[0], "ignored")
	assert.Eq(t, p.ProfileSkillsIgnored(), "")
	for _, path := range []string{
		filepath.Join(f.parent, "x", "SKILL.md"),
		filepath.Join(f.other, "x", "SKILL.md"),
	} {
		if !p.ProfileScope().Denied(path) {
			t.Fatalf("%s allowed through a refused inherited link", path)
		}
		if _, err := p.ResolveRead(path); err == nil {
			t.Fatalf("ResolveRead(%s) allowed through a refused inherited link", path)
		}
		if p.InProfileSkills(path) {
			t.Fatalf("%s counted as profile skills", path)
		}
	}
	// The own skills stay lifted.
	if p.ProfileScope().Denied(filepath.Join(f.own, "x", "SKILL.md")) {
		t.Fatal("own skills lost with a refused inherited link")
	}
}

func TestPolicy_MemoryOverlapsInheritedSkills(t *testing.T) {
	f := newInheritFixture(t)
	// The parent's skills link out of the jas home (allowed), so only the
	// overlap check, not liftDir, can refuse memory linked onto them.
	out := filepath.Join(realpath(t, t.TempDir()), "dotskills")
	assert.NoErr(t, os.Rename(f.parent, out))
	symlinkOrSkip(t, out, f.parent)
	memory := filepath.Join(f.home, "profiles", "c", "memory")
	symlinkOrSkip(t, f.parent, memory)
	p := f.policy(t)
	p.ProfileMemory = memory
	assert.Eq(t, len(p.InheritedSkillsIgnored()), 0)
	assert.Contains(t, p.ProfileMemoryIgnored(), "ignored")
	assert.Contains(t, p.ProfileMemoryIgnored(), "overlaps the profile skills")
	if p.ProfileMemoryLifted() {
		t.Fatal("memory aliasing the inherited skills was lifted")
	}
	skill := filepath.Join(out, "x", "SKILL.md")
	if p.InProfileMemory(skill) {
		t.Fatal("an inherited skill counted as memory")
	}
	if !p.InProfileSkills(skill) {
		t.Fatal("the inherited skills lift was lost")
	}
}
