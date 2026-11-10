package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/internal/testutil"
	"github.com/jasonli0226/jas/providers/fake"
)

func TestManualSkill_InstalledToCacheDir(t *testing.T) {
	cache := t.TempDir()
	userHome := t.TempDir()
	url, bodies := testutil.FakeOpenAI(t, []fake.Turn{{Text: "a"}})
	env := append(jasEnv(userHome, url), "XDG_CACHE_HOME="+cache) // Linux
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
