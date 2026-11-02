package tui

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/providers/fake"
	"github.com/jasonli0226/jas/tools"
)

func TestBridge_TagsGeneration(t *testing.T) {
	var got []tea.Msg
	b := &bridge{gen: 3, send: func(m tea.Msg) { got = append(got, m) }}
	b.Text("a")
	b.Event("tool_started", map[string]any{"tool": "ls"})
	b.Event("x", 5)
	assert.Eq(t, len(got), 2)
	assert.Eq(t, got[0].(textMsg), textMsg{gen: 3, s: "a"})
	ev := got[1].(eventMsg)
	assert.Eq(t, ev.gen, 3)
	assert.Eq(t, ev.typ, "tool_started")
	assert.Eq(t, ev.data["tool"], "ls")
}

// testBackend is a Backend over a fake provider and temp dirs. It registers
// the read-only and mutating tools with the policy root set to root.
type testBackend struct {
	t           *testing.T
	root        string
	sessionsDir string
	prov        *fake.Provider
	redact      func(string) string // the redacting UI's mask, as cmd/jas shares it with Options.Redact; nil is identity
	window      int                 // context window; 0 is testWindow
	provider    string              // what ProviderOf reports; "" is unknown, so /model never refuses
	keep        int                 // compaction keep tokens; 0 is core.DefaultCompactKeep
	closes      atomic.Int32        // calls to Live.Close, across all sessions; Close runs from tea commands
	opens       atomic.Int32        // sessions handed out as a Live
}

const testWindow = 100000

func newTestBackend(t *testing.T, turns ...fake.Turn) *testBackend {
	t.Helper()
	b := &testBackend{
		t: t, root: t.TempDir(), sessionsDir: t.TempDir(),
		prov: &fake.Provider{Turns: turns},
	}
	// Runs before the temp dirs are removed: an event log left open there
	// fails the removal on Windows.
	t.Cleanup(func() {
		if opens, closes := b.opens.Load(), b.closes.Load(); opens != closes {
			t.Errorf("%d sessions opened, %d closed", opens, closes)
		}
	})
	return b
}

func (b *testBackend) config(path, model string, log core.EventLog, ui core.UI, gate core.Gate) (core.SessionConfig, *core.Budget) {
	// Same payload shape as the real backend: events pass the redacting UI.
	redact := b.redact
	if redact == nil {
		redact = func(s string) string { return s }
	}
	ui = core.NewRedactingUI(ui, redact)
	log = core.NewUILog(log, ui)
	reg := core.NewRegistry(append(tools.ReadOnly(), tools.Mutating()...)...)
	ex := &core.Executor{Reg: reg, Policy: core.ExecPolicy{Root: b.root}, Gate: gate, Log: log}
	bud := &core.Budget{Window: b.Window(model)}
	keep := b.keep
	if keep == 0 {
		keep = core.DefaultCompactKeep
	}
	return core.SessionConfig{
		Provider: b.prov, Model: model, System: core.BuildSystem(core.BasePrompt, ""),
		Exec: ex, Log: log, UI: ui, Budget: bud,
		Compact: &core.CompactConfig{Reserve: core.DefaultCompactReserve, Keep: keep},
		Meta:    map[string]any{"base_url": "http://x", "approve": "never", "root": b.root, "context_files": []string{}},
	}, bud
}

func (b *testBackend) live(path, model string, log core.EventLog, s *core.Session, bud *core.Budget) *Live {
	b.opens.Add(1)
	return &Live{
		Session: s, ID: filepath.Base(filepath.Dir(path)), Path: path, Model: model, Budget: bud,
		Close: func(status string, code int) error {
			b.closes.Add(1)
			err := s.Close(status, code)
			return errors.Join(err, log.Close())
		},
	}
}

func (b *testBackend) NewSession(model string, ui core.UI, gate core.Gate) (*Live, error) {
	path := filepath.Join(b.sessionsDir, core.NewSessionID(time.Now()), "events.jsonl")
	log, err := core.OpenEventLog(path, nil, nil)
	if err != nil {
		return nil, err
	}
	cfg, bud := b.config(path, model, log, ui, gate)
	s, err := core.NewSession(cfg)
	if err != nil {
		_ = log.Close()
		return nil, err
	}
	return b.live(path, model, log, s, bud), nil
}

func (b *testBackend) Resume(path string, ui core.UI, gate core.Gate) (*Live, []string, error) {
	st, err := core.Replay(path)
	if err != nil {
		return nil, nil, err
	}
	log, err := core.OpenEventLog(path, nil, nil)
	if err != nil {
		return nil, nil, err
	}
	model, notice := core.ResumeModel(st, "http://x", "m")
	cfg, bud := b.config(path, model, log, ui, gate)
	s, err := core.ResumeSession(cfg, st)
	if err != nil {
		_ = log.Close()
		return nil, nil, err
	}
	notices := st.Notices
	if notice != "" {
		notices = append(slices.Clone(notices), notice)
	}
	return b.live(path, model, log, s, bud), notices, nil
}

func (b *testBackend) Sessions(exclude string) ([]core.SessionInfo, error) {
	all, err := core.ListSessions(b.sessionsDir, b.root, 0)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(all, func(i core.SessionInfo) bool {
		return i.Path == exclude
	}), nil
}

func (b *testBackend) Models() []string         { return []string{"m", "m2"} }
func (b *testBackend) ProviderOf(string) string { return b.provider }
func (b *testBackend) SkillNames() []string     { return []string{"review"} }
func (b *testBackend) SkillFiles() map[string]string {
	return map[string]string{"/skills/review/SKILL.md": "review"}
}

func (b *testBackend) Window(string) int {
	if b.window > 0 {
		return b.window
	}
	return testWindow
}

func (b *testBackend) SkillPrompt(name, text string) (string, bool) {
	if name != "review" {
		return "", false
	}
	return "SKILL review: " + text, true
}

var _ Backend = (*testBackend)(nil)

func TestTestBackend_SessionRoundTrip(t *testing.T) {
	b := newTestBackend(t, fake.Turn{Text: "A"}, fake.Turn{Text: "B"})
	live, err := b.NewSession("m", core.NewQuietUI(), core.AllowAll{})
	assert.NoErr(t, err)
	res, err := live.Session.Send(context.Background(), "hi")
	assert.NoErr(t, err)
	assert.Eq(t, res.Final, "A")
	assert.NoErr(t, live.Close(res.Status, res.Code))
	assert.Eq(t, b.closes.Load(), 1)

	infos, err := b.Sessions("")
	assert.NoErr(t, err)
	assert.Eq(t, len(infos), 1)
	assert.Eq(t, infos[0].FirstMessage, "hi")
	other, err := b.Sessions(live.Path)
	assert.NoErr(t, err)
	assert.Eq(t, len(other), 0)

	live2, notices, err := b.Resume(live.Path, core.NewQuietUI(), core.AllowAll{})
	assert.NoErr(t, err)
	assert.Eq(t, len(notices), 0)
	res, err = live2.Session.Send(context.Background(), "again")
	assert.NoErr(t, err)
	assert.Eq(t, res.Final, "B")
	assert.NoErr(t, live2.Close(res.Status, res.Code))
	assert.Eq(t, b.closes.Load(), 2)
}
