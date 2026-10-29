package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
)

// skillPolicy returns a project policy whose ReadRoots hold one skill dir
// with a reference file and a .env the deny floor must hide.
func skillPolicy(t *testing.T) (core.ExecPolicy, string) {
	t.Helper()
	p := policy(t)
	skill, err := filepath.EvalSymlinks(t.TempDir())
	assert.NoErr(t, err)
	p.ReadRoots = []string{skill}
	assert.NoErr(t, os.MkdirAll(filepath.Join(skill, "references"), 0o755))
	assert.NoErr(t, os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("see references/guide.md\n"), 0o644))
	assert.NoErr(t, os.WriteFile(filepath.Join(skill, "references", "guide.md"), []byte("step one\n"), 0o644))
	assert.NoErr(t, os.WriteFile(filepath.Join(skill, ".env"), []byte("step one=secret\n"), 0o644))
	return p, skill
}

func jsonArgs(t *testing.T, m map[string]string) string {
	t.Helper()
	b, err := json.Marshal(m)
	assert.NoErr(t, err)
	return string(b)
}

func TestRead_ReadRoot(t *testing.T) {
	p, skill := skillPolicy(t)
	r := call(t, Read{}, p, jsonArgs(t, map[string]string{"path": filepath.Join(skill, "SKILL.md")}))
	assert.Eq(t, r.IsError, false)
	assert.Eq(t, r.Output, "see references/guide.md\n")
	r = call(t, Read{}, p, jsonArgs(t, map[string]string{"path": filepath.Join(skill, ".env")}))
	assert.Eq(t, r.IsError, true)
}

func TestLs_ReadRootHidesDenied(t *testing.T) {
	p, skill := skillPolicy(t)
	r := call(t, Ls{}, p, jsonArgs(t, map[string]string{"path": skill}))
	assert.Eq(t, r.IsError, false)
	assert.Eq(t, r.Output, "SKILL.md\nreferences/")
}

func TestGrep_ReadRootAbsolutePathsSkipDenied(t *testing.T) {
	p, skill := skillPolicy(t)
	r := call(t, Grep{}, p, jsonArgs(t, map[string]string{"pattern": "step one", "path": skill}))
	assert.Eq(t, r.IsError, false)
	assert.Eq(t, r.Output, filepath.ToSlash(filepath.Join(skill, "references", "guide.md"))+":1:step one")
}

func TestMutatingTools_IgnoreReadRoots(t *testing.T) {
	p, skill := skillPolicy(t)
	target := filepath.Join(skill, "SKILL.md")
	r := call(t, Write{}, p, jsonArgs(t, map[string]string{"path": target, "content": "x"}))
	assert.Eq(t, r.IsError, true)
	r = call(t, Edit{}, p, jsonArgs(t, map[string]string{"path": target, "old_string": "see", "new_string": "x"}))
	assert.Eq(t, r.IsError, true)
	got, err := os.ReadFile(target)
	assert.NoErr(t, err)
	assert.Eq(t, string(got), "see references/guide.md\n")
}
