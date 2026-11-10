package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
)

func profileEnv(t *testing.T, extra ...string) (env []string, jasHome string) {
	t.Helper()
	userHome := t.TempDir()
	return append(jasEnv(userHome, "http://127.0.0.1:1"), extra...), filepath.Join(userHome, ".jas")
}

func TestProfileCmd_NewListUseShow(t *testing.T) {
	env, home := profileEnv(t)
	_, se, code := runJas(t, env, "profile", "new", "ds")
	assert.Eq[any](t, code, 0)
	fi, err := os.Stat(filepath.Join(home, "profiles", "ds"))
	assert.NoErr(t, err)
	ok(t, fi.IsDir())

	skill := filepath.Join(home, "profiles", "ds", "skills", "demo")
	assert.NoErr(t, os.MkdirAll(skill, 0o700))
	assert.NoErr(t, os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: demo\ndescription: Use when demoing\n---\nbody\n"), 0o600))
	assert.NoErr(t, os.WriteFile(filepath.Join(home, "profiles", "ds", "config.json"), []byte(`{"model":"x"}`), 0o600))

	out, se, code := runJas(t, env, "profile", "list")
	assert.Eq[any](t, code, 0)
	ok(t, strings.Contains(out, "* default  0 skills"), out)
	ok(t, strings.Contains(out, "  ds  1 skills"), out)

	_, se, code = runJas(t, env, "profile", "use", "ds")
	assert.Eq[any](t, code, 0)
	out, _, _ = runJas(t, env, "profile", "list")
	ok(t, strings.Contains(out, "* ds  1 skills"), out)
	ok(t, strings.Contains(out, "  default  0 skills"), out)

	out, se, code = runJas(t, env, "profile", "show")
	assert.Eq[any](t, code, 0)
	ok(t, strings.Contains(out, filepath.Join(home, "profiles", "ds")), out)
	ok(t, strings.Contains(out, "model"), out)
	ok(t, strings.Contains(out, "AGENTS.md: no"), out)
	ok(t, strings.Contains(out, "demo"), out)

	_, se, code = runJas(t, env, "profile", "new", "ds")
	assert.Eq[any](t, code, core.ExitUsage)
	ok(t, strings.Contains(se, `profile "ds" exists`), se)
}

func TestProfileCmd_NewInvalidName(t *testing.T) {
	env, _ := profileEnv(t)
	for _, n := range []string{"Bad", "a/b", "-x", ""} {
		_, _, code := runJas(t, env, "profile", "new", n)
		assert.Eq[any](t, code, core.ExitUsage)
	}
}

func TestProfileCmd_UseUnknown(t *testing.T) {
	env, home := profileEnv(t)
	_, _, code := runJas(t, env, "profile", "use", "nope")
	assert.Eq[any](t, code, core.ExitUsage)
	_, err := os.Stat(filepath.Join(home, "config.json"))
	ok(t, os.IsNotExist(err))
}

func TestProfileCmd_ListJSON(t *testing.T) {
	env, home := profileEnv(t)
	assert.NoErr(t, os.MkdirAll(filepath.Join(home, "profiles", "ds"), 0o700))
	out, _, code := runJas(t, env, "profile", "list", "--json")
	assert.Eq[any](t, code, 0)
	var got []map[string]any
	assert.NoErr(t, json.Unmarshal([]byte(out), &got))
	assert.Eq[any](t, len(got), 2)
	assert.Eq[any](t, got[0]["name"], "default")
	assert.Eq[any](t, got[0]["active"], true)
	assert.Eq[any](t, got[1]["name"], "ds")
	assert.Eq[any](t, got[1]["active"], false)
	assert.Eq[any](t, got[1]["skills"], float64(0))
}

func TestConfig_InProfileWrites(t *testing.T) {
	env, home := profileEnv(t, "JAS_PROFILE=ds")
	assert.NoErr(t, os.MkdirAll(filepath.Join(home, "profiles", "ds"), 0o700))
	_, _, code := runJas(t, env, "config", "set", "--in-profile", "model", "x")
	assert.Eq[any](t, code, 0)
	b, err := os.ReadFile(filepath.Join(home, "profiles", "ds", "config.json"))
	assert.NoErr(t, err)
	ok(t, strings.Contains(string(b), `"model": "x"`), string(b))
	out, _, code := runJas(t, env, "config", "get", "--in-profile", "model")
	assert.Eq[any](t, code, 0)
	assert.Eq[any](t, strings.TrimSpace(out), "x")
}

func TestConfig_InProfileRefusesEndpoint(t *testing.T) {
	env, home := profileEnv(t, "JAS_PROFILE=ds")
	assert.NoErr(t, os.MkdirAll(filepath.Join(home, "profiles", "ds"), 0o700))
	for _, k := range []string{"base_url", "provider", "search_base_url", "profile"} {
		_, _, code := runJas(t, env, "config", "set", "--in-profile", k, "v")
		assert.Eq[any](t, code, core.ExitUsage)
	}
}

func TestConfig_InProfileUnknownProfile(t *testing.T) {
	env, _ := profileEnv(t, "JAS_PROFILE=ghost")
	_, _, code := runJas(t, env, "config", "set", "--in-profile", "model", "x")
	assert.Eq[any](t, code, core.ExitUsage)
}

func ok(t *testing.T, cond bool, msg ...string) {
	t.Helper()
	if !cond {
		t.Fatalf("assertion failed: %s", strings.Join(msg, " "))
	}
}
