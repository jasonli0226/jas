package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/internal/testutil"
	"github.com/jasonli0226/jas/providers/fake"
	"github.com/jasonli0226/jas/settings"
)

func TestProfileShow_ResolvedListsModelOverrides(t *testing.T) {
	userHome, _ := chainHome(t, map[string]string{
		"config.json": `{"models":{"big":{"context_window":1000},"mid":{"context_window":500}}}`,
	}, map[string]string{"config.json": `{"models":{"big":{"context_window":2000}}}`})
	out, se, code := runJas(t, jasEnv(userHome, "http://127.0.0.1:1"), "profile", "show", "c", "--resolved")
	ok(t, code == 0, se)
	assert.Contains(t, out, "models.big.context_window  2000  (c)")
	assert.Contains(t, out, "models.mid.context_window  500  (p)")
}

func TestProfileShow_ResolvedNamesRealSource(t *testing.T) {
	userHome := t.TempDir()
	jasHome := filepath.Join(userHome, ".jas")
	writeProfile(t, jasHome, "default", nil)
	assert.NoErr(t, os.WriteFile(filepath.Join(jasHome, "config.json"), []byte(`{"profile":"gone"}`), 0o600))
	_, se, code := runJas(t, jasEnv(userHome, "http://127.0.0.1:1"), "profile", "show", "--resolved")
	ok(t, code == 0, se)
	assert.Contains(t, se, `profile "gone" (from global config) not found`)
}

func TestRun_FallbackToDeepChainedDefaultUsesArrows(t *testing.T) {
	userHome := t.TempDir()
	jasHome := filepath.Join(userHome, ".jas")
	writeProfile(t, jasHome, "root", nil)
	writeProfile(t, jasHome, "mid", map[string]string{"profile.yaml": "extends: root\n"})
	writeProfile(t, jasHome, "default", map[string]string{"profile.yaml": "extends: mid\n"})
	assert.NoErr(t, os.WriteFile(filepath.Join(jasHome, "config.json"), []byte(`{"profile": "gone"}`), 0o600))
	url, _ := testutil.FakeOpenAI(t, []fake.Turn{{Text: "done"}})
	_, errOut, code := runJas(t, jasEnv(userHome, url), "run", "q")
	assert.Eq(t, code, 0)
	assert.Contains(t, errOut, "using default (extends root → mid)")
}

// memoryReport must apply the memory-vs-parent-skills overlap check, so the
// report says what the session would do.
func TestMemoryReport_IgnoresMemoryAliasingParentSkills(t *testing.T) {
	t.Setenv("JAS_MEMORY", "")
	jasHome, root := t.TempDir(), memProject(t)
	jasHome = realPath(t, jasHome)
	writeProfile(t, jasHome, "p", map[string]string{"skills/x/SKILL.md": "x"})
	writeProfile(t, jasHome, "c", map[string]string{settings.ProfileMetaFile: "extends: p\n"})
	// The parent's skills link out of the jas home, so only the overlap
	// check can refuse memory linked onto them.
	skills := filepath.Join(jasHome, "profiles", "p", "skills")
	out := filepath.Join(realPath(t, t.TempDir()), "dotskills")
	assert.NoErr(t, os.Rename(skills, out))
	assert.NoErr(t, os.Symlink(out, skills))
	prof, _, err := loadProfile(jasHome, "c", settings.SourceDefault)
	assert.NoErr(t, err)
	dirs, err := memoryDirs(jasHome, "c", root)
	assert.NoErr(t, err)
	assert.NoErr(t, os.MkdirAll(filepath.Dir(dirs.Profile), 0o700))
	assert.NoErr(t, os.Symlink(skills, dirs.Profile))

	text, _, err := memoryReport(jasHome, prof, root, settings.Layers{ProfileName: "c"})
	assert.NoErr(t, err)
	assert.Contains(t, text, "overlaps the profile skills")
}

func realPath(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	assert.NoErr(t, err)
	return r
}

func TestEnv_ReloadContextNoticesChangedChain(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	writeProfile(t, home, "p", nil)
	c := writeProfile(t, home, "c", map[string]string{settings.ProfileMetaFile: "extends: p\n"})
	e := &env{root: root, home: home, profile: "c", red: newRedactor(""), policy: core.ExecPolicy{Root: root}}
	notices, _, err := e.reloadContext(nil) // the start state: no earlier chain to compare
	assert.NoErr(t, err)
	assert.Eq(t, len(notices), 1)

	notices, _, err = e.reloadContext(nil) // unchanged chain: the notice is unchanged
	assert.NoErr(t, err)
	assert.Eq(t, len(notices), 1)

	assert.NoErr(t, os.WriteFile(filepath.Join(c, settings.ProfileMetaFile), []byte("replace_agents: false\n"), 0o600))
	notices, _, err = e.reloadContext(nil)
	assert.NoErr(t, err)
	assert.Eq(t, len(notices), 2)
	assert.Contains(t, notices[1], "profile chain is now c")
	assert.Contains(t, notices[1], "/profile c or the next start")
}
