package tui

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
	t            *testing.T
	root         string
	sessionsDir  string
	prov         *fake.Provider
	redact       func(string) string    // the redacting UI's mask, as cmd/jas shares it with Options.Redact; nil is identity
	window       int                    // context window; 0 is testWindow
	windowGuess  bool                   // Window reports the window as unknown
	maxTurns     int                    // per-message turn cap; 0 = default
	provider     string                 // what ProviderOf reports; "" is unknown, so /model never refuses
	keep         int                    // compaction keep tokens; 0 is core.DefaultCompactKeep
	ctxFiles     []ContextFile          // the start banner's [Context] rows; nil hides the section
	closes       atomic.Int32           // calls to Live.Close, across all sessions; Close runs from tea commands
	opens        atomic.Int32           // sessions handed out as a Live
	defaults     []string               // models stored as the config default, in order
	farmer       Farmer                 // the farm port; nil: farms unavailable
	reloadSystem string                 // SetSystem text for Reload; empty uses a default
	reloadAsk    []string               // prompts Reload passed to ask, in order
	reloadRoots  []string               // SetReadRoots after Reload; nil leaves roots alone
	settings     map[string]SettingLine // effective settings for /settings tests
	setCalls     []string               // "scope key=value" or "unset scope key"
	profile      string                 // active profile; "" is default
	profiles     []string               // Profiles; nil is just default
	profDefaults []string               // profiles stored as the config default, in order
	profSwitch   ProfileSwitch          // what SwitchProfile reports
	profErr      error                  // SwitchProfile's error
	profDefErr   error                  // SetDefaultProfile's error
	profGate     chan struct{}          // when set, SwitchProfile blocks until it is closed
	switching    atomic.Bool            // a SwitchProfile is in flight
	touched      atomic.Int32           // Profile/Profiles calls made while switching
	skillFiles   map[string]string      // SkillFiles; nil is the review skill
	skillPrompts int                    // SkillPrompt calls
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
	w, _ := b.Window(model)
	bud := &core.Budget{Window: w}
	keep := b.keep
	if keep == 0 {
		keep = core.DefaultCompactKeep
	}
	return core.SessionConfig{
		Provider: b.prov, Model: model, System: core.BuildSystem(core.BasePrompt, ""),
		Exec: ex, Log: log, UI: ui, Budget: bud, MaxTurns: b.maxTurns,
		Compact: &core.CompactConfig{Reserve: core.DefaultCompactReserve, Keep: keep},
		Meta:    map[string]any{"base_url": "http://x", "approve": approveOf(gate), "root": b.root, "context_files": []string{}},
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

// SetDefaultModel records the stored default; the real one writes config.json.
func (b *testBackend) SetDefaultModel(name string) error {
	b.defaults = append(b.defaults, name)
	return nil
}

// BareModel mirrors the real backend: a known provider prefix is stripped,
// anything else is already a bare id.
func (b *testBackend) BareModel(ref string) string {
	if name, rest, found := strings.Cut(ref, "/"); found {
		switch name {
		case "openrouter", "ollama", "custom", "vllm":
			return rest
		}
	}
	return ref
}
func (b *testBackend) SkillNames() []string { return []string{"review"} }

// ContextFiles backs the start banner's [Context] section; nil hides it.
func (b *testBackend) ContextFiles() []ContextFile { return b.ctxFiles }
func (b *testBackend) SkillFiles() map[string]string {
	if b.skillFiles != nil {
		return b.skillFiles
	}
	return map[string]string{"/skills/review/SKILL.md": "review"}
}

func (b *testBackend) Window(string) (int, bool) {
	if b.window > 0 {
		return b.window, !b.windowGuess
	}
	return testWindow, !b.windowGuess
}

func (b *testBackend) Farmer() Farmer { return b.farmer }

func (b *testBackend) SkillPrompt(name, text string) (string, bool) {
	b.skillPrompts++
	if name != "review" {
		return "", false
	}
	return "SKILL review: " + text, true
}

func (b *testBackend) DefaultSettingsScope() string { return "local" }

func (b *testBackend) ListSettings() []SettingLine {
	if b.settings == nil {
		return []SettingLine{{Key: "model", Value: "m", Source: "default"}}
	}
	keys := make([]string, 0, len(b.settings))
	for k := range b.settings {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	out := make([]SettingLine, 0, len(keys))
	for _, k := range keys {
		out = append(out, b.settings[k])
	}
	return out
}

func (b *testBackend) GetSetting(key string) (SettingLine, error) {
	if b.settings != nil {
		if v, ok := b.settings[key]; ok {
			return v, nil
		}
	}
	if key == "model" {
		return SettingLine{Key: "model", Value: "m", Source: "default"}, nil
	}
	return SettingLine{}, errors.New(key + " is not set")
}

func (b *testBackend) SetSetting(scope, key, value string) error {
	if scope == "" {
		scope = b.DefaultSettingsScope()
	}
	b.setCalls = append(b.setCalls, scope+" "+key+"="+value)
	if b.settings == nil {
		b.settings = map[string]SettingLine{}
	}
	b.settings[key] = SettingLine{Key: key, Value: value, Source: scope + " config"}
	return nil
}

func (b *testBackend) UnsetSetting(scope, key string) error {
	if scope == "" {
		scope = b.DefaultSettingsScope()
	}
	b.setCalls = append(b.setCalls, "unset "+scope+" "+key)
	if b.settings != nil {
		delete(b.settings, key)
	}
	return nil
}

func (b *testBackend) Profile() string {
	if b.switching.Load() {
		b.touched.Add(1)
	}
	if b.profile == "" {
		return "default"
	}
	return b.profile
}

func (b *testBackend) Profiles() []string {
	if b.switching.Load() {
		b.touched.Add(1)
	}
	if b.profiles == nil {
		return []string{"default"}
	}
	return b.profiles
}

// SetDefaultProfile records the stored default; the real one writes config.json.
func (b *testBackend) SetDefaultProfile(name string) error {
	if b.profDefErr != nil {
		return b.profDefErr
	}
	b.profDefaults = append(b.profDefaults, name)
	return nil
}

// SwitchProfile sets the active profile and reports profSwitch, or profErr.
func (b *testBackend) SwitchProfile(s *core.Session, name string, ask func(string) string) (ProfileSwitch, error) {
	b.switching.Store(true)
	if b.profGate != nil {
		<-b.profGate
	}
	b.switching.Store(false)
	if b.profErr != nil {
		return ProfileSwitch{}, b.profErr
	}
	b.profile = name
	return b.profSwitch, nil
}

// Reload swaps the live session's system prompt (and optional read roots).
func (b *testBackend) Reload(s *core.Session, ask func(string) string) ([]string, error) {
	if ask != nil {
		b.reloadAsk = append(b.reloadAsk, ask("trust /tmp and load AGENTS.md? [y/N/always] "))
	}
	system := b.reloadSystem
	if system == "" {
		system = "reloaded-system"
	}
	if err := s.SetSystem(system, map[string]any{"context_files": []map[string]any{{"path": "AGENTS.md", "loaded": true}}}); err != nil {
		return nil, err
	}
	if b.reloadRoots != nil {
		s.SetReadRoots(b.reloadRoots)
	}
	return []string{"reloaded · AGENTS.md loaded · 0 skills"}, nil
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

func (b *testBackend) Shell(ctx context.Context, command string, out io.Writer) (int, error) {
	return tools.RunShell(ctx, b.root, command, os.Environ(), out)
}

// approveOf is the approve level a session's gate runs at, as the real
// backend records it in session_started.
func approveOf(gate core.Gate) string {
	if g, ok := gate.(*Gate); ok {
		return g.Level().String()
	}
	return core.Never.String()
}
