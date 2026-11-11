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
	"github.com/jasonli0226/jas/memory"
	"github.com/jasonli0226/jas/providers/fake"
	"github.com/jasonli0226/jas/sessions"
	"github.com/jasonli0226/jas/settings"
	"github.com/jasonli0226/jas/tools"
)

const (
	profileNote = "- [Owner](owner.md) — PROFILE-MEM-8841"
	projectNote = "- [Deploy](deploy.md) — PROJECT-MEM-8842"
)

// memProject is a project directory outside any git repository, resolved
// the way jas sees its root.
func memProject(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	assert.NoErr(t, err)
	return dir
}

// memDirs are the default profile's memory dirs under jasHome for root.
func memDirs(t *testing.T, jasHome, root string) memory.Dirs {
	t.Helper()
	d, err := memory.DirsFor(settings.ProfileDir(jasHome, settings.DefaultProfile), sessions.MainRoot(root))
	assert.NoErr(t, err)
	return d
}

func writeIndex(t *testing.T, dir, text string) string {
	t.Helper()
	assert.NoErr(t, os.MkdirAll(dir, 0o700))
	p := filepath.Join(dir, "MEMORY.md")
	assert.NoErr(t, os.WriteFile(p, []byte(text+"\n"), 0o600))
	return p
}

// memRun runs jas run --json in root with HOME=userHome and the given turns,
// returning stdout, the first request's system prompt and the exit code.
func memRun(t *testing.T, userHome, root string, turns []fake.Turn, args ...string) (out, sys string, code int) {
	t.Helper()
	url, bodies := testutil.FakeOpenAI(t, turns)
	env := append(jasEnv(userHome, url), "GIT_CEILING_DIRECTORIES="+filepath.Dir(root))
	out, errOut, code := runJasIn(t, root, env, append([]string{"run", "--json"}, append(args, "q")...)...)
	if len(bodies()) == 0 {
		t.Fatalf("no request sent (exit %d): %s", code, errOut)
	}
	return out, systemPrompt(t, bodies()[0]), code
}

// lastToolResult is the last tool_result line of a --json run.
func lastToolResult(out string) string {
	var result string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, `"type":"tool_result"`) {
			result = l
		}
	}
	return result
}

// noteWrite is a fake turn writing a valid note into dir.
func noteWrite(t *testing.T, dir string) fake.Turn {
	t.Helper()
	args, err := json.Marshal(map[string]string{
		"path":    filepath.Join(dir, "owner.md"),
		"content": "---\nname: owner\ndescription: who the owner is\ntype: user\n---\nLikes Go.\n",
	})
	assert.NoErr(t, err)
	return fake.Turn{Calls: []fake.Call{{Name: "write", Args: string(args)}}}
}

func contextPaths(t *testing.T, files any) []string {
	t.Helper()
	var out []string
	for _, f := range files.([]any) {
		out = append(out, f.(map[string]any)["path"].(string))
	}
	return out
}

func TestMemory_IndexInSystemPrompt(t *testing.T) {
	userHome, root := t.TempDir(), memProject(t)
	d := memDirs(t, filepath.Join(userHome, ".jas"), root)
	pIdx, qIdx := writeIndex(t, d.Profile, profileNote), writeIndex(t, d.Project, projectNote)
	out, sys, code := memRun(t, userHome, root, []fake.Turn{{Text: "ok"}})
	assert.Eq(t, code, 0)
	shown := "~/.jas/profiles/default/memory" // home as ~/, like the skill catalog
	for _, want := range []string{"PROFILE-MEM-8841", "PROJECT-MEM-8842", shown + "\n", shown + "/projects/" + memory.Key(root), "## Memory"} {
		assert.Contains(t, sys, want)
	}
	assert.NotContains(t, sys, userHome)
	ev := sessionStarted(t, out)
	mem, ok := ev["memory"].(map[string]any)
	if !ok {
		t.Fatalf("session_started has no memory object: %v", ev)
	}
	assert.Eq(t, mem["profile_dir"], any(d.Profile))
	assert.Eq(t, mem["project_dir"], any(d.Project))
	assert.Eq(t, mem["truncated"], any(false))
	if n, _ := mem["index_bytes"].(float64); n <= 0 {
		t.Fatalf("index_bytes = %v, want > 0", mem["index_bytes"])
	}
	paths := strings.Join(contextPaths(t, ev["context_files"]), "\n")
	assert.Contains(t, paths, pIdx)
	assert.Contains(t, paths, qIdx)
}

func TestMemory_OffRemovesEverything(t *testing.T) {
	userHome, root := t.TempDir(), memProject(t)
	jasHome := filepath.Join(userHome, ".jas")
	d := memDirs(t, jasHome, root)
	writeIndex(t, d.Profile, profileNote)
	writeJSON(t, filepath.Join(jasHome, "config.json"), `{"memory":"off"}`)
	out, sys, code := memRun(t, userHome, root, []fake.Turn{noteWrite(t, d.Profile), {Text: "done"}})
	assert.Eq(t, code, 0)
	assert.NotContains(t, sys, "## Memory")
	assert.NotContains(t, sys, "PROFILE-MEM-8841")
	ev := sessionStarted(t, out)
	if _, ok := ev["memory"]; ok {
		t.Fatalf("session_started has a memory field with memory off: %v", ev["memory"])
	}
	assert.NotContains(t, strings.Join(contextPaths(t, ev["context_files"]), "\n"), "MEMORY.md")
	result := lastToolResult(out) // no lift: the path is outside every root jas may write
	assert.Contains(t, result, "outside project root")
	assert.Contains(t, result, `"status":"error"`)
	if _, err := os.Stat(filepath.Join(d.Profile, "owner.md")); err == nil {
		t.Fatal("the note was written with memory off")
	}
}

func TestMemory_ProjectOffHidesProjectOnly(t *testing.T) {
	userHome, root := t.TempDir(), memProject(t)
	d := memDirs(t, filepath.Join(userHome, ".jas"), root)
	writeIndex(t, d.Profile, profileNote)
	writeIndex(t, d.Project, projectNote)
	writeJSON(t, filepath.Join(root, ".jas", "config.json"), `{"memory":"off"}`)
	out, sys, code := memRun(t, userHome, root, []fake.Turn{noteWrite(t, d.Profile), {Text: "done"}}, "--trust-project")
	assert.Eq(t, code, 0)
	assert.Contains(t, sys, "PROFILE-MEM-8841")
	assert.NotContains(t, sys, "PROJECT-MEM-8842")
	assert.NotContains(t, sys, memory.Key(root)) // no project folder named at all
	assert.NotContains(t, sys, d.Project)
	assert.NotContains(t, sys, "Project memory")
	mem := sessionStarted(t, out)["memory"].(map[string]any)
	assert.Eq(t, mem["project_dir"], any(""))
	result := lastToolResult(out)
	assert.Contains(t, result, "read-only")
	assert.Contains(t, result, `"status":"error"`)
}

func TestMemory_HeadlessWriteIsToolError(t *testing.T) {
	userHome, root := t.TempDir(), memProject(t)
	d := memDirs(t, filepath.Join(userHome, ".jas"), root)
	out, sys, code := memRun(t, userHome, root, []fake.Turn{noteWrite(t, d.Profile), {Text: "done"}})
	assert.Eq(t, code, 0)
	assert.Contains(t, sys, "Memory writes are not available")
	result := lastToolResult(out)
	assert.Contains(t, result, "read-only in this run")
	assert.Contains(t, result, `"status":"error"`)
	if _, err := os.Stat(filepath.Join(d.Profile, "owner.md")); err == nil {
		t.Fatal("a headless run wrote a note")
	}
}

func TestMemory_WorktreesShareKey(t *testing.T) {
	userHome := t.TempDir()
	repo, err := filepath.EvalSymlinks(farmRepo(t))
	assert.NoErr(t, err)
	wt := filepath.Join(memProject(t), "wt")
	gitIn(t, repo, "worktree", "add", "-q", "-b", "side", wt)
	projectDir := func(dir string) any {
		url, _ := testutil.FakeOpenAI(t, []fake.Turn{{Text: "ok"}})
		out, errOut, code := runJasIn(t, dir, jasEnv(userHome, url), "run", "--json", "q")
		assert.Eq(t, code, 0)
		mem, ok := sessionStarted(t, out)["memory"].(map[string]any)
		if !ok {
			t.Fatalf("no memory object (stderr %q)", errOut)
		}
		return mem["project_dir"]
	}
	main, linked := projectDir(repo), projectDir(wt)
	assert.Eq(t, linked, main)
	assert.Eq(t, main, any(memDirs(t, filepath.Join(userHome, ".jas"), repo).Project))
}

// memChatEnv is testEnv on the default profile, as prepare builds it for a
// chat, with the start state loaded by reloadContext.
func memChatEnv(t *testing.T) *env {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir()) // the manual skill installs here
	e := testEnv(t)
	e.root = memProject(t)
	e.profile, e.interactive = settings.DefaultProfile, true
	e.policy = core.ExecPolicy{Root: e.root, Deny: denyPatterns(e.home)}
	e.files = e.files.WithProfile(settings.DefaultProfile, settings.File{})
	e.layers, _ = e.files.ForProject(false)
	_, _, err := e.reloadContext(nil)
	assert.NoErr(t, err)
	return e
}

func TestMemory_ReloadPicksUpNewNote(t *testing.T) {
	e := memChatEnv(t)
	b := &chatBackend{e: e}
	path := filepath.Join(t.TempDir(), "events.jsonl")
	log, err := core.OpenEventLog(path, nil, nil)
	assert.NoErr(t, err)
	t.Cleanup(func() { _ = log.Close() })
	s, err := core.NewSession(core.SessionConfig{
		Provider: &fake.Provider{}, Model: "m", System: e.pc.system,
		Exec: &core.Executor{Reg: core.NewRegistry(), Policy: e.policy, Log: log},
		Log:  log, Budget: &core.Budget{Window: 100000},
	})
	assert.NoErr(t, err)
	assert.Contains(t, s.System(), "## Memory")
	assert.NotContains(t, s.System(), "PROFILE-MEM-8841")

	idx := writeIndex(t, e.mem.dirs.Profile, profileNote)
	_, err = b.Reload(s, nil)
	assert.NoErr(t, err)
	assert.Contains(t, s.System(), "PROFILE-MEM-8841")
	assert.NoErr(t, log.Close())
	var files []string
	for _, ev := range testutil.ReadEvents(t, path) {
		if ev["type"] == core.EvContextReloaded {
			files = contextPaths(t, ev["context_files"])
		}
	}
	assert.Contains(t, strings.Join(files, "\n"), idx)
}

func TestMemory_FarmWorkerReadOnly(t *testing.T) {
	e := memChatEnv(t) // interactive: the chat itself could write notes
	assert.Contains(t, e.pc.system, "After adding a note")
	e.provider = &fake.Provider{Turns: []fake.Turn{noteWrite(t, e.mem.dirs.Profile), {Text: "done"}}}
	path := filepath.Join(t.TempDir(), "events.jsonl")
	log, err := core.OpenEventLog(path, nil, nil)
	assert.NoErr(t, err)
	t.Cleanup(func() { _ = log.Close() })
	ws := farm.WorkerSession{Part: farm.Part{ID: "a"}, Root: e.root, Log: log, System: "FARM-PART",
		Tools: tools.SessionTools(false, ""), Gate: core.AllowAll{},
		// The real worker policy: owned paths that don't cover the memory
		// dir, so a preview of the write would fail on the write limit.
		Policy: farm.WorkerPolicy(e.policy, farm.Role{Writes: []core.WriteRule{{All: []string{"src/**"}}}}, e.root)}
	s, err := farmSession(e)(ws)
	assert.NoErr(t, err)
	assert.Contains(t, s.System(), "Memory writes are not available")
	assert.NotContains(t, s.System(), "After adding a note")
	_, err = s.Send(t.Context(), "save a note")
	assert.NoErr(t, err)
	assert.NoErr(t, s.Close("ok", 0))
	var result map[string]any
	for _, ev := range testutil.ReadEvents(t, path) {
		if ev["type"] == core.EvToolResult {
			result = ev
		}
	}
	if result == nil {
		t.Fatal("no tool_result")
	}
	assert.Eq(t, result["status"], any("error"))
	b, _ := json.Marshal(result)
	assert.Contains(t, string(b), "read-only in this run")
}

func TestMemory_EnvSettingIsTheOwnerBase(t *testing.T) {
	userHome, root := t.TempDir(), memProject(t)
	jasHome := filepath.Join(userHome, ".jas")
	writeIndex(t, memDirs(t, jasHome, root).Profile, profileNote)
	writeJSON(t, filepath.Join(jasHome, "config.json"), `{"memory":"off"}`)
	_, sys, code := memRun(t, userHome, root, []fake.Turn{{Text: "ok"}})
	assert.Eq(t, code, 0)
	assert.NotContains(t, sys, "## Memory")

	// JAS_MEMORY beats the global layer...
	url, bodies := testutil.FakeOpenAI(t, []fake.Turn{{Text: "ok"}})
	_, _, code = runJasIn(t, root, append(jasEnv(userHome, url), "JAS_MEMORY=on"), "run", "q")
	assert.Eq(t, code, 0)
	assert.Contains(t, systemPrompt(t, bodies()[0]), "PROFILE-MEM-8841")

	// ...and turns memory off over an unset config.
	assert.NoErr(t, os.Remove(filepath.Join(jasHome, "config.json")))
	url, bodies = testutil.FakeOpenAI(t, []fake.Turn{{Text: "ok"}})
	_, _, code = runJasIn(t, root, append(jasEnv(userHome, url), "JAS_MEMORY=off"), "run", "q")
	assert.Eq(t, code, 0)
	assert.NotContains(t, systemPrompt(t, bodies()[0]), "## Memory")
}

func TestMemorySetting_ProjectStillNarrowsEnv(t *testing.T) {
	l := settings.Layers{Project: settings.File{Values: map[string]string{"memory": "off"}}}
	env := func(k string) string { return map[string]string{"JAS_MEMORY": "on"}[k] }
	mode, hide := memory.ResolveMode(memorySetting(l, env))
	assert.Eq(t, mode, memory.ModeRead)
	assert.Eq(t, hide, true)
}

func TestMemory_SwitchProfileAppliesMemoryNow(t *testing.T) {
	e := memChatEnv(t)
	writeProfile(t, e.home, "ds", nil)
	writeJSON(t, settings.ProfileConfigPath(e.home, "ds"), `{"memory":"off"}`)
	useURL(t, e, e.cfg.BaseURL)
	b := &chatBackend{e: e}
	s := quietSession(t, e)
	assert.Contains(t, s.System(), "## Memory")
	sw, err := b.SwitchProfile(s, "ds", nil)
	assert.NoErr(t, err)
	assert.Eq(t, slices.Contains(sw.Deferred, "memory"), false)
	assert.NotContains(t, s.System(), "## Memory")
	assert.Eq(t, e.policy.ProfileMemory, "")
}

func TestMemory_NewSessionReloadsMemory(t *testing.T) {
	e := memChatEnv(t)
	b := &chatBackend{e: e}
	first, err := b.NewSession("m", core.NewQuietUI(), core.AllowAll{})
	assert.NoErr(t, err)
	assert.NoErr(t, first.Close("ok", 0))
	assert.NotContains(t, first.Session.System(), "PROFILE-MEM-8841")

	idx := writeIndex(t, e.mem.dirs.Profile, profileNote)
	second, err := b.NewSession("m", core.NewQuietUI(), core.AllowAll{})
	assert.NoErr(t, err)
	assert.NoErr(t, second.Close("ok", 0))
	assert.Contains(t, second.Session.System(), "PROFILE-MEM-8841")
	assert.Eq(t, strings.Count(second.Session.System(), "## Memory"), 1)
	ev := testutil.ReadEvents(t, second.Path)[0]
	assert.Contains(t, strings.Join(contextPaths(t, ev["context_files"]), "\n"), idx)

	e.setFile(settings.ScopeGlobal, settings.File{Values: map[string]string{"memory": "off"}})
	third, err := b.NewSession("m", core.NewQuietUI(), core.AllowAll{})
	assert.NoErr(t, err)
	assert.NoErr(t, third.Close("ok", 0))
	assert.NotContains(t, third.Session.System(), "## Memory")
	assert.Eq(t, e.policy.ProfileMemory, "")
	assert.Eq(t, slices.Contains(e.policy.ReadRoots, e.mem.dirs.Profile), false)
	assert.Eq(t, b.memGuard.Load() == nil, true)
	ev = testutil.ReadEvents(t, third.Path)[0]
	if _, ok := ev["memory"]; ok {
		t.Fatal("session_started has memory after memory off + /new")
	}
	assert.NotContains(t, strings.Join(contextPaths(t, ev["context_files"]), "\n"), "MEMORY.md")
}

func TestMemory_IndexLinkedOutsideIsNotLoaded(t *testing.T) {
	userHome, root := t.TempDir(), memProject(t)
	d := memDirs(t, filepath.Join(userHome, ".jas"), root)
	assert.NoErr(t, os.MkdirAll(d.Profile, 0o700))
	outside := filepath.Join(t.TempDir(), "elsewhere.txt")
	assert.NoErr(t, os.WriteFile(outside, []byte("OUTSIDE-MEM-7712\n"), 0o600))
	assert.NoErr(t, os.Symlink(outside, filepath.Join(d.Profile, "MEMORY.md")))
	url, bodies := testutil.FakeOpenAI(t, []fake.Turn{{Text: "ok"}})
	env := append(jasEnv(userHome, url), "GIT_CEILING_DIRECTORIES="+filepath.Dir(root))
	_, stderr, code := runJasIn(t, root, env, "run", "q")
	assert.Eq(t, code, 0)
	assert.NotContains(t, systemPrompt(t, bodies()[0]), "OUTSIDE-MEM-7712")
	assert.Contains(t, stderr, "memory index "+filepath.Join(d.Profile, "MEMORY.md")+" ignored")

	out, _, code := runJasIn(t, root, env, "memory")
	assert.Eq(t, code, 0)
	assert.Contains(t, out, "ignored: it links to")
}
