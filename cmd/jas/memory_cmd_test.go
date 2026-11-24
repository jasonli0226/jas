package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/internal/testutil"
	"github.com/jasonli0226/jas/memory"
	"github.com/jasonli0226/jas/sessions"
	"github.com/jasonli0226/jas/settings"
)

func memoryProject(t *testing.T) (dir, home string, env []string) {
	t.Helper()
	// Resolved: the binary's working dir comes back from getcwd with
	// symlinks resolved (macOS: /var -> /private/var), and keys on that.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home = testutil.TempHome(t)
	return dir, home, []string{"JAS_HOME=" + home, "GIT_CEILING_DIRECTORIES=" + filepath.Dir(dir)}
}

func TestMemoryCmd_Path(t *testing.T) {
	dir, home, env := memoryProject(t)
	want, err := memory.DirsFor(settings.ProfileDir(home, "default"), sessions.MainRoot(core.FindRoot(dir)))
	assert.NoErr(t, err)
	out, _, code := runJasIn(t, dir, env, "memory", "--path")
	assert.Eq(t, code, 0)
	assert.Eq(t, out, want.Project+"\n")

	// scripting use: still the project dir with memory off
	out, _, code = runJasIn(t, dir, append(env, "JAS_MEMORY=off"), "memory", "--path")
	assert.Eq(t, code, 0)
	assert.Eq(t, out, want.Project+"\n")
}

func TestMemoryCmd_Report(t *testing.T) {
	dir, _, env := memoryProject(t)
	out, _, code := runJasIn(t, dir, env, "memory")
	assert.Eq(t, code, 0)
	assert.Contains(t, out, "memory: on")
	assert.Contains(t, out, "0 notes")

	out, _, code = runJasIn(t, dir, append(env, "JAS_MEMORY=off"), "memory")
	assert.Eq(t, code, 0)
	assert.Contains(t, out, "memory is off")
}

func TestMemoryCmd_Usage(t *testing.T) {
	dir, _, env := memoryProject(t)
	_, _, code := runJasIn(t, dir, env, "memory", "extra")
	assert.Eq(t, code, core.ExitUsage)
}

func TestMemoryCmd_ProjectSettingsNeedStoredTrust(t *testing.T) {
	dir, home, env := memoryProject(t)
	cfg := settings.ProjectConfigPath(dir)
	assert.NoErr(t, os.MkdirAll(filepath.Dir(cfg), 0o700))
	assert.NoErr(t, os.WriteFile(cfg, []byte(`{"memory":"off"}`), 0o600))

	out, stderr, code := runJasIn(t, dir, env, "memory")
	assert.Eq(t, code, 0)
	assert.Contains(t, out, "memory: on") // untrusted: the project cannot narrow memory
	assert.Eq(t, stderr, "")

	bundle := trustBundle(nil, nil, nil, readProjectSettings(dir))
	assert.NoErr(t, trustStore(home).Trust(dir, bundle))
	out, _, code = runJasIn(t, dir, env, "memory")
	assert.Eq(t, code, 0)
	assert.Contains(t, out, "memory: read")
	assert.Contains(t, out, "hidden")
}

func TestMemoryCmd_ShowsIgnoredProfileMemory(t *testing.T) {
	dir, home, env := memoryProject(t)
	prof := settings.ProfileDir(home, "default")
	assert.NoErr(t, os.MkdirAll(prof, 0o700))
	assert.NoErr(t, os.Symlink(home, filepath.Join(prof, "memory")))
	// What the link reaches is never read or counted.
	assert.NoErr(t, os.WriteFile(filepath.Join(home, "MEMORY.md"), []byte("- a\n- b\n"), 0o600))
	assert.NoErr(t, os.WriteFile(filepath.Join(home, "stray.md"), []byte("x\n"), 0o600))
	out, _, code := runJasIn(t, dir, env, "memory")
	assert.Eq(t, code, 0)
	assert.Contains(t, out, "profile memory ignored: ")
	assert.NotContains(t, out, "lines")
	assert.NotContains(t, out, "notes")
}

func TestMemoryCmd_UnliftedDirIsReported(t *testing.T) {
	dir, home, env := memoryProject(t)
	prof := settings.ProfileDir(home, "default")
	assert.NoErr(t, os.MkdirAll(prof, 0o700))
	assert.NoErr(t, os.WriteFile(filepath.Join(prof, "memory"), []byte("a file\n"), 0o600))
	out, _, code := runJasIn(t, dir, env, "memory")
	assert.Eq(t, code, 0)
	assert.Contains(t, out, "is not a folder jas can use")
	assert.NotContains(t, out, "notes")
}
