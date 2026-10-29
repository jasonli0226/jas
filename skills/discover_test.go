package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
)

func skillFile(t *testing.T, dir, name, frontmatter string) string {
	t.Helper()
	d := filepath.Join(dir, name)
	assert.NoErr(t, os.MkdirAll(d, 0o755))
	assert.NoErr(t, os.WriteFile(filepath.Join(d, "SKILL.md"), []byte("---\n"+frontmatter+"---\n# body\n"), 0o644))
	return d
}

func realDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	assert.NoErr(t, err)
	return d
}

func names(ss []Skill) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = s.Name
	}
	return out
}

func noDeny(string) bool { return false }

func TestDiscover_ProjectShadowsUser(t *testing.T) {
	proj, user := realDir(t), realDir(t)
	skillFile(t, proj, "pdf", "name: pdf\ndescription: project pdf\n")
	skillFile(t, user, "pdf", "name: pdf\ndescription: user pdf\n")
	skillFile(t, user, "git", "name: git\ndescription: user git\n")
	got, warns := Discover([]Source{{Dir: proj, Scope: Project}, {Dir: user, Scope: User}}, noDeny)
	assert.Eq(t, strings.Join(names(got), ","), "git,pdf")
	for _, s := range got {
		if s.Name == "pdf" {
			assert.Eq(t, s.Description, "project pdf")
			assert.Eq(t, s.Scope, Project)
			assert.Eq(t, s.Location, filepath.Join(proj, "pdf", "SKILL.md"))
			assert.Eq(t, s.Dir, filepath.Join(proj, "pdf"))
		}
	}
	assert.Eq(t, len(warns), 1)
	assert.Contains(t, warns[0], "shadowed")
}

func TestDiscover_LenientValidation(t *testing.T) {
	dir := realDir(t)
	skillFile(t, dir, "no-desc", "name: no-desc\n")
	skillFile(t, dir, "bad", "name: [unclosed\ndescription: \"x\n")
	skillFile(t, dir, "Mismatch", "name: Other_Name\ndescription: loads anyway\n")
	skillFile(t, dir, "unnamed", "description: falls back to dir name\n")
	got, warns := Discover([]Source{{Dir: dir, Scope: User}}, noDeny)
	assert.Eq(t, strings.Join(names(got), ","), "Other_Name,unnamed")
	joined := strings.Join(warns, "\n")
	assert.Contains(t, joined, "no-desc")
	assert.Contains(t, joined, "missing description")
	assert.Contains(t, joined, "bad")
	assert.Contains(t, joined, "does not match directory")
	assert.Contains(t, joined, "invalid name")
}

func TestDiscover_SkipsDeniedHiddenAndNonSkills(t *testing.T) {
	dir := realDir(t)
	skillFile(t, dir, "ok", "name: ok\ndescription: d\n")
	skillFile(t, dir, ".hidden", "name: hidden\ndescription: d\n")
	skillFile(t, dir, "secret", "name: secret\ndescription: d\n")
	assert.NoErr(t, os.MkdirAll(filepath.Join(dir, "empty"), 0o755))
	assert.NoErr(t, os.WriteFile(filepath.Join(dir, "loose.md"), []byte("x"), 0o644))
	deny := func(p string) bool { return strings.Contains(p, "secret") }
	got, warns := Discover([]Source{{Dir: dir, Scope: User}, {Dir: filepath.Join(dir, "missing"), Scope: User}}, deny)
	assert.Eq(t, strings.Join(names(got), ","), "ok")
	assert.Eq(t, len(warns), 0)
}

func TestDiscover_SymlinkedSkillResolvedAndDeduped(t *testing.T) {
	store, a, b := realDir(t), realDir(t), realDir(t)
	target := skillFile(t, store, "tool", "name: tool\ndescription: d\n")
	if err := os.Symlink(target, filepath.Join(a, "tool")); err != nil {
		t.Skipf("symlinks unavailable: %v", err) // windows without the privilege
	}
	assert.NoErr(t, os.Symlink(target, filepath.Join(b, "tool")))
	got, warns := Discover([]Source{{Dir: a, Scope: User}, {Dir: b, Scope: User}}, noDeny)
	assert.Eq(t, len(got), 1)
	assert.Eq(t, got[0].Dir, target)
	assert.Eq(t, got[0].Location, filepath.Join(target, "SKILL.md"))
	assert.Eq(t, len(warns), 0) // same skill seen twice is not a collision
}

func TestDiscover_FlagsAndLongDescription(t *testing.T) {
	dir := realDir(t)
	skillFile(t, dir, "manual", "name: manual\ndescription: d\ndisable-model-invocation: true\n")
	skillFile(t, dir, "long", "name: long\ndescription: "+strings.Repeat("é", maxDescription+5)+"\n")
	got, warns := Discover([]Source{{Dir: dir, Scope: User}}, noDeny)
	assert.Eq(t, len(got), 2)
	for _, s := range got {
		switch s.Name {
		case "manual":
			assert.Eq(t, s.DisableModelInvocation, true)
		case "long":
			assert.Eq(t, len([]rune(s.Description)), maxDescription)
		}
	}
	assert.Contains(t, strings.Join(warns, "\n"), "description longer than")
}

func TestDiscover_KeepsContentForTrust(t *testing.T) {
	dir := realDir(t)
	skillFile(t, dir, "x", "name: x\ndescription: d\n")
	got, _ := Discover([]Source{{Dir: dir, Scope: Project}}, noDeny)
	assert.Eq(t, string(got[0].Content), "---\nname: x\ndescription: d\n---\n# body\n")
}

func TestSources_ProjectConfinedThenUser(t *testing.T) {
	root := realDir(t)
	got := ProjectSources(root)
	assert.Eq(t, len(got), 2)
	assert.Eq(t, got[0], Source{Dir: filepath.Join(root, ".agents", "skills"), Scope: Project, Confine: root})
	assert.Eq(t, got[1], Source{Dir: filepath.Join(root, ".claude", "skills"), Scope: Project, Confine: root})
	user := UserSources("/h")
	assert.Eq(t, user[0], Source{Dir: filepath.Join("/h", ".agents", "skills"), Scope: User})
	assert.Eq(t, user[1], Source{Dir: filepath.Join("/h", ".claude", "skills"), Scope: User})
	assert.Eq(t, len(UserSources("")), 0)
}

func TestDiscover_NestedFolders(t *testing.T) {
	dir := realDir(t)
	skillFile(t, filepath.Join(dir, "group"), "deep", "name: deep\ndescription: d\n")
	skillFile(t, filepath.Join(dir, "a", "b", "c"), "deeper", "name: deeper\ndescription: d\n")
	outer := skillFile(t, dir, "outer", "name: outer\ndescription: d\n")
	skillFile(t, outer, "inner", "name: inner\ndescription: d\n") // inside a skill: not a skill
	skillFile(t, filepath.Join(dir, "node_modules"), "dep", "name: dep\ndescription: d\n")
	skillFile(t, filepath.Join(dir, ".git"), "git", "name: git\ndescription: d\n")
	got, warns := Discover([]Source{{Dir: dir, Scope: User}}, noDeny)
	assert.Eq(t, strings.Join(names(got), ","), "deep,deeper,outer")
	assert.Eq(t, len(warns), 0)
}

func TestDiscover_DepthLimit(t *testing.T) {
	dir := realDir(t)
	parts := []string{dir}
	for i := 0; i < maxDepth; i++ {
		parts = append(parts, "d")
	}
	skillFile(t, filepath.Join(parts[:maxDepth]...), "edge", "name: edge\ndescription: d\n")
	skillFile(t, filepath.Join(parts...), "too-deep", "name: too-deep\ndescription: d\n")
	got, _ := Discover([]Source{{Dir: dir, Scope: User}}, noDeny)
	assert.Eq(t, strings.Join(names(got), ","), "edge")
}

func TestDiscover_SymlinkLoopTerminates(t *testing.T) {
	dir := realDir(t)
	skillFile(t, filepath.Join(dir, "g"), "ok", "name: ok\ndescription: d\n")
	if err := os.Symlink(dir, filepath.Join(dir, "g", "loop")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	got, _ := Discover([]Source{{Dir: dir, Scope: User}}, noDeny)
	assert.Eq(t, strings.Join(names(got), ","), "ok")
}

func TestDiscover_UnreadableSubdirSkipped(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	dir := realDir(t)
	skillFile(t, dir, "ok", "name: ok\ndescription: d\n")
	locked := filepath.Join(dir, "locked")
	assert.NoErr(t, os.MkdirAll(locked, 0o755))
	assert.NoErr(t, os.Chmod(locked, 0))
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	got, warns := Discover([]Source{{Dir: dir, Scope: User}}, noDeny)
	assert.Eq(t, strings.Join(names(got), ","), "ok")
	assert.Eq(t, len(warns), 0)
}

func TestDiscover_ConfinedSourceRefusesEscapes(t *testing.T) {
	root, outside := realDir(t), realDir(t)
	src := filepath.Join(root, ".agents", "skills")
	skillFile(t, src, "inside", "name: inside\ndescription: d\n")
	escaped := skillFile(t, outside, "escaped", "name: escaped\ndescription: d\n")
	skillFile(t, filepath.Join(outside, "tree"), "walked", "name: walked\ndescription: d\n")
	if err := os.Symlink(escaped, filepath.Join(src, "escaped")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	assert.NoErr(t, os.Symlink(outside, filepath.Join(src, "tree")))
	got, warns := Discover([]Source{{Dir: src, Scope: Project, Confine: root}}, noDeny)
	assert.Eq(t, strings.Join(names(got), ","), "inside")
	assert.Contains(t, strings.Join(warns, "\n"), "outside the project")
}

func TestMerge_ProjectWinsAndDedupes(t *testing.T) {
	project := []Skill{{Name: "pdf", Dir: "/p/pdf", Location: "/p/pdf/SKILL.md", Scope: Project}}
	user := []Skill{
		{Name: "git", Dir: "/u/git", Location: "/u/git/SKILL.md", Scope: User},
		{Name: "pdf", Dir: "/u/pdf", Location: "/u/pdf/SKILL.md", Scope: User},
		{Name: "same", Dir: "/p/pdf", Location: "/p/pdf/SKILL.md", Scope: User},
	}
	got, warns := Merge(project, user)
	assert.Eq(t, strings.Join(names(got), ","), "git,pdf")
	assert.Eq(t, got[1].Scope, Project)
	assert.Eq(t, len(warns), 1)
	assert.Contains(t, warns[0], "/u/pdf/SKILL.md shadowed by /p/pdf/SKILL.md")
}
