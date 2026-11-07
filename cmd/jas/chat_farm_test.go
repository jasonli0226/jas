package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/farm"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/internal/testutil"
	"github.com/jasonli0226/jas/providers/fake"
	"github.com/jasonli0226/jas/settings"
	"github.com/jasonli0226/jas/tui"
)

// farmChat is a chat backend on a trusted farm repo whose provider is a fake
// OpenAI server playing turns.
func farmChat(t *testing.T, check string, trusted bool, turns ...fake.Turn) *chatBackend {
	t.Helper()
	repo, home := farmRepo(t), farmHome(t, check)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	url, _ := testutil.FakeOpenAI(t, turns)
	e := testEnv(t)
	e.root, e.home = repo, home
	e.cfg = settings.Run{Provider: settings.ProviderCustom, Model: "m", BaseURL: url, Approve: "never", MaxTurns: 10}
	e.policy = core.ExecPolicy{Root: repo}
	e.pc.trusted = trusted
	return &chatBackend{e: e}
}

func TestChatFarm_AvailableNeedsTrustAndCheck(t *testing.T) {
	ok, why := farmChat(t, "go test ./...", false).Farmer().Available()
	assert.Eq(t, ok, false)
	assert.Contains(t, why, "not trusted")
	ok, why = farmChat(t, "", true).Farmer().Available()
	assert.Eq(t, ok, false)
	assert.Contains(t, why, "no check")
	ok, _ = farmChat(t, "go test ./...", true).Farmer().Available()
	assert.Eq(t, ok, true)
}

func TestChatFarm_PlanRefusal(t *testing.T) {
	refuse := "refuse:\n  reason: small_change\n  detail: one typo\nsuggest: 'jas run \"fix typo\"'\n"
	f := farmChat(t, "go test ./...", true, fake.Turn{Text: fenceYAML(refuse)}).Farmer()
	p, err := f.Plan(context.Background(), "fix typo", "m")
	assert.NoErr(t, err)
	assert.Eq(t, p.Refusal, "small_change")
	assert.Contains(t, p.Suggest, "fix typo")
	_, err = f.Approve(p)
	if err == nil {
		t.Fatal("approved a refused plan")
	}
}

// hookRec records farm hook calls.
type hookRec struct {
	mu     sync.Mutex
	events []string
	text   map[string]*strings.Builder
}

func (h *hookRec) hooks() tui.FarmHooks {
	h.text = map[string]*strings.Builder{}
	return tui.FarmHooks{
		Event: func(typ string, m map[string]any) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.events = append(h.events, typ)
		},
		Worker: func(part string) core.UI {
			h.mu.Lock()
			defer h.mu.Unlock()
			b := &strings.Builder{}
			h.text[part] = b
			return &lockedTextUI{mu: &h.mu, b: b}
		},
	}
}

type lockedTextUI struct {
	mu *sync.Mutex
	b  *strings.Builder
}

func (u *lockedTextUI) Text(d string)     { u.mu.Lock(); u.b.WriteString(d); u.mu.Unlock() }
func (u *lockedTextUI) Reasoning(string)  {}
func (u *lockedTextUI) Event(string, any) {}

func TestChatFarm_ApproveThenStart(t *testing.T) {
	b := farmChat(t, "true", true,
		fake.Turn{Text: fenceYAML(farmPlan)}, fake.Turn{Text: "a done"}, fake.Turn{Text: "b done"})
	f := b.Farmer()
	p, err := f.Plan(context.Background(), "split it", "m")
	assert.NoErr(t, err)
	assert.Eq(t, p.Refusal, "")
	assert.Contains(t, strings.Join(p.Summary, "\n"), "owns core/**")
	path, err := f.Approve(p)
	assert.NoErr(t, err)
	if !strings.HasPrefix(path, filepath.Join(b.e.home, "farms")) {
		t.Fatalf("plan written to %s, not under the farm dir", path)
	}
	if _, err := os.Stat(filepath.Join(b.e.root, "plan.yaml")); err == nil {
		t.Fatal("plan written into the repo")
	}
	rec := &hookRec{}
	run, err := f.Start(context.Background(), path, "m", core.Never, rec.hooks())
	assert.NoErr(t, err)
	res := run.Wait()
	if res.Code != 0 {
		t.Fatalf("result %+v", res)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if !strings.Contains(strings.Join(rec.events, " "), farm.EvPartStarted) {
		t.Fatalf("events %v", rec.events)
	}
	got := rec.text["a"].String() + rec.text["b"].String()
	if !strings.Contains(got, "a done") || !strings.Contains(got, "b done") {
		t.Fatalf("worker text %q", got)
	}
}

func TestSuggestFarm_RegisteredInEveryChat(t *testing.T) {
	has := func(b *chatBackend) bool {
		log, err := core.OpenEventLog(filepath.Join(t.TempDir(), "e.jsonl"), nil, nil)
		assert.NoErr(t, err)
		defer log.Close()
		c := b.configProv(nil, "custom", "", "m", log, core.NewQuietUI(), core.AllowAll{})
		_, ok := c.Exec.Reg.Get(suggestFarmName)
		return ok
	}
	assert.Eq(t, has(farmChat(t, "go test ./...", true)), true)
	assert.Eq(t, has(farmChat(t, "go test ./...", false)), true)
	assert.Eq(t, has(farmChat(t, "", true)), true)
}

func TestSuggestFarm_PostsTaskToTheChat(t *testing.T) {
	ui := &eventUI{}
	tool := suggestFarm{ui: ui, available: func() (bool, string) { return true, "" }}
	res, err := tool.Call(context.Background(), core.ExecPolicy{}, []byte(`{"task":"migrate logging"}`))
	assert.NoErr(t, err)
	assert.Contains(t, res.Output, `suggested to the owner: /farm "migrate logging"`)
	assert.Eq(t, ui.typ, tui.EvSuggestFarm)
	assert.Eq(t, ui.data["task"], any("migrate logging"))
	if _, err := tool.Call(context.Background(), core.ExecPolicy{}, []byte(`{"task":"  "}`)); err == nil {
		t.Fatal("an empty task was accepted")
	}
}

func TestSuggestFarm_UnavailableSaysWhyAndPostsNothing(t *testing.T) {
	ui := &eventUI{}
	b := farmChat(t, "go test ./...", false)
	tool := suggestFarm{ui: ui, available: b.Farmer().Available}
	_, err := tool.Call(context.Background(), core.ExecPolicy{}, []byte(`{"task":"migrate logging"}`))
	if err == nil {
		t.Fatal("suggested a farm in an untrusted project")
	}
	assert.Contains(t, err.Error(), "--trust-project")
	assert.Contains(t, err.Error(), "do not run jas farm through shell")
	assert.Contains(t, err.Error(), "Stop and ask the owner")
	assert.Eq(t, ui.typ, "")
}

// A check added during the chat makes the farm available without a restart:
// availability is decided at call time, not when the session starts.
func TestSuggestFarm_CheckAddedMidChat(t *testing.T) {
	b := farmChat(t, "", true)
	tool := suggestFarm{ui: &eventUI{}, available: b.Farmer().Available}
	_, err := tool.Call(context.Background(), core.ExecPolicy{}, []byte(`{"task":"x"}`))
	if err == nil {
		t.Fatal("suggested a farm without a check")
	}
	assert.Contains(t, err.Error(), "offer to set up a check")
	assert.NoErr(t, os.WriteFile(filepath.Join(b.e.home, "farm.yaml"), []byte("check: go test ./...\n"), 0o644))
	_, err = tool.Call(context.Background(), core.ExecPolicy{}, []byte(`{"task":"x"}`))
	assert.NoErr(t, err)
}

// A dirty tree or a repository with no commit cannot be planned: the agent
// hears why before the owner is asked, so it can offer to commit first.
func TestSuggestFarm_TreeMustBeCommitted(t *testing.T) {
	b := farmChat(t, "go test ./...", true)
	ui, f := &eventUI{}, b.Farmer()
	tool := suggestFarm{ui: ui, available: f.Available, tree: b.farmer.TreeReady}
	_, err := tool.Call(context.Background(), core.ExecPolicy{}, []byte(`{"task":"x"}`))
	assert.NoErr(t, err)

	assert.NoErr(t, os.WriteFile(filepath.Join(b.e.root, "new.sh"), []byte("echo\n"), 0o644))
	ui.typ = ""
	_, err = tool.Call(context.Background(), core.ExecPolicy{}, []byte(`{"task":"x"}`))
	if err == nil {
		t.Fatal("suggested a farm on a dirty tree")
	}
	assert.Contains(t, err.Error(), "uncommitted changes")
	assert.Contains(t, err.Error(), "offer to commit")
	assert.Eq(t, ui.typ, "")

	assert.NoErr(t, os.RemoveAll(filepath.Join(b.e.root, ".git")))
	gitIn(t, b.e.root, "init", "-q")
	_, err = tool.Call(context.Background(), core.ExecPolicy{}, []byte(`{"task":"x"}`))
	if err == nil {
		t.Fatal("suggested a farm in a repository with no commit")
	}
	assert.Contains(t, err.Error(), "no commit yet")
}

type eventUI struct {
	typ  string
	data map[string]any
}

func (u *eventUI) Text(string)      {}
func (u *eventUI) Reasoning(string) {}
func (u *eventUI) Event(typ string, data any) {
	u.typ, u.data = typ, data.(map[string]any)
}
