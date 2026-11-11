package main

import (
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
	dir = t.TempDir()
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
