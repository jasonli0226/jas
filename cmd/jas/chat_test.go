package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/internal/testutil"
	"github.com/jasonli0226/jas/providers/fake"
	"github.com/jasonli0226/jas/settings"
	"github.com/jasonli0226/jas/skills"
	"github.com/jasonli0226/jas/tui"
)

const chatTestKey = "zq9chatbackendkey5150"

// testEnv is an env over temp dirs and a fake provider, as prepare would build
// it for a project with no context file.
func testEnv(t *testing.T, turns ...fake.Turn) *env {
	t.Helper()
	root := t.TempDir()
	red := newRedactor(chatTestKey)
	return &env{
		cfg:       settings.Run{Provider: settings.ProviderCustom, Model: "m", BaseURL: "http://x/v1", Approve: "never"},
		key:       chatTestKey,
		root:      root,
		home:      t.TempDir(),
		red:       red,
		policy:    core.ExecPolicy{Root: root},
		pc:        projectContext{system: core.BuildSystem(core.BasePrompt, ""), files: []contextFileMeta{}},
		skillList: []skillMeta{},
		provider:  &fake.Provider{Turns: turns},
	}
}

// saveSession writes a session with one user message and closes it.
func saveSession(t *testing.T, b *chatBackend, model string) *tui.Live {
	t.Helper()
	live, err := b.NewSession(model, core.NewQuietUI(), core.AllowAll{})
	assert.NoErr(t, err)
	res, err := live.Session.Send(context.Background(), "hello")
	assert.NoErr(t, err)
	assert.NoErr(t, live.Close(res.Status, res.Code))
	return live
}

func TestChatCmd_NeedsTerminal(t *testing.T) {
	home := testutil.TempHome(t)
	assert.NoErr(t, os.WriteFile(filepath.Join(home, configFile), []byte("{}\n"), 0o600))
	old := isTerminal
	isTerminal = func(int) bool { return false }
	defer func() { isTerminal = old }()
	errOut := captureStderr(t, func() {
		assert.Eq(t, dispatch(context.Background(), nil), core.ExitUsage)
	})
	assert.Contains(t, errOut, "the chat needs a terminal")
}

func TestChatCmd_PositionalArgIsUsageError(t *testing.T) {
	_, err := parseChat([]string{"--continue", "fix it"})
	var ee *core.ExitError
	if !errors.As(err, &ee) || ee.Code != core.ExitUsage {
		t.Fatalf("want usage error, got %v", err)
	}
	assert.Contains(t, err.Error(), `jas run "task"`)
}

func TestChatCmd_NameFlag(t *testing.T) {
	ca, err := parseChat([]string{"--name", "login bug"})
	assert.NoErr(t, err)
	assert.Eq(t, ca.name, "login bug")
	_, ok := ca.flags["name"]
	assert.Eq(t, ok, false)
	_, err = parseChat([]string{"--name", "  "})
	var ee *core.ExitError
	if !errors.As(err, &ee) || ee.Code != core.ExitUsage {
		t.Fatalf("want usage error for a blank --name, got %v", err)
	}
}

func TestChatCmd_HelpPrintsUsage(t *testing.T) {
	errOut := captureStderr(t, func() {
		assert.Eq(t, dispatch(context.Background(), []string{"--help"}), core.ExitOK)
	})
	assert.Contains(t, errOut, "jas [--continue]")
}

func TestChatBackend_NewSessionMetaMatchesRun(t *testing.T) {
	b := &chatBackend{e: testEnv(t, fake.Turn{Text: "a"})}
	saved := saveSession(t, b, "m")
	ev := testutil.ReadEvents(t, saved.Path)[0]
	assert.Eq(t, ev["type"].(string), core.EvSessionStarted)
	assert.Eq(t, ev["base_url"].(string), "http://x/v1")
	assert.Eq(t, ev["provider"].(string), settings.ProviderCustom)
	assert.Eq(t, ev["approve"].(string), "never")
	assert.Eq(t, ev["root"].(string), b.e.root)
	assert.Eq(t, ev["context_window"].(float64), float64(settings.DefaultContextWindow))
	assert.Eq(t, ev["window_known"].(bool), false)
	assert.Eq(t, saved.Path, filepath.Join(b.e.home, sessionsDir, saved.ID, eventsFile))
}

// A qualified model's session logs the provider it resolved to, and a /model
// switch logs the same provider and base_url next to the bare model (#97).
func TestChatBackend_QualifiedModelLogsProvider(t *testing.T) {
	e := testEnv(t)
	e.layers.Global = settings.File{Providers: map[string]settings.ProviderEntry{"vllm": {BaseURL: "http://vllm/v1"}}}
	b := &chatBackend{e: e}
	live, err := b.NewSession("vllm/qwen3", core.NewQuietUI(), core.AllowAll{})
	assert.NoErr(t, err)
	assert.NoErr(t, live.Session.SetModel("qwen3-coder"))
	assert.NoErr(t, live.Close("ok", 0))
	var started, changed map[string]any
	for _, ev := range testutil.ReadEvents(t, live.Path) {
		switch ev["type"] {
		case core.EvSessionStarted:
			started = ev
		case core.EvModelChanged:
			changed = ev
		}
	}
	assert.Eq(t, started["provider"].(string), "vllm")
	assert.Eq(t, started["base_url"].(string), "http://vllm/v1")
	assert.Eq(t, started["model"].(string), "qwen3")
	assert.Eq(t, changed["provider"].(string), "vllm")
	assert.Eq(t, changed["base_url"].(string), "http://vllm/v1")
	assert.Eq(t, changed["model"].(string), "qwen3-coder")
	st, err := core.Replay(live.Path)
	assert.NoErr(t, err)
	assert.Eq(t, st.Provider, "vllm")
}

func TestChatBackend_SetDefaultModelPersists(t *testing.T) {
	b := &chatBackend{e: testEnv(t)}
	assert.NoErr(t, b.SetDefaultModel("openrouter/gpt-5.2"))
	raw, err := os.ReadFile(filepath.Join(b.e.home, configFile))
	assert.NoErr(t, err)
	assert.Contains(t, string(raw), `"model": "openrouter/gpt-5.2"`)
	assert.Eq(t, b.e.cfg.Model, "openrouter/gpt-5.2")
	assert.Contains(t, strings.Join(b.Models(), ","), "openrouter/gpt-5.2")
}

func TestChatBackend_ResumeLockedSession(t *testing.T) {
	b := &chatBackend{e: testEnv(t, fake.Turn{Text: "a"})}
	saved := saveSession(t, b, "m")
	held, err := core.OpenEventLog(saved.Path, nil, nil)
	assert.NoErr(t, err)
	defer held.Close()
	_, _, err = b.Resume(saved.Path, core.NewQuietUI(), core.AllowAll{})
	if !errors.Is(err, core.ErrSessionLocked) {
		t.Fatalf("want ErrSessionLocked, got %v", err)
	}
	assert.Contains(t, err.Error(), saved.ID)
}

func TestChatBackend_ResumeUsesFileModelOnSameBaseURL(t *testing.T) {
	b := &chatBackend{e: testEnv(t, fake.Turn{Text: "a"})}
	saved := saveSession(t, b, "z-ai/glm-x")
	live, notices, err := b.Resume(saved.Path, core.NewQuietUI(), core.AllowAll{})
	assert.NoErr(t, err)
	defer live.Close("ok", core.ExitOK)
	assert.Eq(t, live.Model, "z-ai/glm-x")
	assert.Eq(t, len(notices), 0)
	assert.Eq(t, live.ID, saved.ID)
}

func TestChatBackend_SessionsExcludesCurrent(t *testing.T) {
	b := &chatBackend{e: testEnv(t, fake.Turn{Text: "a"}, fake.Turn{Text: "b"})}
	first := saveSession(t, b, "m")
	second := saveSession(t, b, "m")
	all, err := b.Sessions("")
	assert.NoErr(t, err)
	assert.Eq(t, len(all), 2)
	got, err := b.Sessions(filepath.Join(filepath.Dir(first.Path), ".", eventsFile))
	assert.NoErr(t, err)
	assert.Eq(t, len(got), 1)
	assert.Eq(t, got[0].ID, second.ID)
}

func TestChatBackend_SkillPromptRedacts(t *testing.T) {
	e := testEnv(t)
	e.pc.skills = []skills.Skill{
		{Name: "deploy", Location: "/s/deploy/SKILL.md", Content: []byte("use key " + chatTestKey + "\n")},
		{Name: "manual", Location: "/s/manual/SKILL.md", Content: []byte("by hand\n"), DisableModelInvocation: true},
	}
	b := &chatBackend{e: e}
	assert.Eq(t, strings.Join(b.SkillNames(), ","), "deploy,manual")
	p, ok := b.SkillPrompt("deploy", "ship it")
	assert.Eq(t, ok, true)
	assert.NotContains(t, p, chatTestKey)
	assert.Contains(t, p, core.RedactedMarker)
	assert.Contains(t, p, "Use the \"deploy\" skill for this request. Its instructions (/s/deploy/SKILL.md):\n\nuse key ")
	assert.Contains(t, p, "\n\nship it")
	p, ok = b.SkillPrompt("manual", "")
	assert.Eq(t, ok, true)
	assert.Eq(t, p, "Use the \"manual\" skill for this request. Its instructions (/s/manual/SKILL.md):\n\nby hand")
	_, ok = b.SkillPrompt("nope", "")
	assert.Eq(t, ok, false)
}

func TestChatBackend_SkillFilesMapsLocationToName(t *testing.T) {
	e := testEnv(t)
	e.pc.skills = []skills.Skill{
		{Name: "deploy", Location: "/s/deploy/SKILL.md"},
		{Name: "manual", Location: "/s/manual/SKILL.md", DisableModelInvocation: true},
	}
	files := (&chatBackend{e: e}).SkillFiles()
	assert.Eq(t, len(files), 2)
	assert.Eq(t, files["/s/deploy/SKILL.md"], "deploy")
	assert.Eq(t, files["/s/manual/SKILL.md"], "manual")
}

func TestChatBackend_ModelsUnion(t *testing.T) {
	e := testEnv(t)
	e.layers = settings.Layers{Global: settings.File{Models: map[string]settings.ModelOverride{"zz": {ContextWindow: 1000}, "m": {ContextWindow: 2000}}}}
	b := &chatBackend{e: e}
	assert.Eq(t, strings.Join(b.Models(), ","), "m,zz")
	w, known := b.Window("zz")
	assert.Eq(t, w, 1000)
	assert.Eq(t, known, true)
	_, known = b.Window("not-in-catalog")
	assert.Eq(t, known, false)
}

func TestChatBackend_SettingsScopes(t *testing.T) {
	e := testEnv(t)
	e.layers = settings.Layers{}
	e.flags = map[string]string{}
	e.settingsTrusted = true
	// Need a buildable config after writes.
	g, err := settings.File{}.Set("model", "m")
	assert.NoErr(t, err)
	g, err = g.Set("provider", "custom")
	assert.NoErr(t, err)
	g, err = g.Set("base_url", "http://x/v1")
	assert.NoErr(t, err)
	assert.NoErr(t, g.Save(settings.GlobalConfigPath(e.home)))
	e.layers.Global, e.files.Global = g, g
	b := &chatBackend{e: e}
	assert.Eq(t, b.DefaultSettingsScope(), "local")
	assert.NoErr(t, b.SetSetting("", "approve", "shell"))
	raw, err := os.ReadFile(settings.LocalConfigPath(e.root))
	assert.NoErr(t, err)
	assert.Contains(t, string(raw), `"approve": "shell"`)
	assert.Eq(t, e.cfg.Approve, "shell")
	assert.NoErr(t, b.SetSetting("project", "max_turns", "9"))
	raw, err = os.ReadFile(settings.ProjectConfigPath(e.root))
	assert.NoErr(t, err)
	assert.Contains(t, string(raw), `"max_turns": "9"`)
	line, err := b.GetSetting("approve")
	assert.NoErr(t, err)
	assert.Eq(t, line.Source, "local config")
	assert.NoErr(t, b.UnsetSetting("local", "approve"))
	line, err = b.GetSetting("approve")
	assert.NoErr(t, err)
	assert.Eq(t, line.Source, "default")
	lines := b.ListSettings()
	found := false
	for _, l := range lines {
		if l.Key == "max_turns" {
			assert.Eq(t, l.Value, "9")
			assert.Eq(t, l.Source, "project config")
			found = true
		}
	}
	assert.Eq(t, found, true)
}

// BareModel strips a known provider prefix and leaves OpenRouter-style ids
// ("z-ai/glm-5.3-flash" names no provider) and bare ids alone.
func TestChatBackend_BareModel(t *testing.T) {
	b := &chatBackend{e: testEnv(t)}
	assert.Eq(t, b.BareModel("openrouter/z-ai/glm-5.3-flash"), "z-ai/glm-5.3-flash")
	assert.Eq(t, b.BareModel("z-ai/glm-5.3-flash"), "z-ai/glm-5.3-flash")
	assert.Eq(t, b.BareModel("glm-5.3-flash"), "glm-5.3-flash")
}

// captureStderr runs fn with os.Stderr sent to a temp file and returns what it wrote.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stderr")
	assert.NoErr(t, err)
	defer f.Close()
	old := os.Stderr
	os.Stderr = f
	defer func() { os.Stderr = old }()
	fn()
	b, err := os.ReadFile(f.Name())
	assert.NoErr(t, err)
	return string(b)
}

func TestChatCmd_CancelledBeforeChatIsInterrupted(t *testing.T) {
	home := testutil.TempHome(t)
	assert.NoErr(t, os.WriteFile(filepath.Join(home, configFile), []byte("{}\n"), 0o600))
	t.Setenv("JAS_MODEL", "m")
	t.Setenv("JAS_API_KEY", "k")
	t.Setenv("JAS_BASE_URL", "http://127.0.0.1:1/v1")
	t.Chdir(t.TempDir())
	old := isTerminal
	isTerminal = func(int) bool { return true }
	defer func() { isTerminal = old }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // as if Ctrl-C came during the trust prompt
	errOut := captureStderr(t, func() {
		assert.Eq(t, dispatch(ctx, nil), core.ExitInterrupted)
	})
	assert.Eq(t, errOut, "")
	_, err := os.Stat(filepath.Join(home, sessionsDir))
	assert.Eq(t, errors.Is(err, os.ErrNotExist), true)
}

func TestChatExit_KilledByCancelIsInterrupted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	killed := fmt.Errorf("run: %w", tea.ErrProgramKilled)
	assert.Eq(t, chatExit(ctx, killed), killed) // not cancelled: a real failure
	cancel()
	var ee *core.ExitError
	if !errors.As(chatExit(ctx, killed), &ee) || ee.Code != core.ExitInterrupted {
		t.Fatalf("want interrupted, got %v", chatExit(ctx, killed))
	}
	other := errors.New("boom")
	assert.Eq(t, chatExit(ctx, other), other)
	assert.Eq(t, chatExit(ctx, nil), nil)
}

func TestChatBackend_EnablesCompaction(t *testing.T) {
	e := testEnv(t)
	e.cfg.CompactReserve, e.cfg.CompactKeep, e.cfg.CompactModel = 8000, 4000, "z-ai/glm-x"
	b := &chatBackend{e: e}
	c := b.configProv(e.provider, e.cfg.Provider, e.cfg.BaseURL, "m", nil, core.NewQuietUI(), core.AllowAll{})
	if c.Compact == nil {
		t.Fatal("chat sessions should have compaction on")
	}
	assert.Eq(t, *c.Compact, core.CompactConfig{Reserve: 8000, Keep: 4000, Model: "z-ai/glm-x"})
	assert.Eq(t, settings.DefaultCompactReserve, core.DefaultCompactReserve)
	assert.Eq(t, settings.DefaultCompactKeep, core.DefaultCompactKeep)
}

func TestChatBackend_RegistersRecord(t *testing.T) {
	b := &chatBackend{e: testEnv(t, fake.Turn{Text: "a"})}
	saved := saveSession(t, b, "m")
	ev := testutil.ReadEvents(t, saved.Path)[0]
	recPath, _ := ev["record_path"].(string)
	if recPath == "" {
		t.Fatal("session_started has no record_path")
	}
	raw, err := os.ReadFile(recPath)
	assert.NoErr(t, err)
	assert.Contains(t, string(raw), `"mode": "chat"`)
	assert.Contains(t, string(raw), `"state": "ended"`)
}

func TestChatBackend_SkillFilesKnowTheCatalogForm(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	real, err := filepath.EvalSymlinks(home)
	assert.NoErr(t, err)
	e := testEnv(t)
	loc := filepath.Join(real, ".agents", "skills", "deploy", "SKILL.md")
	e.pc.skills = []skills.Skill{{Name: "deploy", Location: loc}}
	files := (&chatBackend{e: e}).SkillFiles()
	assert.Eq(t, files[loc], "deploy")
	assert.Eq(t, files["~/.agents/skills/deploy/SKILL.md"], "deploy") // how the catalog shows it
}

func TestChatBackend_SkillPromptWritesHomeAsTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	real, err := filepath.EvalSymlinks(home)
	assert.NoErr(t, err)
	e := testEnv(t)
	e.pc.skills = []skills.Skill{{Name: "deploy", Location: filepath.Join(real, "s", "deploy", "SKILL.md"), Content: []byte("steps")}}
	p, ok := (&chatBackend{e: e}).SkillPrompt("deploy", "")
	assert.Eq(t, ok, true)
	assert.Contains(t, p, "(~/s/deploy/SKILL.md)")
	assert.NotContains(t, p, real)
}

func TestChatExit_KillKeepsCloseErrorAsWarning(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var warned []string
	err := chatExitWarn(ctx, errors.Join(tea.ErrProgramKilled, errors.New("close session: disk full")),
		func(w string) { warned = append(warned, w) })
	var ee *core.ExitError
	if !errors.As(err, &ee) || ee.Code != core.ExitInterrupted {
		t.Fatalf("want interrupted, got %v", err)
	}
	assert.Eq(t, len(warned), 1)
	assert.Contains(t, warned[0], "close session: disk full")
}

func TestRemoveUnstartedSession(t *testing.T) {
	home := t.TempDir()
	for _, c := range []struct {
		name, content string
		removed       bool
	}{
		{"empty", "", true},
		{"torn", `{"v":1,"id":"e000001","type":"sess`, true},
		{"started", `{"v":1,"id":"e000001","seq":1,"type":"session_started"}` + "\n", false},
	} {
		dir := filepath.Join(home, c.name)
		assert.NoErr(t, os.MkdirAll(dir, 0o700))
		path := filepath.Join(dir, eventsFile)
		assert.NoErr(t, os.WriteFile(path, []byte(c.content), 0o600))
		removeUnstartedSession(path)
		_, err := os.Stat(dir)
		assert.Eq(t, os.IsNotExist(err), c.removed)
	}
}
