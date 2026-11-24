package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/providers/fake"
	"github.com/jasonli0226/jas/skills"
)

func TestFormatReloadNotice(t *testing.T) {
	assert.Eq(t, formatReloadNotice(projectContext{}), "reloaded · no project context file · 0 skills")
	pc := projectContext{
		files:  []contextFileMeta{{Path: "/p/AGENTS.md", Loaded: true}},
		skills: []skills.Skill{{Name: "a"}, {Name: "b", DisableModelInvocation: true}},
	}
	assert.Eq(t, formatReloadNotice(pc), "reloaded · AGENTS.md loaded · 1 skill")
	pc.files[0].Loaded = false
	pc.skills = []skills.Skill{{Name: "a"}, {Name: "b"}}
	assert.Eq(t, formatReloadNotice(pc), "reloaded · AGENTS.md not loaded · 2 skills")
}

func TestFormatReloadNotice_SkipsProfileAgents(t *testing.T) {
	prof := "/h/profiles/default/AGENTS.md"
	pc := projectContext{files: []contextFileMeta{{Path: prof, Loaded: true}}, profileAgents: []string{prof}}
	assert.Eq(t, formatReloadNotice(pc), "reloaded · no project context file · 0 skills")
	pc.files = append(pc.files, contextFileMeta{Path: "/p/AGENTS.md", Loaded: false})
	assert.Eq(t, formatReloadNotice(pc), "reloaded · AGENTS.md not loaded · 0 skills")
}

func TestEnv_ReloadContextRereadsProfileAgents(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	dir := filepath.Join(home, "profiles", "ds")
	assert.NoErr(t, os.MkdirAll(dir, 0o700))
	assert.NoErr(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("RELOADED-PROFILE-1203"), 0o600))
	e := &env{root: root, home: home, profile: "ds", red: newRedactor(""), policy: core.ExecPolicy{Root: root}}
	notices, _, err := e.reloadContext(nil)
	assert.NoErr(t, err)
	assert.Contains(t, e.pc.system, "RELOADED-PROFILE-1203")
	assert.Contains(t, notices[0], "no project context file")
}

func TestEnv_ReloadContextLoadsAgents(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	text := "RELOAD-RULE-9910"
	assert.NoErr(t, os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(text), 0o644))
	real, err := filepath.EvalSymlinks(root)
	assert.NoErr(t, err)
	store := core.TrustStore{Path: filepath.Join(home, trustFile)}
	assert.NoErr(t, store.Trust(real, []byte(text)))

	e := &env{
		root: root, home: home,
		red:    newRedactor(""),
		policy: core.ExecPolicy{Root: root},
		pc:     projectContext{system: core.BuildSystem(core.BasePrompt, "")},
	}
	notices, _, err := e.reloadContext(nil) // trusted hash: no ask
	assert.NoErr(t, err)
	assert.Contains(t, e.pc.system, "RELOAD-RULE-9910")
	assert.Contains(t, notices[0], "AGENTS.md loaded")
	assert.Eq(t, e.pc.trusted, true)
}

func TestEnv_ReloadContextAskTrust(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	assert.NoErr(t, os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("NEW-RULE-4422"), 0o644))
	e := &env{
		root: root, home: home,
		red:    newRedactor(""),
		policy: core.ExecPolicy{Root: root},
		pc:     projectContext{system: "old"},
	}
	var asked string
	notices, _, err := e.reloadContext(func(p string) string {
		asked = p
		return "y"
	})
	assert.NoErr(t, err)
	assert.Contains(t, asked, "trust ")
	assert.Contains(t, asked, "AGENTS.md")
	assert.Contains(t, e.pc.system, "NEW-RULE-4422")
	assert.Contains(t, notices[0], "AGENTS.md loaded")
}

func TestEnv_ReloadContextFlagTrust(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	assert.NoErr(t, os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("FLAG-TRUST-33"), 0o644))
	e := &env{
		root: root, home: home, trust: true,
		red:    newRedactor(""),
		policy: core.ExecPolicy{Root: root},
	}
	var asked bool
	_, _, err := e.reloadContext(func(string) string { asked = true; return "n" })
	assert.NoErr(t, err)
	assert.Eq(t, asked, false) // --trust-project never asks
	assert.Contains(t, e.pc.system, "FLAG-TRUST-33")
}

func TestChatBackend_ReloadAppliesToSession(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	assert.NoErr(t, os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("LIVE-SYS-55"), 0o644))
	real, err := filepath.EvalSymlinks(root)
	assert.NoErr(t, err)
	assert.NoErr(t, (core.TrustStore{Path: filepath.Join(home, trustFile)}).Trust(real, []byte("LIVE-SYS-55")))

	e := &env{
		root: root, home: home,
		red:    newRedactor(""),
		policy: core.ExecPolicy{Root: root},
		pc:     projectContext{system: "before"},
	}
	b := &chatBackend{e: e}
	path := filepath.Join(t.TempDir(), "events.jsonl")
	log, err := core.OpenEventLog(path, nil, nil)
	assert.NoErr(t, err)
	t.Cleanup(func() { _ = log.Close() })
	prov := &fake.Provider{Turns: []fake.Turn{{Text: "ok"}}}
	s, err := core.NewSession(core.SessionConfig{
		Provider: prov, Model: "m", System: "before",
		Exec:   &core.Executor{Reg: core.NewRegistry(), Policy: e.policy, Log: log},
		Log:    log,
		Budget: &core.Budget{Window: 100000},
	})
	assert.NoErr(t, err)
	notices, err := b.Reload(s, nil)
	assert.NoErr(t, err)
	assert.Contains(t, notices[0], "AGENTS.md loaded")
	assert.Contains(t, s.System(), "LIVE-SYS-55")
	assert.Eq(t, strings.HasPrefix(s.System(), "before"), false)
}

func TestEnv_ReloadTrustChangeLeavesSettingsForNextStart(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	writeJSON(t, filepath.Join(root, ".jas", "config.json"), `{"approve":"never"}`)
	e := &env{
		root: root, home: home,
		red:    newRedactor(""),
		policy: core.ExecPolicy{Root: root},
	}
	notices, _, err := e.reloadContext(func(string) string { return "y" })
	assert.NoErr(t, err)
	assert.Eq(t, e.pc.trusted, true)
	assert.Eq(t, e.settingsTrusted, false)
	assert.Contains(t, strings.Join(notices, "\n"), "project settings apply at the next start")
	assert.Contains(t, e.projectWritable("project").Error(), "project not trusted")
}
