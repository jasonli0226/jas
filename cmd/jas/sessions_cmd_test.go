package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/internal/testutil"
	"github.com/jasonli0226/jas/providers/fake"
)

func TestCLI_RunRegistersSessionRecord(t *testing.T) {
	dir := t.TempDir()
	url, _ := testutil.FakeOpenAI(t, []fake.Turn{{Text: "ok"}})
	env := []string{"JAS_MODEL=m", "JAS_BASE_URL=" + url, "JAS_API_KEY=test-key-0123456789", "GIT_CEILING_DIRECTORIES=" + filepath.Dir(dir)}
	out, _, code := runJasIn(t, dir, env, "run", "--json", "--name", "nightly", "task")
	assert.Eq(t, code, 0)
	assert.Contains(t, out, `"record_path":`)
	entries, err := os.ReadDir(filepath.Join(dir, ".jas", "sessions"))
	assert.NoErr(t, err)
	assert.Eq(t, len(entries), 2) // the record and the .gitignore that hides it

	list, _, code := runJasIn(t, dir, env, "sessions")
	assert.Eq(t, code, 0)
	assert.Contains(t, list, "nightly")
	assert.Contains(t, list, "ended")

	pruned, _, code := runJasIn(t, dir, env, "sessions", "--prune")
	assert.Eq(t, code, 0)
	assert.Contains(t, pruned, "removed 0")
}

func TestCLI_FileToolsDenySessionsDir(t *testing.T) {
	dir := t.TempDir()
	assert.NoErr(t, os.MkdirAll(filepath.Join(dir, ".jas", "sessions"), 0o700))
	assert.NoErr(t, os.WriteFile(filepath.Join(dir, ".jas", "sessions", "x.json"), []byte("{}"), 0o600))
	url, _ := testutil.FakeOpenAI(t, []fake.Turn{
		{Calls: []fake.Call{{Name: "read", Args: `{"path":".jas/sessions/x.json"}`}}},
		{Text: "done"},
	})
	env := []string{"JAS_MODEL=m", "JAS_BASE_URL=" + url, "JAS_API_KEY=test-key-0123456789", "GIT_CEILING_DIRECTORIES=" + filepath.Dir(dir)}
	out, _, code := runJasIn(t, dir, env, "run", "--json", "task")
	assert.Eq(t, code, 0)
	var result string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, `"type":"tool_result"`) {
			result = l
		}
	}
	assert.Contains(t, result, "denied")
}

func TestCLI_RunPrunesStaleRecords(t *testing.T) {
	dir := t.TempDir()
	recs := filepath.Join(dir, ".jas", "sessions")
	assert.NoErr(t, os.MkdirAll(recs, 0o700))
	old := `{"v":1,"session_id":"old","pid":1,"host":"x","state":"ended","heartbeat":"2020-01-01T00:00:00Z","started":"2020-01-01T00:00:00Z","touched":[]}`
	assert.NoErr(t, os.WriteFile(filepath.Join(recs, "old.json"), []byte(old), 0o600))
	url, _ := testutil.FakeOpenAI(t, []fake.Turn{{Text: "ok"}})
	env := []string{"JAS_MODEL=m", "JAS_BASE_URL=" + url, "JAS_API_KEY=test-key-0123456789", "GIT_CEILING_DIRECTORIES=" + filepath.Dir(dir)}
	_, _, code := runJasIn(t, dir, env, "run", "--json", "task")
	assert.Eq(t, code, 0)
	if _, err := os.Stat(filepath.Join(recs, "old.json")); !os.IsNotExist(err) {
		t.Fatalf("stale record not pruned: %v", err)
	}
}

func TestCLI_FileToolsDenyFarmWorktrees(t *testing.T) {
	dir := t.TempDir()
	wt := filepath.Join(dir, ".jas", "worktrees", "f1", "a")
	assert.NoErr(t, os.MkdirAll(wt, 0o700))
	assert.NoErr(t, os.WriteFile(filepath.Join(wt, "x.go"), []byte("package a"), 0o600))
	url, _ := testutil.FakeOpenAI(t, []fake.Turn{
		{Calls: []fake.Call{{Name: "read", Args: `{"path":".jas/worktrees/f1/a/x.go"}`}}},
		{Text: "done"},
	})
	env := []string{"JAS_MODEL=m", "JAS_BASE_URL=" + url, "JAS_API_KEY=test-key-0123456789", "GIT_CEILING_DIRECTORIES=" + filepath.Dir(dir)}
	out, _, code := runJasIn(t, dir, env, "run", "--json", "task")
	assert.Eq(t, code, 0)
	var result string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, `"type":"tool_result"`) {
			result = l
		}
	}
	assert.Contains(t, result, "denied")
}
