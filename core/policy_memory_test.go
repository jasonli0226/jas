package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
)

// memoryFixture is the profile fixture plus ~/.jas/profiles/ds/memory holding
// an index and one note.
type memoryFixture struct {
	profileFixture
	memory, note string
}

func homeMemoryFixture(t *testing.T) memoryFixture {
	t.Helper()
	f := memoryFixture{profileFixture: homeProfileFixture(t)}
	f.memory = filepath.Join(f.jas, "profiles", "ds", "memory")
	f.note = filepath.Join(f.memory, "user-role.md")
	writeFile(t, filepath.Join(f.memory, "MEMORY.md"), "- [[user-role]]\n")
	writeFile(t, f.note, "note")
	return f
}

// policy lists the memory dir as a read root plus the whole jas home: the
// worst case, so refusals come from the deny floor and not from confinement.
func (f memoryFixture) policy(t *testing.T) ExecPolicy {
	return ExecPolicy{
		Root:          realpath(t, t.TempDir()),
		ReadRoots:     []string{f.memory, f.jas},
		ProfileMemory: f.memory,
	}
}

func TestProfileMemory_WriteLifted(t *testing.T) {
	f := homeMemoryFixture(t)
	p := f.policy(t)
	for path, want := range map[string]string{
		filepath.Join(f.memory, "x.md"):                  filepath.Join(f.memory, "x.md"),
		"~/.jas/profiles/ds/memory/x.md":                 filepath.Join(f.memory, "x.md"),
		"~/.jas/profiles/ds/memory/projects/k/MEMORY.md": filepath.Join(f.memory, "projects", "k", "MEMORY.md"),
		"~/.jas/profiles/ds/memory/user-role.md":         f.note,
	} {
		got, err := p.Resolve(path)
		assert.NoErr(t, err)
		assert.Eq(t, got, want)
		if !p.InProfileMemory(got) {
			t.Fatalf("InProfileMemory(%q) = false", got)
		}
		if p.InProfileSkills(got) {
			t.Fatalf("InProfileSkills(%q) = true for a memory path", got)
		}
	}
	if p.InProfileMemory(filepath.Join(p.Root, "x.go")) {
		t.Fatal("InProfileMemory held for a project file")
	}
	if !p.Denied(f.note) {
		t.Fatal("base policy Denied lifted the floor")
	}
	p.ProfileMemory = "" // memory off: the floor stays whole
	if _, err := p.Resolve(filepath.Join(f.memory, "x.md")); err == nil {
		t.Fatal("empty ProfileMemory allowed a write in the jas home")
	}
	if p.InProfileMemory(f.note) {
		t.Fatal("empty ProfileMemory: InProfileMemory held")
	}
	p.ProfileMemory, p.Writes = f.memory, []WriteRule{{All: []string{"**"}}}
	if full, err := p.Resolve(f.note); err == nil && p.WriteAllowed(full) {
		t.Fatal("a write-limited policy (farm worker) may write the profile memory")
	}
}

func TestProfileMemory_ReadLifted(t *testing.T) {
	f := homeMemoryFixture(t)
	p := ExecPolicy{Root: realpath(t, t.TempDir()), ReadRoots: []string{f.memory}, ProfileMemory: f.memory}
	for _, path := range []string{f.note, "~/.jas/profiles/ds/memory/user-role.md"} {
		got, err := p.ResolveRead(path)
		assert.NoErr(t, err)
		assert.Eq(t, got, f.note)
	}
	match := p.ReadScope(f.memory).DenyMatcher()
	if match(f.note) || match(filepath.Join(f.memory, "MEMORY.md")) {
		t.Fatal("memory walk denied a note")
	}
	if !match(filepath.Join(f.jas, "auth.json")) {
		t.Fatal("auth.json allowed by a lifted memory matcher")
	}
	p.ProfileMemory = "" // memory off: the read root alone does not lift
	if _, err := p.ResolveRead(f.note); err == nil {
		t.Fatal("empty ProfileMemory lifted the floor for reads")
	}
}

func TestProfileMemory_EnvStillDenied(t *testing.T) {
	f := homeMemoryFixture(t)
	writeFile(t, filepath.Join(f.memory, ".env"), "K=v")
	p := f.policy(t)
	for _, path := range []string{
		"~/.jas/profiles/ds/memory/.env",
		"~/.jas/profiles/ds/memory/projects/k/.env",
		"~/.jas/profiles/ds/memory/secrets/key",
		"~/.jas/profiles/ds/memory/x.pem",
	} {
		if got, err := p.Resolve(path); err == nil {
			t.Fatalf("Resolve(%q) = %q: expected refusal", path, got)
		}
	}
	_, err := p.ResolveRead("~/.jas/profiles/ds/memory/.env")
	if err == nil {
		t.Fatal("ResolveRead of memory/.env allowed")
	}
	assert.Contains(t, err.Error(), "path denied by policy")
	if !p.ReadScope(f.memory).DenyMatcher()(filepath.Join(f.memory, ".env")) {
		t.Fatal(".env allowed in the memory walk")
	}
	for _, path := range []string{
		"~/.jas/profiles/ds/config.json",
		"~/.jas/profiles/ds/AGENTS.md",
		"~/.jas/auth.json",
		"~/.jas/profiles/ds/memory/../config.json",
		filepath.Join(f.memory, "..", "config.json"),
	} {
		if _, err := p.Resolve(path); err == nil {
			t.Fatalf("Resolve(%q): expected refusal", path)
		}
		if _, err := p.ResolveRead(path); err == nil {
			t.Fatalf("ResolveRead(%q): expected denial", path)
		}
	}
}

func TestProfileMemory_LinkToAuthDenied(t *testing.T) {
	f := homeMemoryFixture(t)
	link := filepath.Join(f.memory, "creds.md")
	symlinkOrSkip(t, filepath.Join(f.jas, "auth.json"), link)
	up := filepath.Join(f.memory, "up")
	symlinkOrSkip(t, f.jas, up)
	p := f.policy(t)
	for _, path := range []string{link, "~/.jas/profiles/ds/memory/creds.md", filepath.Join(up, "auth.json"), filepath.Join(up, "new.md")} {
		if _, err := p.Resolve(path); err == nil {
			t.Fatalf("Resolve(%q): a link out of memory allowed a write", path)
		}
	}
	for _, path := range []string{link, filepath.Join(up, "auth.json")} {
		if _, err := p.ResolveRead(path); err == nil {
			t.Fatalf("ResolveRead(%q): a link out of memory allowed a read", path)
		}
		if !p.ReadScope(f.memory).Denied(path) {
			t.Fatalf("ReadScope(memory).Denied(%q) = false", path)
		}
	}
}

func TestProfileMemory_LinkToJasHomeIgnored(t *testing.T) {
	f := homeMemoryFixture(t)
	for _, target := range []string{f.jas, filepath.Join(f.jas, "profiles"), filepath.Join(f.jas, "profiles", "other"), f.home} {
		assert.NoErr(t, os.RemoveAll(f.memory))
		symlinkOrSkip(t, target, f.memory)
		p := ExecPolicy{Root: realpath(t, t.TempDir()), ReadRoots: []string{f.jas}, ProfileMemory: f.memory}
		why := p.ProfileMemoryIgnored()
		assert.Contains(t, why, f.memory)
		assert.Contains(t, why, "ignored")
		for _, path := range []string{filepath.Join(f.memory, "auth.json"), filepath.Join(f.memory, "x.md")} {
			if _, err := p.Resolve(path); err == nil {
				t.Fatalf("memory -> %s: Resolve(%s) allowed", target, path)
			}
			if p.InProfileMemory(path) {
				t.Fatalf("memory -> %s: InProfileMemory(%s) held", target, path)
			}
		}
		for _, path := range []string{filepath.Join(f.memory, "auth.json"), filepath.Join(f.jas, "auth.json")} {
			if _, err := p.ResolveRead(path); err == nil {
				t.Fatalf("memory -> %s: ResolveRead(%s) allowed", target, path)
			}
		}
	}
}

func TestProfileMemory_LinkToSkillsIgnored(t *testing.T) {
	f := homeMemoryFixture(t)
	assert.NoErr(t, os.RemoveAll(f.memory))
	symlinkOrSkip(t, f.skills, f.memory)
	p := ExecPolicy{Root: realpath(t, t.TempDir()), ProfileSkills: f.skills, ProfileMemory: f.memory}
	assert.Contains(t, p.ProfileMemoryIgnored(), "ignored")
	assert.Eq(t, p.ProfileSkillsIgnored(), "")
	skill := filepath.Join(f.demo, "SKILL.md")
	if p.InProfileMemory(skill) {
		t.Fatal("memory -> skills: a skill counted as memory")
	}
	if !p.InProfileSkills(skill) {
		t.Fatal("memory -> skills: the skills lift was lost")
	}

	// Both linked out of the jas home onto one tree: memory may not alias
	// the skills, so the memory lift is refused and the skills lift stays.
	out := realpath(t, t.TempDir())
	writeFile(t, filepath.Join(out, "skills", "demo", "SKILL.md"), "skill")
	assert.NoErr(t, os.RemoveAll(f.skills))
	symlinkOrSkip(t, filepath.Join(out, "skills"), f.skills)
	for _, target := range []string{filepath.Join(out, "skills"), out, filepath.Join(out, "skills", "demo")} {
		assert.NoErr(t, os.RemoveAll(f.memory))
		symlinkOrSkip(t, target, f.memory)
		p := ExecPolicy{Root: realpath(t, t.TempDir()), ProfileSkills: f.skills, ProfileMemory: f.memory}
		assert.Contains(t, p.ProfileMemoryIgnored(), "ignored")
		outSkill := filepath.Join(out, "skills", "demo", "SKILL.md")
		if p.InProfileMemory(outSkill) {
			t.Fatalf("memory -> %s: memory aliased the skills", target)
		}
		assert.Eq(t, p.ProfileSkillsIgnored(), "")
		if !p.InProfileSkills(outSkill) {
			t.Fatalf("memory -> %s: the skills lift was lost", target)
		}
	}
	// Apart, both outside links lift.
	apart := realpath(t, t.TempDir())
	assert.NoErr(t, os.RemoveAll(f.memory))
	symlinkOrSkip(t, apart, f.memory)
	p = ExecPolicy{Root: realpath(t, t.TempDir()), ProfileSkills: f.skills, ProfileMemory: f.memory}
	assert.Eq(t, p.ProfileMemoryIgnored(), "")
	assert.Eq(t, p.ProfileSkillsIgnored(), "")
	got, err := p.Resolve(filepath.Join(f.memory, "x.md"))
	assert.NoErr(t, err)
	assert.Eq(t, got, filepath.Join(apart, "x.md"))
}

func TestProfileMemory_MissingDirLifted(t *testing.T) {
	home := realpath(t, t.TempDir())
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	jas := filepath.Join(home, ".jas")
	writeFile(t, filepath.Join(jas, "auth.json"), "{}")
	memory := filepath.Join(jas, "profiles", "ds", "memory")
	p := ExecPolicy{Root: realpath(t, t.TempDir()), ProfileMemory: memory}
	assert.Eq(t, p.ProfileMemoryIgnored(), "")
	got, err := p.Resolve("~/.jas/profiles/ds/memory/MEMORY.md")
	assert.NoErr(t, err) // the first note creates memory/
	assert.Eq(t, got, filepath.Join(memory, "MEMORY.md"))
	if !p.InProfileMemory(got) {
		t.Fatal("missing memory dir: InProfileMemory = false")
	}
	if _, err := p.ResolveRead("~/.jas/auth.json"); err == nil {
		t.Fatal("missing memory dir lifted reads of auth.json")
	}
	if _, err := p.Resolve("~/.jas/profiles/ds/config.json"); err == nil {
		t.Fatal("missing memory dir allowed a write beside it")
	}
}

func TestProfileMemory_SymlinkedHomeKeepsLift(t *testing.T) {
	t.Run("jas home", func(t *testing.T) {
		home := realpath(t, t.TempDir())
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		real := filepath.Join(realpath(t, t.TempDir()), "jasdata")
		writeFile(t, filepath.Join(real, "auth.json"), "{}")
		writeFile(t, filepath.Join(real, "profiles", "ds", "memory", "MEMORY.md"), "x")
		lexJas := filepath.Join(home, ".jas")
		symlinkOrSkip(t, real, lexJas)
		assertMemoryLifted(t, lexJas, real)
	})
	t.Run("home component", func(t *testing.T) {
		real := realpath(t, t.TempDir())
		linkHome := filepath.Join(realpath(t, t.TempDir()), "home")
		symlinkOrSkip(t, real, linkHome)
		t.Setenv("HOME", linkHome)
		t.Setenv("USERPROFILE", linkHome)
		writeFile(t, filepath.Join(real, ".jas", "auth.json"), "{}")
		writeFile(t, filepath.Join(real, ".jas", "profiles", "ds", "memory", "MEMORY.md"), "x")
		assertMemoryLifted(t, filepath.Join(linkHome, ".jas"), filepath.Join(real, ".jas"))
	})
}

// assertMemoryLifted checks that a note spelled through the lexical jas home
// lexJas (whose real location is realJas) is a lifted memory write and read,
// while auth.json stays denied.
func assertMemoryLifted(t *testing.T, lexJas, realJas string) {
	t.Helper()
	homeDeny := filepath.ToSlash(realJas) + "/**"
	realMemory := filepath.Join(realJas, "profiles", "ds", "memory")
	p := ExecPolicy{
		Root:          realpath(t, t.TempDir()),
		ReadRoots:     []string{realMemory},
		ProfileMemory: filepath.Join(lexJas, "profiles", "ds", "memory"),
		HomeDeny:      homeDeny,
		Deny:          []string{homeDeny},
	}
	assert.Eq(t, p.ProfileMemoryIgnored(), "")
	for _, path := range []string{
		filepath.Join(lexJas, "profiles", "ds", "memory", "x.md"),
		filepath.Join(realMemory, "x.md"),
		"~/.jas/profiles/ds/memory/x.md",
	} {
		got, err := p.Resolve(path)
		assert.NoErr(t, err)
		assert.Eq(t, got, filepath.Join(realMemory, "x.md"))
		if !p.InProfileMemory(got) {
			t.Fatalf("InProfileMemory(%q) = false", got)
		}
	}
	got, err := p.ResolveRead(filepath.Join(lexJas, "profiles", "ds", "memory", "MEMORY.md"))
	assert.NoErr(t, err)
	assert.Eq(t, got, filepath.Join(realMemory, "MEMORY.md"))
	for _, path := range []string{filepath.Join(lexJas, "auth.json"), filepath.Join(realJas, "auth.json")} {
		if _, err := p.Resolve(path); err == nil {
			t.Fatalf("Resolve(%q): expected refusal", path)
		}
		if _, err := p.ResolveRead(path); err == nil {
			t.Fatalf("ResolveRead(%q): expected denial", path)
		}
	}
}

func TestProfileMemory_SkillsUnaffected(t *testing.T) {
	f := homeMemoryFixture(t)
	p := ExecPolicy{
		Root:          realpath(t, t.TempDir()),
		ReadRoots:     []string{f.demo, f.memory},
		ProfileSkills: f.skills,
		ProfileMemory: f.memory,
	}
	skill := filepath.Join(f.demo, "SKILL.md")
	got, err := p.Resolve("~/.jas/profiles/ds/skills/demo/SKILL.md")
	assert.NoErr(t, err)
	assert.Eq(t, got, skill)
	if !p.InProfileSkills(got) || p.InProfileMemory(got) {
		t.Fatal("skill write: want InProfileSkills only")
	}
	got, err = p.Resolve("~/.jas/profiles/ds/memory/x.md")
	assert.NoErr(t, err)
	if p.InProfileSkills(got) || !p.InProfileMemory(got) {
		t.Fatal("memory write: want InProfileMemory only")
	}
	for _, path := range []string{skill, f.note} {
		if _, err := p.ResolveRead(path); err != nil {
			t.Fatalf("ResolveRead(%q): %v", path, err)
		}
	}
	for _, path := range []string{
		"~/.jas/profiles/ds/config.json",
		"~/.jas/auth.json",
		"~/.jas/profiles/ds/skills/demo/.env",
		"~/.jas/profiles/other/skills/x/SKILL.md",
	} {
		if _, err := p.Resolve(path); err == nil {
			t.Fatalf("Resolve(%q): expected refusal", path)
		}
		if _, err := p.ResolveRead(path); err == nil {
			t.Fatalf("ResolveRead(%q): expected denial", path)
		}
	}
	ps := p.ProfileScope()
	assert.Eq(t, ps.Root, f.skills)
	if ps.Denied(skill) {
		t.Fatal("ProfileScope denied the active profile's skill")
	}
	if !p.DeniedToken("~/.jas/profiles/ds/memory/x.md") {
		t.Fatal("shell filter lifted the floor for memory")
	}
}
