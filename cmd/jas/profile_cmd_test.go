package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/settings"
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
		_, se, code := runJas(t, env, "config", "set", "--in-profile", k, "v")
		assert.Eq[any](t, code, core.ExitUsage)
		assert.Contains(t, se, "cannot be set in a profile")
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

func TestConfig_GetListShowProfileLayer(t *testing.T) {
	env, home := profileEnv(t, "JAS_PROFILE=ds")
	assert.NoErr(t, os.MkdirAll(filepath.Join(home, "profiles", "ds"), 0o700))
	assert.NoErr(t, os.WriteFile(filepath.Join(home, "profiles", "ds", "config.json"), []byte(`{"model":"x"}`), 0o600))
	assert.NoErr(t, os.WriteFile(filepath.Join(home, "config.json"), []byte(`{"model":"g"}`), 0o600))
	env = append(env, "JAS_MODEL=") // let the file layers show
	out, se, code := runJas(t, env, "config", "get", "model")
	assert.Eq(t, code, 0)
	assert.Eq(t, strings.TrimSpace(out), "x")
	_ = se
	out, _, code = runJas(t, env, "config", "list")
	assert.Eq(t, code, 0)
	assert.Contains(t, out, "model  x  (profile config (ds))")
}

func TestConfig_InProfileConfigNamedMissingProfile(t *testing.T) {
	env, home := profileEnv(t)
	assert.NoErr(t, os.MkdirAll(home, 0o700))
	assert.NoErr(t, os.WriteFile(filepath.Join(home, "config.json"), []byte(`{"profile":"ghost"}`), 0o600))
	for _, args := range [][]string{{"set", "--in-profile", "model", "x"}, {"unset", "--in-profile", "model"}} {
		_, se, code := runJas(t, env, append([]string{"config"}, args...)...)
		assert.Eq[any](t, code, core.ExitUsage)
		assert.Contains(t, se, `profile "ghost"`)
	}
	_, err := os.Stat(filepath.Join(home, "profiles", "default", "config.json"))
	ok(t, os.IsNotExist(err))
}

func TestProfileList_ShowsExtends(t *testing.T) {
	userHome, _ := chainHome(t, nil, nil)
	env := jasEnv(userHome, "http://127.0.0.1:1")
	out, _, code := runJas(t, env, "profile", "list")
	assert.Eq[any](t, code, 0)
	assert.Contains(t, out, "  c  0 skills  (extends p)\n")
	out, _, code = runJas(t, env, "profile", "list", "--json")
	assert.Eq[any](t, code, 0)
	assert.Contains(t, out, `"name":"c","active":false,"skills":0,"extends":"p"`)
	ok(t, !strings.Contains(out, `"error"`), out)
}

func TestProfileList_BadYamlDoesNotFail(t *testing.T) {
	userHome, _ := chainHome(t, nil, map[string]string{settings.ProfileMetaFile: "bogus: 1\n"})
	env := jasEnv(userHome, "http://127.0.0.1:1")
	out, _, code := runJas(t, env, "profile", "list")
	assert.Eq[any](t, code, 0)
	assert.Contains(t, out, "(bad profile.yaml)")
	out, _, _ = runJas(t, env, "profile", "list", "--json")
	assert.Contains(t, out, `"error":"`)
}

func TestProfileShow_ExtendsLineWithMissingParent(t *testing.T) {
	userHome, _ := chainHome(t, nil, map[string]string{settings.ProfileMetaFile: "extends: nope\nreplace_agents: true\n"})
	out, _, code := runJas(t, jasEnv(userHome, "http://127.0.0.1:1"), "profile", "show", "c")
	assert.Eq[any](t, code, 0)
	assert.Contains(t, out, "extends: nope\n")
	assert.Contains(t, out, "replace_agents: true\n")
}

func TestProfileShow_BadYamlFails(t *testing.T) {
	userHome, _ := chainHome(t, nil, map[string]string{settings.ProfileMetaFile: "bogus: 1\n"})
	_, se, code := runJas(t, jasEnv(userHome, "http://127.0.0.1:1"), "profile", "show", "c")
	ok(t, code != 0)
	assert.Contains(t, se, "profile.yaml")
}

func TestProfileShow_Resolved(t *testing.T) {
	skill := func(n string) string { return "---\nname: " + n + "\ndescription: Use when " + n + "\n---\nbody\n" }
	userHome, _ := chainHome(t, map[string]string{
		"config.json": `{"max_turns":"7","model":"m1"}`, "AGENTS.md": "P-RULE",
		"skills/sql/SKILL.md": skill("sql"), "skills/old/SKILL.md": skill("old"),
	}, map[string]string{
		settings.ProfileMetaFile: "extends: p\nexclude: [old]\n",
		"config.json":            `{"model":"m2"}`, "AGENTS.md": "C-RULE", "skills/mine/SKILL.md": skill("mine"),
	})
	out, se, code := runJas(t, jasEnv(userHome, "http://127.0.0.1:1"), "profile", "show", "c", "--resolved")
	ok(t, code == 0, se)
	assertBefore(t, out, "chain: p → c", "settings:", "max_turns  7  (p)", "AGENTS.md:",
		"Profile instructions (p AGENTS.md):", "P-RULE", "Profile instructions (c AGENTS.md):", "C-RULE",
		"skills (2):", "mine  (c)", "sql  (p)", "excluded:", "old  (by c)")
	assert.Contains(t, out, "model  m2  (c)")
}

func TestProfileShow_ResolvedBrokenChain(t *testing.T) {
	userHome, _ := chainHome(t, nil, map[string]string{settings.ProfileMetaFile: "extends: nope\n"})
	_, se, code := runJas(t, jasEnv(userHome, "http://127.0.0.1:1"), "profile", "show", "c", "--resolved")
	assert.Eq[any](t, code, core.ExitUsage)
	assert.Contains(t, se, "which does not exist")
}

func TestProfileNew_Extends(t *testing.T) {
	env, home := profileEnv(t)
	writeProfile(t, home, "p", nil)
	_, se, code := runJas(t, env, "profile", "new", "c", "--extends", "p")
	ok(t, code == 0, se)
	path := filepath.Join(home, "profiles", "c", settings.ProfileMetaFile)
	b, err := os.ReadFile(path)
	assert.NoErr(t, err)
	assert.Eq(t, string(b), "extends: p\n")
	fi, err := os.Stat(path)
	assert.NoErr(t, err)
	assert.Perm(t, fi.Mode().Perm(), 0o600)
	_, _, code = runJas(t, env, "profile", "new", "d", "--extends", "nope")
	assert.Eq[any](t, code, core.ExitUsage)
	_, err = os.Stat(filepath.Join(home, "profiles", "d"))
	ok(t, err != nil)
}

func TestProfileShow_ResolvedRedactsStoredKey(t *testing.T) {
	userHome, jasHome := chainHome(t, map[string]string{"AGENTS.md": "token is plainsecret-4471 ok"}, nil)
	assert.NoErr(t, os.WriteFile(filepath.Join(jasHome, "auth.json"), []byte(`{"custom":{"key":"plainsecret-4471"}}`), 0o600))
	out, se, code := runJas(t, jasEnv(userHome, "http://127.0.0.1:1"), "profile", "show", "c", "--resolved")
	ok(t, code == 0, se)
	ok(t, !strings.Contains(out, "plainsecret-4471"), out)
	assert.Contains(t, out, "token is ")
}

func TestProfileShow_ResolvedPrintsSkillWarnings(t *testing.T) {
	userHome, _ := chainHome(t, nil, map[string]string{settings.ProfileMetaFile: "extends: p\nexclude: [ghost]\n"})
	out, se, code := runJas(t, jasEnv(userHome, "http://127.0.0.1:1"), "profile", "show", "c", "--resolved")
	ok(t, code == 0, out)
	assert.Contains(t, se, `exclude names "ghost"`)
}
