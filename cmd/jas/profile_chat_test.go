package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/farm"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/internal/testutil"
	"github.com/jasonli0226/jas/providers/fake"
	"github.com/jasonli0226/jas/settings"
	"github.com/jasonli0226/jas/tools"
)

// profileChatEnv is testEnv on the default profile, as prepare builds it, with
// two profiles in the jas home: default (skill "oldskill", AGENTS.md
// DEFAULT-PROF-RULES) and ds (skill "newskill", AGENTS.md DS-PROF-RULES).
// It returns the env and both SKILL.md paths.
func profileChatEnv(t *testing.T) (e *env, oldSkill, newSkill string) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir()) // the manual skill installs here
	e = testEnv(t)
	writeProfile(t, e.home, "default", map[string]string{"AGENTS.md": "DEFAULT-PROF-RULES"})
	writeProfile(t, e.home, "ds", map[string]string{"AGENTS.md": "DS-PROF-RULES"})
	oldSkill = writeSkill(t, filepath.Join(e.home, "profiles", "default", "skills"), "oldskill", "Use for old work", "OLD-SKILL-BODY-7701\n")
	newSkill = writeSkill(t, filepath.Join(e.home, "profiles", "ds", "skills"), "newskill", "Use for new work", "NEW-SKILL-BODY-7702\n")
	e.profile = settings.DefaultProfile
	e.policy = core.ExecPolicy{Root: e.root, Deny: denyPatterns(e.home)}
	e.files = e.files.WithProfile(settings.DefaultProfile, settings.File{})
	useURL(t, e, e.cfg.BaseURL)
	_, _, err := e.reloadContext(nil) // the start state: default's skills and AGENTS.md
	assert.NoErr(t, err)
	return e, oldSkill, newSkill
}

// useURL stores testEnv's settings, with base_url url, as e's global config
// file, so a settings rebuild (a profile switch) keeps them.
func useURL(t *testing.T, e *env, url string) {
	t.Helper()
	e.files.Global = settings.File{Values: map[string]string{
		"provider": settings.ProviderCustom, "model": "m", "base_url": url, "approve": "never",
	}}
	e.layers, _ = e.files.ForProject(false)
	assert.NoErr(t, (&chatBackend{e: e}).reconfigure())
}

func TestChatBackend_SwitchProfileSwapsSkillsAndAgents(t *testing.T) {
	e, oldSkill, newSkill := profileChatEnv(t)
	url, _ := testutil.FakeOpenAI(t, []fake.Turn{{Text: "hi"}})
	useURL(t, e, url)
	b := &chatBackend{e: e}
	live, err := b.NewSession("m", core.NewQuietUI(), core.AllowAll{})
	assert.NoErr(t, err)
	t.Cleanup(func() { _ = live.Close("ok", 0) })
	s := live.Session
	assert.Contains(t, s.System(), "DEFAULT-PROF-RULES")
	assert.Contains(t, s.System(), "oldskill")
	_, err = s.Send(t.Context(), "hello")
	assert.NoErr(t, err)
	before := len(s.Messages())

	sw, err := b.SwitchProfile(s, "ds", nil)
	assert.NoErr(t, err)
	assert.Eq(t, b.Profile(), "ds")
	assert.Contains(t, s.System(), "DS-PROF-RULES")
	assert.NotContains(t, s.System(), "DEFAULT-PROF-RULES")
	assert.Contains(t, s.System(), "newskill")
	assert.NotContains(t, s.System(), "oldskill")
	assert.Eq(t, len(s.Messages()), before)
	assert.Eq(t, sw.Notices[0], "profile ds · no project context file · 2 skills")
	assert.Eq(t, e.policy.ProfileSkills, filepath.Join(e.home, "profiles", "ds", "skills"))
	assert.Eq(t, slices.Contains(e.policy.ReadRoots, filepath.Dir(newSkill)), true)
	assert.Eq(t, slices.Contains(e.policy.ReadRoots, filepath.Dir(oldSkill)), false)
}

func TestChatBackend_SwitchProfileLiveAndDeferred(t *testing.T) {
	e, _, _ := profileChatEnv(t)
	writeJSON(t, settings.ProfileConfigPath(e.home, "ds"), `{"approve":"shell","max_turns":"3"}`)
	b := &chatBackend{e: e}
	s := quietSession(t, e)
	sw, err := b.SwitchProfile(s, "ds", nil)
	assert.NoErr(t, err)
	assert.Eq(t, len(sw.Live), 1)
	assert.Eq(t, sw.Live["approve"], "shell")
	assert.Eq(t, strings.Join(sw.Deferred, ","), "max_turns")
	assert.Eq(t, e.cfg.MaxTurns, 3) // the next /new starts on it
	assert.Eq(t, e.level.String(), "shell")

	sw, err = b.SwitchProfile(s, "default", nil) // and back: both revert
	assert.NoErr(t, err)
	assert.Eq(t, sw.Live["approve"], "never")
	assert.Eq(t, strings.Join(sw.Deferred, ","), "max_turns")
}

func TestChatBackend_SwitchProfileUnknown(t *testing.T) {
	e, _, _ := profileChatEnv(t)
	b := &chatBackend{e: e}
	s := quietSession(t, e)
	system := s.System()
	_, err := b.SwitchProfile(s, "nope", nil)
	if err == nil || err.Error() != `unknown profile "nope"` {
		t.Fatalf("want unknown profile error, got %v", err)
	}
	_, err = b.SwitchProfile(s, "../ds", nil)
	assert.Eq(t, err != nil, true)
	assert.Eq(t, b.Profile(), "default")
	assert.Eq(t, s.System(), system)
}

func TestChatBackend_SwitchProfileLogsEvent(t *testing.T) {
	e, _, _ := profileChatEnv(t)
	writeJSON(t, settings.ProfileConfigPath(e.home, "ds"), `{"max_turns":"3"}`)
	b := &chatBackend{e: e}
	live, err := b.NewSession("m", core.NewQuietUI(), core.AllowAll{})
	assert.NoErr(t, err)
	_, err = b.SwitchProfile(live.Session, "ds", nil)
	assert.NoErr(t, err)
	assert.NoErr(t, live.Close("ok", 0))
	var ev map[string]any
	for _, x := range testutil.ReadEvents(t, live.Path) {
		if x["type"] == core.EvProfileChanged {
			ev = x
		}
	}
	if ev == nil {
		t.Fatal("no profile_changed event")
	}
	assert.Eq(t, ev["from"], any("default"))
	assert.Eq(t, ev["to"], any("ds"))
	assert.Eq(t, ev["deferred"].([]any)[0], any("max_turns"))
	var names []string
	for _, sk := range ev["skills"].([]any) {
		names = append(names, sk.(map[string]any)["name"].(string))
	}
	assert.Eq(t, strings.Join(names, ","), "jas,newskill")
	cf := ev["context_files"].([]any)
	assert.Contains(t, cf[0].(map[string]any)["path"].(string), filepath.Join("profiles", "ds", "AGENTS.md"))
	st, err := core.Replay(live.Path)
	assert.NoErr(t, err)
	assert.Eq(t, st.Profile, "ds")
}

func TestChatBackend_ResumeNoticesOtherProfile(t *testing.T) {
	e, _, _ := profileChatEnv(t)
	url, _ := testutil.FakeOpenAI(t, []fake.Turn{{Text: "a"}, {Text: "b"}})
	useURL(t, e, url)
	b := &chatBackend{e: e}
	saved := saveSession(t, b, "m") // recorded on default
	_, err := b.SwitchProfile(quietSession(t, e), "ds", nil)
	assert.NoErr(t, err)
	live, notices, err := b.Resume(saved.Path, core.NewQuietUI(), core.AllowAll{})
	assert.NoErr(t, err)
	defer live.Close("ok", 0)
	assert.Contains(t, strings.Join(notices, "\n"), `session was on profile "default"; continuing on "ds"`)
	assert.Contains(t, live.Session.System(), "DS-PROF-RULES")

	// On the recorded profile there is no notice.
	again, notices, err := b.Resume(saveSession(t, b, "m").Path, core.NewQuietUI(), core.AllowAll{})
	assert.NoErr(t, err)
	defer again.Close("ok", 0)
	assert.NotContains(t, strings.Join(notices, "\n"), "session was on profile")
}

func TestChatBackend_SwitchProfileReadException(t *testing.T) {
	e, oldSkill, newSkill := profileChatEnv(t)
	readOld, _ := json.Marshal(map[string]string{"path": oldSkill})
	readNew, _ := json.Marshal(map[string]string{"path": newSkill})
	url, bodies := testutil.FakeOpenAI(t, []fake.Turn{
		{Calls: []fake.Call{{Name: "read", Args: string(readOld)}, {Name: "read", Args: string(readNew)}}},
		{Text: "done"},
	})
	useURL(t, e, url)
	b := &chatBackend{e: e}
	live, err := b.NewSession("m", core.NewQuietUI(), core.AllowAll{})
	assert.NoErr(t, err)
	_, err = b.SwitchProfile(live.Session, "ds", nil)
	assert.NoErr(t, err)
	_, err = live.Session.Send(t.Context(), "read both")
	assert.NoErr(t, err)
	assert.NoErr(t, live.Close("ok", 0))
	var status []string // old, then new
	for _, ev := range testutil.ReadEvents(t, live.Path) {
		if ev["type"] == core.EvToolResult {
			status = append(status, ev["status"].(string))
		}
	}
	assert.Eq(t, len(status), 2)
	assert.Eq(t, status[1], "ok")
	if status[0] == "ok" {
		t.Fatal("the old profile's skill is still readable after the switch")
	}
	sent := string(bodies()[1])
	assert.Contains(t, sent, "NEW-SKILL-BODY-7702")
	assert.NotContains(t, sent, "OLD-SKILL-BODY-7701")
}

func TestChatBackend_FarmAfterSwitchUsesNewProfile(t *testing.T) {
	e, oldSkill, newSkill := profileChatEnv(t)
	b := &chatBackend{e: e}
	readOld, _ := json.Marshal(map[string]string{"path": oldSkill})
	readNew, _ := json.Marshal(map[string]string{"path": newSkill})
	e.provider = &fake.Provider{Turns: []fake.Turn{
		{Calls: []fake.Call{{Name: "read", Args: string(readOld)}, {Name: "read", Args: string(readNew)}}},
		{Text: "done"},
	}}
	worker := func(newSession func(farm.WorkerSession) (*core.Session, error), base core.ExecPolicy) (*core.Session, string) {
		path := filepath.Join(t.TempDir(), "events.jsonl")
		log, err := core.OpenEventLog(path, nil, nil)
		assert.NoErr(t, err)
		t.Cleanup(func() { log.Close() })
		ws := farm.WorkerSession{Part: farm.Part{ID: "a"}, Root: e.root, Log: log, System: "FARM-PART",
			Tools: tools.ReadOnly(), Policy: farm.WorkerPolicy(base, farm.Role{}, e.root), Gate: core.AllowAll{}}
		s, err := newSession(ws)
		assert.NoErr(t, err)
		t.Cleanup(func() { _ = s.Close("ok", 0) })
		return s, path
	}

	// jas farm run: farmSession reads env when the worker starts.
	_, err := b.SwitchProfile(quietSession(t, e), "ds", nil)
	assert.NoErr(t, err)
	s, _ := worker(farmSession(e), e.policy)
	assert.Contains(t, s.System(), "DS-PROF-RULES")
	assert.NotContains(t, s.System(), "DEFAULT-PROF-RULES")
	assert.Contains(t, s.System(), "FARM-PART")

	// The chat's farm runs on a copy of env taken when it starts (farmEnv),
	// with that copy's policy as the workers' base. A worker started after
	// a switch still gets the new profile's prompt and read exception.
	_, err = b.SwitchProfile(quietSession(t, e), "default", nil)
	assert.NoErr(t, err)
	started := *b.e
	_, err = b.SwitchProfile(quietSession(t, e), "ds", nil)
	assert.NoErr(t, err)
	s, path := worker(b.workerSession(&started), started.policy)
	assert.Contains(t, s.System(), "DS-PROF-RULES")
	assert.Contains(t, s.System(), "newskill")
	assert.NotContains(t, s.System(), "DEFAULT-PROF-RULES")
	_, err = s.Send(t.Context(), "read both")
	assert.NoErr(t, err)
	assert.NoErr(t, s.Close("ok", 0))
	var status []string // old, then new
	for _, ev := range testutil.ReadEvents(t, path) {
		if ev["type"] == core.EvToolResult {
			status = append(status, ev["status"].(string))
		}
		if ev["type"] == core.EvSessionStarted {
			assert.Eq(t, ev["profile"], any("ds"))
		}
	}
	assert.Eq(t, len(status), 2)
	assert.Eq(t, status[1], "ok")
	if status[0] == "ok" {
		t.Fatal("a worker started after the switch can read the old profile's skill")
	}
}

func TestWorkerContext_UntrustedKeepsProfile(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	mainRoot, worktree, home := t.TempDir(), t.TempDir(), t.TempDir()
	assert.NoErr(t, os.WriteFile(filepath.Join(mainRoot, "AGENTS.md"), []byte("PROJECT-RULES-5150"), 0o644))
	assert.NoErr(t, os.WriteFile(filepath.Join(worktree, "AGENTS.md"), []byte("CHANGED-RULES"), 0o644))
	writeProfile(t, home, "ds", map[string]string{"AGENTS.md": "DS-PROF-RULES"})
	writeSkill(t, filepath.Join(home, "profiles", "ds", "skills"), "newskill", "Use for new work", "body\n")
	e := &env{root: mainRoot, home: home, profile: "ds", trust: true, red: newRedactor(""),
		policy: core.ExecPolicy{Root: mainRoot, Deny: denyPatterns(home)}}
	_, _, err := e.reloadContext(nil)
	assert.NoErr(t, err)
	assert.Contains(t, e.pc.system, "PROJECT-RULES-5150")

	sys, trusted := workerContext(e.pc, mainRoot, worktree)
	assert.Eq(t, trusted, false)
	assert.Contains(t, sys, "DS-PROF-RULES")
	assert.Contains(t, sys, "newskill")
	assert.NotContains(t, sys, "PROJECT-RULES-5150")
	assert.NotContains(t, sys, "CHANGED-RULES")
}

func TestChatBackend_ProfilesAndSetDefault(t *testing.T) {
	e, _, _ := profileChatEnv(t)
	b := &chatBackend{e: e}
	assert.Eq(t, strings.Join(b.Profiles(), ","), "default,ds")
	assert.NoErr(t, b.SetDefaultProfile("ds"))
	f, err := settings.LoadFile(filepath.Join(e.home, configFile))
	assert.NoErr(t, err)
	assert.Eq(t, f.Values["profile"], "ds")
	assert.Eq(t, b.Profile(), "default") // the default is for the next start
	if err := b.SetDefaultProfile("nope"); err == nil {
		t.Fatal("want an error for an unknown profile")
	}
}

// quietSession is a session over e's policy and system prompt, as the chat
// would start it, logging to a temp file.
func quietSession(t *testing.T, e *env) *core.Session {
	t.Helper()
	log, err := core.OpenEventLog(filepath.Join(t.TempDir(), "events.jsonl"), nil, nil)
	assert.NoErr(t, err)
	t.Cleanup(func() { _ = log.Close() })
	s, err := core.NewSession(core.SessionConfig{
		Provider: &fake.Provider{}, Model: "m", System: e.pc.system,
		Exec: &core.Executor{Reg: core.NewRegistry(), Policy: e.policy, Log: log},
		Log:  log, Budget: &core.Budget{Window: 100000},
	})
	assert.NoErr(t, err)
	return s
}
