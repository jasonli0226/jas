package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
)

func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	assert.NoErr(t, os.MkdirAll(filepath.Dir(link), 0o755))
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
}

// symlinkPolicy is the policy setup builds for the lexical jas home lexJas
// (as spelled under $HOME) whose real location is f.jas: ProfileSkills as
// spelled, the resolved home deny pattern, and the discovered skill (and the
// worst-case extra read roots) resolved.
func symlinkPolicy(t *testing.T, f profileFixture, lexJas string) ExecPolicy {
	homeDeny := filepath.ToSlash(realpath(t, lexJas)) + "/**"
	var roots []string
	for _, r := range []string{f.demo, f.otherSkill, f.jas} {
		if rr, err := filepath.EvalSymlinks(r); err == nil {
			roots = append(roots, rr)
		}
	}
	return ExecPolicy{
		Root:          realpath(t, t.TempDir()),
		ReadRoots:     roots,
		ProfileSkills: filepath.Join(lexJas, "profiles", "ds", "skills"),
		HomeDeny:      homeDeny,
		Deny:          []string{homeDeny},
	}
}

// assertProfileLifted checks that the active skill loads and reads through
// both spellings while every other jas home file stays denied.
func assertProfileLifted(t *testing.T, p ExecPolicy, lexJas string, f profileFixture) {
	t.Helper()
	assert.Eq(t, p.ProfileSkillsIgnored(), "")
	lexSkill := filepath.Join(lexJas, "profiles", "ds", "skills", "demo", "SKILL.md")
	realSkill := realpath(t, lexSkill)
	ps := p.ProfileScope()
	if ps.Denied(lexSkill) || ps.Denied(realSkill) {
		t.Fatal("ProfileScope denied the active profile's skill")
	}
	for _, path := range []string{lexSkill, realSkill} {
		got, err := p.ResolveRead(path)
		assert.NoErr(t, err)
		assert.Eq(t, got, realSkill)
	}
	if strings.HasPrefix(lexJas, os.Getenv("HOME")+string(filepath.Separator)) {
		rel, _ := filepath.Rel(os.Getenv("HOME"), lexSkill)
		got, err := p.ResolveRead("~/" + filepath.ToSlash(rel))
		assert.NoErr(t, err)
		assert.Eq(t, got, realSkill)
	}
	assertProfileDenied(t, p, lexJas, f)
}

// assertProfileDenied checks the files that stay denied whatever the lift.
func assertProfileDenied(t *testing.T, p ExecPolicy, lexJas string, f profileFixture) {
	t.Helper()
	lexDemo := filepath.Join(lexJas, "profiles", "ds", "skills", "demo")
	link := filepath.Join(f.demo, "creds")
	if _, err := os.Lstat(link); err != nil {
		symlinkOrSkip(t, filepath.Join(f.jas, "auth.json"), link)
	}
	for _, path := range []string{
		filepath.Join(lexJas, "auth.json"),
		filepath.Join(f.jas, "auth.json"),
		filepath.Join(lexJas, "profiles", "ds", "config.json"),
		filepath.Join(f.jas, "profiles", "ds", "config.json"),
		filepath.Join(lexJas, "profiles", "other", "skills", "x", "SKILL.md"),
		filepath.Join(f.otherSkill, "SKILL.md"),
		filepath.Join(lexDemo, ".env"),
		filepath.Join(lexDemo, "creds"),
		filepath.Join(lexDemo, "..", "..", "config.json"),
	} {
		if _, err := p.ResolveRead(path); err == nil {
			t.Fatalf("ResolveRead(%q): expected denial", path)
		}
		if !p.ProfileScope().Denied(path) {
			t.Fatalf("ProfileScope().Denied(%q) = false", path)
		}
	}
	if !p.ProfileScope().Denied(filepath.Join(f.demo, "creds")) {
		t.Fatal("symlink into the jas home allowed")
	}
}

func TestProfileLift_SymlinkedJasHome(t *testing.T) {
	home := realpath(t, t.TempDir())
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	f := newProfileFixture(t, filepath.Join(realpath(t, t.TempDir()), "jasdata"))
	lexJas := filepath.Join(home, ".jas")
	symlinkOrSkip(t, f.jas, lexJas)
	assertProfileLifted(t, symlinkPolicy(t, f, lexJas), lexJas, f)
}

func TestProfileLift_SymlinkedHomeComponent(t *testing.T) {
	real := realpath(t, t.TempDir())
	linkHome := filepath.Join(realpath(t, t.TempDir()), "home")
	symlinkOrSkip(t, real, linkHome)
	t.Setenv("HOME", linkHome)
	t.Setenv("USERPROFILE", linkHome)
	f := newProfileFixture(t, filepath.Join(real, ".jas"))
	lexJas := filepath.Join(linkHome, ".jas")
	assertProfileLifted(t, symlinkPolicy(t, f, lexJas), lexJas, f)
}

func TestProfileLift_SymlinkedProfileDir(t *testing.T) {
	home := realpath(t, t.TempDir())
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	jas := filepath.Join(home, ".jas")
	dotfiles := filepath.Join(realpath(t, t.TempDir()), "ds")
	writeFile(t, filepath.Join(dotfiles, "skills", "demo", "SKILL.md"), "skill")
	writeFile(t, filepath.Join(dotfiles, "skills", "demo", ".env"), "K=v")
	writeFile(t, filepath.Join(dotfiles, "config.json"), "{}")
	writeFile(t, filepath.Join(jas, "auth.json"), "{}")
	writeFile(t, filepath.Join(jas, "profiles", "other", "skills", "x", "SKILL.md"), "skill")
	symlinkOrSkip(t, dotfiles, filepath.Join(jas, "profiles", "ds"))
	f := profileFixture{home: home, jas: jas, skills: filepath.Join(dotfiles, "skills"),
		demo: filepath.Join(dotfiles, "skills", "demo"), otherSkill: filepath.Join(jas, "profiles", "other", "skills", "x")}
	assertProfileLifted(t, symlinkPolicy(t, f, jas), jas, f)
}

func TestProfileLift_SkillsLinkOutside(t *testing.T) {
	home := realpath(t, t.TempDir())
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	jas := filepath.Join(home, ".jas")
	f := newProfileFixture(t, jas)
	outside := filepath.Join(realpath(t, t.TempDir()), "dotskills")
	assert.NoErr(t, os.Rename(f.skills, outside))
	symlinkOrSkip(t, outside, f.skills)
	f.demo = filepath.Join(outside, "demo")
	assertProfileLifted(t, symlinkPolicy(t, f, jas), jas, f)
}

func TestProfileLift_SkillsLinkIntoJasHomeIgnored(t *testing.T) {
	home := realpath(t, t.TempDir())
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	jas := filepath.Join(home, ".jas")
	f := newProfileFixture(t, jas)
	assert.NoErr(t, os.RemoveAll(f.skills))
	for _, target := range []string{jas, filepath.Join(jas, "profiles"), filepath.Dir(f.otherSkill), home} {
		assert.NoErr(t, os.RemoveAll(f.skills))
		symlinkOrSkip(t, target, f.skills)
		p := symlinkPolicy(t, f, jas)
		why := p.ProfileSkillsIgnored()
		assert.Contains(t, why, f.skills)
		assert.Contains(t, why, "ignored")
		if p.profileSkillsDir() != "" {
			t.Fatalf("skills -> %s: lift on", target)
		}
		for _, path := range []string{
			filepath.Join(f.skills, "auth.json"),
			filepath.Join(jas, "auth.json"),
			filepath.Join(f.otherSkill, "SKILL.md"),
			filepath.Join(f.skills, "x", "SKILL.md"),
		} {
			if !p.ProfileScope().Denied(path) {
				t.Fatalf("skills -> %s: %s allowed", target, path)
			}
			if _, err := p.ResolveRead(path); err == nil {
				t.Fatalf("skills -> %s: ResolveRead(%s) allowed", target, path)
			}
		}
	}
}

func TestProfileLift_SkillsLinkIntoFloorHomeIgnored(t *testing.T) {
	home := realpath(t, t.TempDir())
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	writeFile(t, filepath.Join(home, ".jas", "auth.json"), "{}")
	f := newProfileFixture(t, filepath.Join(realpath(t, t.TempDir()), "ci-jas")) // JAS_HOME moved
	assert.NoErr(t, os.RemoveAll(f.skills))
	symlinkOrSkip(t, filepath.Join(home, ".jas"), f.skills)
	p := symlinkPolicy(t, f, f.jas)
	assert.Contains(t, p.ProfileSkillsIgnored(), "ignored")
	if !p.ProfileScope().Denied(filepath.Join(home, ".jas", "auth.json")) {
		t.Fatal("~/.jas/auth.json allowed through a moved home's skills link")
	}
}

func TestProfileLift_PlainAndMissingNoWarning(t *testing.T) {
	f := homeProfileFixture(t)
	assert.Eq(t, f.policy(t).ProfileSkillsIgnored(), "")
	p := ExecPolicy{ProfileSkills: filepath.Join(f.jas, "profiles", "nope", "skills")}
	assert.Eq(t, p.ProfileSkillsIgnored(), "")
}
