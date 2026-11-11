package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/internal/testutil"
	"github.com/jasonli0226/jas/providers/fake"
)

func TestManualSkill_InstalledToCacheDir(t *testing.T) {
	cache := t.TempDir()
	userHome := t.TempDir()
	url, bodies := testutil.FakeOpenAI(t, []fake.Turn{{Text: "a"}})
	env := append(jasEnv(userHome, url), "XDG_CACHE_HOME="+cache,
		"LocalAppData="+cache) // Linux, Windows (macOS uses HOME)
	if runtime.GOOS == "darwin" {
		cache = filepath.Join(userHome, "Library", "Caches")
	}
	_, _, code := runJas(t, env, "run", "q")
	assert.Eq(t, code, 0)
	_, err := os.Stat(filepath.Join(cache, "jas", "manual", "jas", "SKILL.md"))
	assert.NoErr(t, err)
	assert.Contains(t, systemPrompt(t, bodies()[0]), "<name>jas</name>")
}

func TestInstallManualSkills(t *testing.T) {
	root := t.TempDir()

	if err := installManualSkills(root); err != nil {
		t.Fatalf("install: %v", err)
	}
	path := filepath.Join(root, "jas", "SKILL.md")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	want, err := manualSkills.ReadFile("manual/jas/SKILL.md")
	if err != nil {
		t.Fatalf("read embedded: %v", err)
	}
	assert.Eq(t, string(got), string(want))

	// A second run is a no-op, and a stale copy is overwritten.
	if err := installManualSkills(root); err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	stale := []byte("stale")
	if err := os.WriteFile(path, stale, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := installManualSkills(root); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	got, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	assert.Eq(t, string(got), string(want))

	// A file the user dropped beside the manual is untouched.
	other := filepath.Join(root, "jas", "extra.md")
	if err := os.WriteFile(other, stale, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := installManualSkills(root); err != nil {
		t.Fatalf("third install: %v", err)
	}
	got, err = os.ReadFile(other)
	if err != nil {
		t.Fatal(err)
	}
	assert.Eq(t, string(got), string(stale))
}

// TestRun_ReadsManualSkillAtCatalogPath reads the built-in manual's SKILL.md
// and references/manual.md through the live policy, at the path the catalog
// gives (#175).
func TestRun_ReadsManualSkillAtCatalogPath(t *testing.T) {
	cache, userHome := t.TempDir(), t.TempDir()
	if runtime.GOOS == "darwin" {
		cache = filepath.Join(userHome, "Library", "Caches")
	}
	dir := filepath.Join(cache, "jas", "manual", "jas")
	if r, err := filepath.EvalSymlinks(filepath.Dir(cache)); err == nil {
		dir = filepath.Join(r, filepath.Base(cache), "jas", "manual", "jas")
	}
	loc, ref := filepath.Join(dir, "SKILL.md"), filepath.Join(dir, "references", "manual.md")
	paths := []string{loc, ref}
	if runtime.GOOS == "windows" {
		paths = append(paths, filepath.ToSlash(ref)) // the model may write C:/...
	}
	var turns []fake.Turn
	for _, p := range paths {
		args, err := json.Marshal(map[string]string{"path": p})
		assert.NoErr(t, err)
		turns = append(turns, fake.Turn{Calls: []fake.Call{{Name: "read", Args: string(args)}}})
	}
	url, bodies := testutil.FakeOpenAI(t, append(turns, fake.Turn{Text: "done"}))
	env := append(jasEnv(userHome, url), "XDG_CACHE_HOME="+cache, "LocalAppData="+cache)
	_, errOut, code := runJas(t, env, "run", "q")
	assert.Eq(t, code, 0)
	got := bodies()
	assert.Contains(t, systemPrompt(t, got[0]), "<location>"+loc+"</location>")
	skill, err := manualSkills.ReadFile("manual/jas/SKILL.md")
	assert.NoErr(t, err)
	firstLine := strings.SplitN(string(skill), "\n", 2)[0]
	assert.Contains(t, string(got[1]), firstLine)
	for i := 1; i < len(got); i++ {
		assert.NotContains(t, string(got[i]), "path outside project root")
		assert.NotContains(t, string(got[i]), "denied by policy")
	}
	assert.Contains(t, string(got[2]), "Approval")
	assert.NotContains(t, errOut, "denied")
}
