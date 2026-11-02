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
	assert.Eq(t, ev["approve"].(string), "never")
	assert.Eq(t, ev["root"].(string), b.e.root)
	assert.Eq(t, ev["context_window"].(float64), float64(settings.DefaultContextWindow))
	assert.Eq(t, ev["window_known"].(bool), false)
	assert.Eq(t, saved.Path, filepath.Join(b.e.home, sessionsDir, saved.ID, eventsFile))
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
	e.file = settings.File{Models: map[string]settings.ModelOverride{"zz": {ContextWindow: 1000}, "m": {ContextWindow: 2000}}}
	b := &chatBackend{e: e}
	assert.Eq(t, strings.Join(b.Models(), ","), "m,zz")
	assert.Eq(t, b.Window("zz"), 1000)
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
