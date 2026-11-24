package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/internal/testutil"
	"github.com/jasonli0226/jas/providers/fake"
	"github.com/jasonli0226/jas/settings"
)

// chainHome is a user home whose ~/.jas holds profile p and its child c.
func chainHome(t *testing.T, parent, child map[string]string) (userHome, jasHome string) {
	t.Helper()
	userHome = t.TempDir()
	jasHome = filepath.Join(userHome, ".jas")
	writeProfile(t, jasHome, "p", parent)
	if child == nil {
		child = map[string]string{}
	}
	if _, ok := child[settings.ProfileMetaFile]; !ok {
		child[settings.ProfileMetaFile] = "extends: p\n"
	}
	writeProfile(t, jasHome, "c", child)
	return userHome, jasHome
}

// chainPrompt runs jas under profile c and returns the system prompt and stderr.
func chainPrompt(t *testing.T, userHome string) (sys, errOut string) {
	t.Helper()
	url, bodies := testutil.FakeOpenAI(t, []fake.Turn{{Text: "done"}})
	_, errOut, code := runJas(t, append(jasEnv(userHome, url), "JAS_PROFILE=c"), "run", "q")
	assert.Eq(t, code, 0)
	return systemPrompt(t, bodies()[0]), errOut
}

func skillsOf(jasHome, name string) string {
	return filepath.Join(jasHome, "profiles", name, "skills")
}

// assertBefore checks each of parts appears in s, in order.
func assertBefore(t *testing.T, s string, parts ...string) {
	t.Helper()
	at := 0
	for _, p := range parts {
		i := strings.Index(s[at:], p)
		if i < 0 {
			t.Fatalf("%q missing or out of order in:\n%s", p, s)
		}
		at += i + len(p)
	}
}

func TestRun_FlatProfilePromptUnchanged(t *testing.T) {
	userHome := t.TempDir()
	writeProfile(t, filepath.Join(userHome, ".jas"), "default", map[string]string{"AGENTS.md": "FLAT-RULE-7101"})
	url, bodies := testutil.FakeOpenAI(t, []fake.Turn{{Text: "done"}})
	_, errOut, code := runJas(t, jasEnv(userHome, url), "run", "q")
	assert.Eq(t, code, 0)
	sys := systemPrompt(t, bodies()[0])
	assert.Contains(t, sys, "Profile instructions (AGENTS.md):\nFLAT-RULE-7101")
	assert.NotContains(t, sys, "(default AGENTS.md)")
	assert.NotContains(t, errOut, "profile")
}

func TestRun_ChainAgentsOrder(t *testing.T) {
	userHome, _ := chainHome(t, map[string]string{"AGENTS.md": "PARENT-7102"}, map[string]string{"AGENTS.md": "CHILD-7103"})
	sys, _ := chainPrompt(t, userHome)
	assertBefore(t, sys, "Profile instructions (p AGENTS.md):", "PARENT-7102", "Profile instructions (c AGENTS.md):", "CHILD-7103")
}

func TestRun_ReplaceAgentsDropsParent(t *testing.T) {
	userHome, _ := chainHome(t, map[string]string{"AGENTS.md": "PARENT-7102"},
		map[string]string{"AGENTS.md": "CHILD-7103", "profile.yaml": "extends: p\nreplace_agents: true\n"})
	sys, _ := chainPrompt(t, userHome)
	assert.NotContains(t, sys, "PARENT-7102")
	assert.Contains(t, sys, "CHILD-7103")
}

// skillChain is p with skills tdd and sql, and c with its own tdd.
func skillChain(t *testing.T) (userHome, jasHome string) {
	t.Helper()
	userHome, jasHome = chainHome(t, nil, nil)
	writeSkill(t, skillsOf(jasHome, "p"), "tdd", "P-TDD-7104", "body\n")
	writeSkill(t, skillsOf(jasHome, "p"), "sql", "P-SQL-7105", "body\n")
	writeSkill(t, skillsOf(jasHome, "c"), "tdd", "C-TDD-7106", "body\n")
	return userHome, jasHome
}

func TestRun_ChainSkillsInheritAndOverride(t *testing.T) {
	userHome, _ := skillChain(t)
	sys, _ := chainPrompt(t, userHome)
	assert.Contains(t, sys, "C-TDD-7106")
	assert.Contains(t, sys, "P-SQL-7105")
	assert.NotContains(t, sys, "P-TDD-7104")
}

func TestRun_ChainSkillOverrideNoWarning(t *testing.T) {
	userHome, _ := skillChain(t)
	_, errOut := chainPrompt(t, userHome)
	assert.NotContains(t, errOut, "clash")
	assert.NotContains(t, errOut, "shadow")
}

func TestRun_ExcludeCumulative(t *testing.T) {
	userHome := t.TempDir()
	jasHome := filepath.Join(userHome, ".jas")
	writeProfile(t, jasHome, "g", nil)
	writeSkill(t, skillsOf(jasHome, "g"), "tdd", "G-TDD-7108", "body\n")
	writeProfile(t, jasHome, "p", map[string]string{"profile.yaml": "extends: g\nexclude: [tdd]\n"})
	writeProfile(t, jasHome, "c", map[string]string{"profile.yaml": "extends: p\n"})
	sys, errOut := chainPrompt(t, userHome)
	assert.NotContains(t, sys, "<name>tdd</name>")
	assert.NotContains(t, errOut, "exclude")

	writeSkill(t, skillsOf(jasHome, "c"), "tdd", "C-TDD-7109", "body\n")
	sys, _ = chainPrompt(t, userHome)
	assert.Contains(t, sys, "C-TDD-7109")
	assert.NotContains(t, sys, "G-TDD-7108")
}

func TestRun_ExcludeUnknownWarns(t *testing.T) {
	userHome, _ := chainHome(t, nil, map[string]string{"profile.yaml": "extends: p\nexclude: [nope]\n"})
	_, errOut := chainPrompt(t, userHome)
	assert.Contains(t, errOut, `exclude names "nope"`)
	assert.Contains(t, errOut, "profile.yaml")
}

func TestRun_ParentSkillReadable(t *testing.T) {
	userHome, jasHome := chainHome(t, nil, nil)
	writeSkill(t, skillsOf(jasHome, "p"), "sql", "Use for sql", "P-SQL-BODY-7110\n")
	mem := filepath.Join(jasHome, "profiles", "p", "memory")
	assert.NoErr(t, os.MkdirAll(mem, 0o700))
	assert.NoErr(t, os.WriteFile(filepath.Join(mem, "MEMORY.md"), []byte("P-MEM-7111\n"), 0o600))
	read := func(path string) fake.Turn {
		args, err := json.Marshal(map[string]string{"path": path})
		assert.NoErr(t, err)
		return fake.Turn{Calls: []fake.Call{{Name: "read", Args: string(args)}}}
	}
	url, bodies := testutil.FakeOpenAI(t, []fake.Turn{
		read("~/.jas/profiles/p/skills/sql/SKILL.md"),
		read("~/.jas/profiles/p/memory/MEMORY.md"),
		{Text: "done"},
	})
	_, _, code := runJas(t, append(jasEnv(userHome, url), "JAS_PROFILE=c"), "run", "q")
	assert.Eq(t, code, 0)
	assert.Contains(t, string(bodies()[1]), "P-SQL-BODY-7110")
	assertReadRefused(t, strings.TrimPrefix(string(bodies()[2]), string(bodies()[1])))
	assert.NotContains(t, string(bodies()[2]), "P-MEM-7111")
}

func TestRun_ChainSettingsInherit(t *testing.T) {
	userHome, _ := chainHome(t, map[string]string{"config.json": `{"max_turns": 7}`}, nil)
	env := append(jasEnv(userHome, "http://unused"), "JAS_PROFILE=c")
	out, errOut, code := runJas(t, env, "config", "get", "max_turns")
	assert.Eq(t, code, 0)
	assert.Eq(t, strings.TrimSpace(out), "7")
	assert.NotContains(t, errOut, "profile")
	out, _, code = runJas(t, env, "config", "list")
	assert.Eq(t, code, 0)
	assert.Contains(t, out, "max_turns  7  (profile config (p))")
}

func TestRun_MissingParentExit2(t *testing.T) {
	userHome := t.TempDir()
	jasHome := filepath.Join(userHome, ".jas")
	writeProfile(t, jasHome, "c", map[string]string{"profile.yaml": "extends: nope\n"})
	assert.NoErr(t, os.WriteFile(filepath.Join(jasHome, "config.json"), []byte(`{"profile": "c"}`), 0o600))
	url, _ := testutil.FakeOpenAI(t, []fake.Turn{{Text: "done"}})
	_, errOut, code := runJas(t, jasEnv(userHome, url), "run", "q")
	assert.Eq(t, code, 2)
	assert.Contains(t, errOut, `extends "nope", which does not exist`)
}

func TestRun_FallbackToChainedDefaultNamesChain(t *testing.T) {
	userHome := t.TempDir()
	jasHome := filepath.Join(userHome, ".jas")
	writeProfile(t, jasHome, "p", nil)
	writeProfile(t, jasHome, "default", map[string]string{"profile.yaml": "extends: p\n"})
	assert.NoErr(t, os.WriteFile(filepath.Join(jasHome, "config.json"), []byte(`{"profile": "gone"}`), 0o600))
	url, _ := testutil.FakeOpenAI(t, []fake.Turn{{Text: "done"}})
	_, errOut, code := runJas(t, jasEnv(userHome, url), "run", "q")
	assert.Eq(t, code, 0)
	assert.Contains(t, errOut, "using default (extends p)")
}

func TestConfig_InProfileWritesChildOnly(t *testing.T) {
	userHome, jasHome := chainHome(t, map[string]string{"config.json": `{"approve": "never"}`}, nil)
	parentPath := settings.ProfileConfigPath(jasHome, "p")
	before, err := os.ReadFile(parentPath)
	assert.NoErr(t, err)
	env := append(jasEnv(userHome, "http://unused"), "JAS_PROFILE=c")
	out, _, code := runJas(t, env, "config", "set", "--in-profile", "approve", "shell")
	assert.Eq(t, code, 0)
	assert.Contains(t, out, "overrides profile p")
	child, err := settings.LoadFile(settings.ProfileConfigPath(jasHome, "c"))
	assert.NoErr(t, err)
	assert.Eq(t, child.Values["approve"], "shell")
	after, err := os.ReadFile(parentPath)
	assert.NoErr(t, err)
	assert.Eq(t, string(after), string(before))

	out, _, code = runJas(t, env, "config", "unset", "--in-profile", "approve")
	assert.Eq(t, code, 0)
	assert.Contains(t, out, "now inherited from profile p")
}

func TestConfig_InProfileFlatPrintsNothing(t *testing.T) {
	userHome := t.TempDir()
	writeProfile(t, filepath.Join(userHome, ".jas"), "c", nil)
	env := append(jasEnv(userHome, "http://unused"), "JAS_PROFILE=c")
	out, _, code := runJas(t, env, "config", "set", "--in-profile", "approve", "shell")
	assert.Eq(t, code, 0)
	assert.Eq(t, out, "")
}

func TestRun_ChildNoOwnSkillsNoMigrationNotice(t *testing.T) {
	userHome, jasHome := chainHome(t, nil, nil)
	writeSkill(t, skillsOf(jasHome, "p"), "sql", "Use for sql", "body\n")
	writeSkill(t, filepath.Join(userHome, ".agents", "skills"), "x", "Use for x", "body\n")
	_, errOut := chainPrompt(t, userHome)
	assert.NotContains(t, errOut, "skills are now read from")
}

// /settings --in-profile names the parent the write overrides, and the one
// an unset reveals.
func TestChatBackend_SettingNoteNamesParent(t *testing.T) {
	e, _, _ := profileChatEnv(t)
	writeProfile(t, e.home, "base", map[string]string{"config.json": `{"approve": "shell"}`})
	writeProfile(t, e.home, "ds", map[string]string{"profile.yaml": "extends: base\n"})
	b := &chatBackend{e: e}
	_, err := b.SwitchProfile(quietSession(t, e), "ds", nil)
	assert.NoErr(t, err)
	assert.NoErr(t, b.SetSetting("profile", "approve", "mutating"))
	assert.Eq(t, b.SettingNote("profile", "approve", false), " (overrides profile base)")
	assert.NoErr(t, b.UnsetSetting("profile", "approve"))
	assert.Eq(t, b.SettingNote("profile", "approve", true), " (now inherited from profile base)")
	assert.Eq(t, e.cfg.Approve, "shell") // the parent's value is back
	assert.Eq(t, b.SettingNote("profile", "max_turns", false), "")
	assert.Eq(t, b.SettingNote("local", "approve", false), "")
	assert.Eq(t, b.SettingNote("", "approve", false), "") // the default scope is local
}
