package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/providers"
)

// profileFixture is a fake $HOME holding ~/.jas/profiles/{ds,other} with a
// demo skill each, plus the profile's own config and AGENTS.md beside skills/.
type profileFixture struct {
	home, jas, skills, demo, otherSkill string
}

func newProfileFixture(t *testing.T, jasHome string) profileFixture {
	t.Helper()
	f := profileFixture{jas: jasHome}
	f.skills = filepath.Join(jasHome, "profiles", "ds", "skills")
	f.demo = filepath.Join(f.skills, "demo")
	f.otherSkill = filepath.Join(jasHome, "profiles", "other", "skills", "x")
	writeFile(t, filepath.Join(f.demo, "SKILL.md"), "skill")
	writeFile(t, filepath.Join(f.demo, ".env"), "K=v")
	writeFile(t, filepath.Join(jasHome, "profiles", "ds", "config.json"), "{}")
	writeFile(t, filepath.Join(jasHome, "profiles", "ds", "AGENTS.md"), "x")
	writeFile(t, filepath.Join(jasHome, "auth.json"), "{}")
	writeFile(t, filepath.Join(f.otherSkill, "SKILL.md"), "skill")
	return f
}

func homeProfileFixture(t *testing.T) profileFixture {
	t.Helper()
	home := realpath(t, t.TempDir())
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	f := newProfileFixture(t, filepath.Join(home, ".jas"))
	f.home = home
	return f
}

// policy lists the active skill first, then the other profile's skill and the
// whole jas home as read roots: the worst case, so refusals come from the
// deny floor and not from confinement.
func (f profileFixture) policy(t *testing.T) ExecPolicy {
	return ExecPolicy{
		Root:          realpath(t, t.TempDir()),
		ReadRoots:     []string{f.demo, f.otherSkill, f.jas},
		ProfileSkills: f.skills,
	}
}

func TestResolveRead_ProfileSkillAllowed(t *testing.T) {
	f := homeProfileFixture(t)
	p := ExecPolicy{Root: realpath(t, t.TempDir()), ReadRoots: []string{f.demo}, ProfileSkills: f.skills}
	got, err := p.ResolveRead("~/.jas/profiles/ds/skills/demo/SKILL.md")
	assert.NoErr(t, err)
	assert.Eq(t, got, filepath.Join(f.demo, "SKILL.md"))
	p.ProfileSkills = "" // no active profile: the floor stays whole
	if _, err := p.ResolveRead("~/.jas/profiles/ds/skills/demo/SKILL.md"); err == nil {
		t.Fatal("empty ProfileSkills lifted the floor")
	}
}

func TestResolveRead_ProfileSkillRefusals(t *testing.T) {
	f := homeProfileFixture(t)
	p := f.policy(t)
	paths := []string{
		"~/.jas/profiles/ds/skills/demo/.env",
		"~/.jas/profiles/ds/config.json",
		"~/.jas/profiles/ds/AGENTS.md",
		"~/.jas/auth.json",
		"~/.jas/profiles/other/skills/x/SKILL.md",
		"~/.jas/profiles/ds/skills/demo/../../config.json",
		filepath.Join(f.demo, "..", "..", "config.json"),
	}
	if err := os.Symlink(filepath.Join(f.jas, "auth.json"), filepath.Join(f.demo, "creds")); err == nil {
		paths = append(paths, "~/.jas/profiles/ds/skills/demo/creds")
	} else {
		t.Logf("symlink unsupported, skipping creds case: %v", err)
	}
	for _, path := range paths {
		_, err := p.ResolveRead(path)
		if err == nil {
			t.Fatalf("ResolveRead(%q): expected denial", path)
		}
		assert.Contains(t, err.Error(), "path denied by policy")
	}
}

func TestReadScope_ProfileSymlinkStillDenied(t *testing.T) {
	f := homeProfileFixture(t)
	link := filepath.Join(f.demo, "creds")
	if err := os.Symlink(filepath.Join(f.jas, "auth.json"), link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	scope := f.policy(t).ReadScope(f.demo)
	if !scope.Denied(link) {
		t.Fatal("symlink to auth.json inside the profile skills was not denied")
	}
	if !f.policy(t).ProfileScope().Denied(link) {
		t.Fatal("ProfileScope: symlink to auth.json was not denied")
	}
}

func TestResolve_ProfileSkillWriteAllowed(t *testing.T) {
	f := homeProfileFixture(t)
	p := ExecPolicy{Root: realpath(t, t.TempDir()), ReadRoots: []string{f.demo}, ProfileSkills: f.skills}
	skill := filepath.Join(f.demo, "SKILL.md")
	for path, want := range map[string]string{
		skill: skill,
		"~/.jas/profiles/ds/skills/demo/SKILL.md":  skill,
		"~/.jas/profiles/ds/skills/fresh/SKILL.md": filepath.Join(f.skills, "fresh", "SKILL.md"),
	} {
		got, err := p.Resolve(path)
		assert.NoErr(t, err)
		assert.Eq(t, got, want)
		if !p.InProfileSkills(got) {
			t.Fatalf("InProfileSkills(%q) = false", got)
		}
	}
	if p.InProfileSkills(filepath.Join(p.Root, "x.go")) {
		t.Fatal("InProfileSkills held for a project file")
	}
	if !p.Denied(skill) {
		t.Fatal("base policy Denied lifted the floor")
	}
	p.ProfileSkills = "" // no active profile: the floor stays whole
	if _, err := p.Resolve(skill); err == nil {
		t.Fatal("empty ProfileSkills allowed a write in the jas home")
	}
	p.ProfileSkills, p.Writes = f.skills, []WriteRule{{All: []string{"**"}}}
	if full, err := p.Resolve(skill); err == nil && p.WriteAllowed(full) {
		t.Fatal("a write-limited policy (farm worker) may write the profile skills")
	}
}

func TestResolve_ProfileSkillWriteRefusals(t *testing.T) {
	f := homeProfileFixture(t)
	p := f.policy(t)
	paths := []string{
		"~/.jas/profiles/ds/skills/demo/.env",
		"~/.jas/profiles/ds/skills/fresh/.env",
		"~/.jas/profiles/ds/skills/fresh/secrets/key",
		"~/.jas/profiles/ds/config.json",
		"~/.jas/profiles/ds/AGENTS.md",
		"~/.jas/auth.json",
		"~/.jas/trust.json",
		"~/.jas/profiles/other/skills/x/SKILL.md",
		"~/.jas/profiles/ds/skills/demo/../../config.json",
		filepath.Join(f.demo, "..", "..", "config.json"),
	}
	if err := os.Symlink(filepath.Join(f.jas, "auth.json"), filepath.Join(f.demo, "creds")); err == nil {
		paths = append(paths, "~/.jas/profiles/ds/skills/demo/creds")
	} else {
		t.Logf("symlink unsupported, skipping creds case: %v", err)
	}
	if err := os.Symlink(f.jas, filepath.Join(f.skills, "up")); err == nil {
		paths = append(paths, "~/.jas/profiles/ds/skills/up/auth.json", "~/.jas/profiles/ds/skills/up/new.json")
	} else {
		t.Logf("symlink unsupported, skipping up case: %v", err)
	}
	for _, path := range paths {
		if got, err := p.Resolve(path); err == nil {
			t.Fatalf("Resolve(%q) = %q: expected refusal", path, got)
		}
	}
	if _, err := p.Resolve("~/.jas/profiles/ds/skills/demo/.env"); err != nil {
		assert.Contains(t, err.Error(), "path denied by policy")
	}
}

func TestResolve_ProfileSkillsDirMissing(t *testing.T) {
	home := realpath(t, t.TempDir())
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	jas := filepath.Join(home, ".jas")
	writeFile(t, filepath.Join(jas, "auth.json"), "{}")
	skills := filepath.Join(jas, "profiles", "ds", "skills")
	p := ExecPolicy{Root: realpath(t, t.TempDir()), ProfileSkills: skills}
	got, err := p.Resolve("~/.jas/profiles/ds/skills/first/SKILL.md")
	assert.NoErr(t, err) // the first personal skill creates skills/
	assert.Eq(t, got, filepath.Join(skills, "first", "SKILL.md"))
	if _, err := p.ResolveRead("~/.jas/auth.json"); err == nil {
		t.Fatal("missing skills dir lifted reads of auth.json")
	}
	if _, err := p.Resolve("~/.jas/profiles/ds/config.json"); err == nil {
		t.Fatal("missing skills dir allowed a write beside it")
	}
}

func TestResolve_ProfileSkillsLinkedIntoHomeNoWrite(t *testing.T) {
	home := realpath(t, t.TempDir())
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	jas := filepath.Join(home, ".jas")
	writeFile(t, filepath.Join(jas, "auth.json"), "{}")
	assert.NoErr(t, os.MkdirAll(filepath.Join(jas, "profiles", "ds"), 0o755))
	link := filepath.Join(jas, "profiles", "ds", "skills")
	if err := os.Symlink(jas, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	p := ExecPolicy{Root: realpath(t, t.TempDir()), ProfileSkills: link}
	for _, path := range []string{"~/.jas/profiles/ds/skills/auth.json", "~/.jas/profiles/ds/skills/x/SKILL.md"} {
		if _, err := p.Resolve(path); err == nil {
			t.Fatalf("Resolve(%q): a skills link into the jas home allowed a write", path)
		}
	}
}

func TestDeniedToken_ProfileSkillDenied(t *testing.T) {
	f := homeProfileFixture(t)
	p := ExecPolicy{Root: realpath(t, t.TempDir()), ReadRoots: []string{f.demo}, ProfileSkills: f.skills}
	if !p.DeniedToken("~/.jas/profiles/ds/skills/demo/SKILL.md") {
		t.Fatal("shell filter lifted the floor")
	}
}

func TestResolveRead_ProfileSkillMovedHome(t *testing.T) {
	t.Setenv("HOME", realpath(t, t.TempDir()))
	jas := filepath.Join(realpath(t, t.TempDir()), "jas-home")
	f := newProfileFixture(t, jas)
	homeDeny := filepath.ToSlash(jas) + "/**"
	p := f.policy(t)
	p.HomeDeny, p.Deny = homeDeny, []string{homeDeny}
	got, err := p.ResolveRead(filepath.Join(f.demo, "SKILL.md"))
	assert.NoErr(t, err)
	assert.Eq(t, got, filepath.Join(f.demo, "SKILL.md"))
	for _, path := range []string{
		filepath.Join(f.demo, ".env"),
		filepath.Join(jas, "profiles", "ds", "config.json"),
		filepath.Join(jas, "auth.json"),
		filepath.Join(f.otherSkill, "SKILL.md"),
	} {
		_, err := p.ResolveRead(path)
		if err == nil {
			t.Fatalf("ResolveRead(%q): expected denial", path)
		}
		assert.Contains(t, err.Error(), "path denied by policy")
	}
	p.HomeDeny = "" // a pattern the lift was not told about stays in force
	if _, err := p.ResolveRead(filepath.Join(f.demo, "SKILL.md")); err == nil {
		t.Fatal("lift dropped a Deny pattern other than HomeDeny")
	}
}

func TestReadScope_ProfileSkillWalk(t *testing.T) {
	f := homeProfileFixture(t)
	p := f.policy(t)
	match := p.ReadScope(f.demo).DenyMatcher()
	if match(filepath.Join(f.demo, "SKILL.md")) {
		t.Fatal("SKILL.md denied in the profile skill walk")
	}
	if !match(filepath.Join(f.demo, ".env")) {
		t.Fatal(".env allowed in the profile skill walk")
	}
	if !match(filepath.Join(f.jas, "auth.json")) {
		t.Fatal("auth.json allowed by a lifted matcher")
	}
	ps := p.ProfileScope()
	assert.Eq(t, ps.Root, f.skills)
	if ps.Denied(filepath.Join(f.demo, "SKILL.md")) {
		t.Fatal("ProfileScope denied the active profile's skill")
	}
	if !ps.Denied(filepath.Join(f.jas, "profiles", "ds", "config.json")) {
		t.Fatal("ProfileScope allowed the profile config")
	}
}

func TestProfileScope_ShellDenied(t *testing.T) {
	f := homeProfileFixture(t)
	ps := f.policy(t).ProfileScope()
	skill := filepath.Join(f.demo, "SKILL.md")
	if _, err := ps.Resolve(filepath.Join(f.demo, ".env")); err == nil {
		t.Fatal("ProfileScope().Resolve accepted a write to a skill's .env")
	}
	if !ps.DeniedToken(skill) {
		t.Fatal("ProfileScope().DeniedToken lifted the floor")
	}
	got, err := ps.ResolveRead(skill) // reads through the scope still work
	assert.NoErr(t, err)
	assert.Eq(t, got, skill)
}

func TestResolveRead_ProfileSkillProjectIsHome(t *testing.T) {
	f := homeProfileFixture(t)
	writeFile(t, filepath.Join(f.demo, "notes.md"), "x")
	p := ExecPolicy{
		Root:          f.home,
		ReadRoots:     []string{f.demo},
		ProfileSkills: f.skills,
		Deny:          []string{".jas/profiles/ds/skills/demo/notes.md"}, // project-relative
	}
	skill := filepath.Join(f.demo, "SKILL.md")
	for _, path := range []string{"~/.jas/profiles/ds/skills/demo/SKILL.md", ".jas/profiles/ds/skills/demo/SKILL.md"} {
		got, err := p.ResolveRead(path)
		assert.NoErr(t, err)
		assert.Eq(t, got, skill)
	}
	for _, path := range []string{
		"~/.jas/profiles/ds/skills/demo/.env",
		"~/.jas/profiles/ds/skills/demo/notes.md",
		"~/.jas/profiles/ds/config.json",
		"~/.jas/auth.json",
		".jas/profiles/ds/skills/demo/../../config.json",
	} {
		_, err := p.ResolveRead(path)
		if err == nil {
			t.Fatalf("ResolveRead(%q): expected denial", path)
		}
		assert.Contains(t, err.Error(), "path denied by policy")
	}
	match := p.ReadScope(f.demo).DenyMatcher()
	if match(skill) || !match(filepath.Join(f.demo, ".env")) || !match(filepath.Join(f.demo, "notes.md")) {
		t.Fatal("walk under the profile skill: want SKILL.md allowed, .env and notes.md denied")
	}
	if !p.ReadScope(f.home).DenyMatcher()(skill) {
		t.Fatal("a walk from the project root entered the jas home")
	}
	got, err := p.Resolve(skill)
	assert.NoErr(t, err)
	assert.Eq(t, got, skill)
	for _, path := range []string{".jas/profiles/ds/skills/demo/notes.md", ".jas/auth.json", ".jas/profiles/ds/config.json"} {
		if _, err := p.Resolve(path); err == nil {
			t.Fatalf("Resolve(%q): expected refusal", path)
		}
	}
}

func TestProfileSkills_SymlinkToHomeNoLift(t *testing.T) {
	home := realpath(t, t.TempDir())
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	jas := filepath.Join(home, ".jas")
	writeFile(t, filepath.Join(jas, "auth.json"), "{}")
	assert.NoErr(t, os.MkdirAll(filepath.Join(jas, "profiles", "ds"), 0o755))
	link := filepath.Join(jas, "profiles", "ds", "skills")
	if err := os.Symlink(jas, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	auth := filepath.Join(jas, "auth.json")
	for _, skills := range []string{link, jas} { // as given, and as a caller would resolve it
		p := ExecPolicy{Root: realpath(t, t.TempDir()), ReadRoots: []string{jas}, ProfileSkills: skills}
		if !p.ProfileScope().Denied(auth) {
			t.Fatalf("ProfileSkills=%s: ProfileScope allowed auth.json", skills)
		}
		if !p.ProfileScope().Denied(filepath.Join(link, "auth.json")) {
			t.Fatalf("ProfileSkills=%s: ProfileScope allowed auth.json through the link", skills)
		}
		if _, err := p.ResolveRead("~/.jas/auth.json"); err == nil {
			t.Fatalf("ProfileSkills=%s: ResolveRead allowed auth.json", skills)
		}
		if _, err := p.ResolveRead("~/.jas/profiles/ds/skills/auth.json"); err == nil {
			t.Fatalf("ProfileSkills=%s: ResolveRead allowed auth.json through the link", skills)
		}
	}
}

func TestExec_ProfileSkillWriteMustAsk(t *testing.T) {
	f := homeProfileFixture(t)
	policy := ExecPolicy{Root: realpath(t, t.TempDir()), ProfileSkills: f.skills}
	for path, want := range map[string]bool{
		"~/.jas/profiles/ds/skills/demo/SKILL.md": true,
		"notes.md": false,
	} {
		for _, name := range []string{"write", "edit"} {
			var asked bool
			gate := gateFunc(func(tl Tool) (Decision, error) {
				asked = MustAsk(tl)
				assert.Eq(t, tl.Name(), name)
				return Granted, nil
			})
			e := newExec(&fakeLog{}, gate, &fakeTool{name: name, kind: Mutating})
			e.Policy = policy
			in, _ := json.Marshal(map[string]string{"path": path})
			c := call("1", name)
			c.Input = in
			_, err := e.Run(context.Background(), []providers.ContentBlock{c})
			assert.NoErr(t, err)
			if asked != want {
				t.Errorf("%s %s: MustAsk = %v, want %v", name, path, asked, want)
			}
		}
	}
}

func TestGated_MustAskOverridesLevel(t *testing.T) {
	w := &fakeTool{name: "write", kind: Mutating}
	for _, level := range []Level{Never, ShellOnly, AllMutating} {
		if !Gated(level, mustAsk{w}) {
			t.Errorf("level %v: a must-ask write was not gated", level)
		}
	}
	if MustAsk(w) {
		t.Fatal("plain tool reported MustAsk")
	}
	a := &Approver{Level: Never}
	d, err := a.Allow(context.Background(), mustAsk{w}, json.RawMessage(`{}`), nil)
	assert.NoErr(t, err)
	assert.Eq(t, d, Unavailable) // no terminal: refused, never auto-granted
}
