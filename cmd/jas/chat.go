package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/term"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/providers"
	"github.com/jasonli0226/jas/sessions"
	"github.com/jasonli0226/jas/settings"
	"github.com/jasonli0226/jas/skills"
	"github.com/jasonli0226/jas/tools"
	"github.com/jasonli0226/jas/tui"
)

// sessionListLimit caps the sessions offered by /resume.
const sessionListLimit = 20

// isTerminal reports whether fd is a terminal; tests replace it.
var isTerminal = term.IsTerminal

type chatArgs struct {
	cont  bool   // --continue: resume this project's newest session
	name  string // --name: title for the session
	trust bool   // --trust-project
	flags map[string]string
}

// chatCmd runs the inline chat: `jas [--continue] [flags]`.
func chatCmd(ctx context.Context, args []string) error {
	ca, err := parseChat(args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, usage)
		return nil
	}
	if err != nil {
		return err
	}
	if err := onboardIfNeeded(); err != nil {
		return err
	}
	// Checked before prepare, whose trust prompt would otherwise read a pipe.
	if !isTerminal(int(os.Stdin.Fd())) || !isTerminal(int(os.Stdout.Fd())) {
		return usageErr(`the chat needs a terminal; use jas run "task"`)
	}
	e, err := prepare(ctx, ca.flags, ca.trust)
	if err != nil {
		return err
	}
	if ctx.Err() != nil { // Ctrl-C at the trust prompt: never start the chat
		return interrupted()
	}
	e.warnUnknownWindow(e.cfg.Model)
	return chatExit(ctx, tui.Run(ctx, &chatBackend{e: e}, tui.Options{
		Level: e.level, Model: e.cfg.Model, Root: e.root, Windows: runtime.GOOS == "windows", Version: version,
		Continue: ca.cont, Name: ca.name, Now: time.Now, Redact: e.red.Redact, FarmBell: e.cfg.FarmBell,
		Hints: e.cfg.Hints, WebTools: e.cfg.WebTools,
	}, os.Stdin, os.Stdout))
}

// chatExit maps the program being killed by a cancelled ctx (a signal) to the
// interrupted exit code, which report prints nothing for.
func chatExit(ctx context.Context, err error) error {
	if ctx.Err() != nil && errors.Is(err, tea.ErrProgramKilled) {
		return interrupted()
	}
	return err
}

func interrupted() error {
	return &core.ExitError{Code: core.ExitInterrupted, Reason: "interrupted"}
}

// parseName normalizes a --name value; passing it blank is a usage error.
func parseName(fs *flag.FlagSet, name string) (string, error) {
	passed := false
	fs.Visit(func(f *flag.Flag) { passed = passed || f.Name == "name" })
	name = core.NormalizeTitle(name)
	if passed && name == "" {
		return "", usageErr("--name needs a non-empty title")
	}
	return name, nil
}

// parseChat reads the chat flags: the setting flags plus --continue, --name
// and --trust-project. A task argument belongs to `jas run`.
func parseChat(args []string) (chatArgs, error) {
	fs := flag.NewFlagSet("jas", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var ca chatArgs
	fs.BoolVar(&ca.cont, "continue", false, "resume this project's newest session")
	fs.StringVar(&ca.name, "name", "", "title for the session")
	fs.BoolVar(&ca.trust, "trust-project", false, "load the project context file without asking")
	for _, k := range settings.Keys {
		if k.Flag {
			fs.String(strings.ReplaceAll(k.Name, "_", "-"), "", k.Name+" (overrides "+k.EnvName()+" and stored config)")
		}
	}
	if err := fs.Parse(args); errors.Is(err, flag.ErrHelp) {
		return ca, err
	} else if err != nil {
		return ca, usageErr("%v", err)
	}
	if fs.NArg() > 0 {
		return ca, usageErr(`unexpected argument %q; to run one task use jas run "task"`, fs.Arg(0))
	}
	var err error
	if ca.name, err = parseName(fs, ca.name); err != nil {
		return ca, err
	}
	ca.flags = map[string]string{}
	fs.Visit(func(f *flag.Flag) {
		if f.Name != "continue" && f.Name != "trust-project" && f.Name != "name" {
			ca.flags[strings.ReplaceAll(f.Name, "-", "_")] = f.Value.String()
		}
	})
	return ca, nil
}

// chatBackend is the chat's tui.Backend over the real settings, provider,
// redaction, trust decision and skills.
type chatBackend struct {
	e          *env
	farmerOnce sync.Once
	farmer     *chatFarmer
}

var _ tui.Backend = (*chatBackend)(nil)

// providerFor resolves a model reference to its provider (name and
// endpoint) and the bare model id. A qualified ref ("vllm/qwen3") picks the
// named provider; a bare id stays on the default one from settings.
func (b *chatBackend) providerFor(model string) (name, baseURL, bare string, err error) {
	name, baseURL, bare, err = b.e.layers.Merged().ResolveModel(model, b.e.cfg.Provider, b.e.cfg.BaseURL)
	return name, baseURL, bare, err
}

// provider builds the Chat Completions client for a resolved provider.
func (b *chatBackend) provider(name, baseURL string) (providers.Provider, error) {
	key, _, err := settings.ResolveKey(name, os.Getenv, b.e.auth)
	if err != nil {
		return nil, err
	}
	return &providers.OpenAI{BaseURL: baseURL, APIKey: key, IdleTimeout: sseIdleTimeout}, nil
}

// NewSession starts a session in a new directory under <home>/sessions.
func (b *chatBackend) NewSession(model string, ui core.UI, gate core.Gate) (*tui.Live, error) {
	provName, baseURL, bare, err := b.providerFor(model)
	if err != nil {
		return nil, err
	}
	prov, err := b.provider(provName, baseURL)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(b.e.home, sessionsDir, core.NewSessionID(time.Now()), eventsFile)
	log, err := core.OpenEventLog(path, nil, b.e.red.Redact)
	if err != nil {
		return nil, err
	}
	rec := b.record(path)
	c := b.configProv(prov, provName, baseURL, bare, log, ui, gate)
	rec.attach(&c, b.e.root)
	s, err := core.NewSession(c)
	if err != nil {
		return nil, errors.Join(err, rec.close(), log.Close())
	}
	return b.live(provName, path, model, log, s, c.Budget, rec), nil
}

// Resume reopens a saved session. The log is opened first: its lock tells
// whether another jas has the session open.
func (b *chatBackend) Resume(path string, ui core.UI, gate core.Gate) (*tui.Live, []string, error) {
	id := filepath.Base(filepath.Dir(path))
	log, err := core.OpenEventLog(path, nil, b.e.red.Redact)
	if errors.Is(err, core.ErrSessionLocked) {
		return nil, nil, fmt.Errorf("session %s is open in another jas process: %w", id, err)
	}
	if err != nil {
		return nil, nil, err
	}
	st, err := core.Replay(path)
	if err != nil {
		return nil, nil, errors.Join(err, log.Close())
	}
	model, notice := core.ResumeModel(st, b.e.cfg.BaseURL, b.e.cfg.Model)
	provName, baseURL, bare, err := b.providerFor(model)
	if err != nil {
		return nil, nil, errors.Join(err, log.Close())
	}
	prov, err := b.provider(provName, baseURL) // resume picks the recorded model's provider
	if err != nil {
		return nil, nil, errors.Join(err, log.Close())
	}
	rec := b.record(path)
	c := b.configProv(prov, provName, baseURL, bare, log, ui, gate)
	rec.attach(&c, b.e.root)
	s, err := core.ResumeSession(c, st)
	if err != nil {
		return nil, nil, errors.Join(err, rec.close(), log.Close())
	}
	notices := slices.Clone(st.Notices)
	if notice != "" {
		notices = append(notices, notice)
	}
	return b.live(provName, path, model, log, s, c.Budget, rec), notices, nil
}

// configProv is the session setup shared by NewSession and Resume, for the
// provider the model resolves to. The system prompt always comes from the
// current trust decision, never from the file. Compaction is on in the chat
// only; jas run leaves it off.
func (b *chatBackend) configProv(prov providers.Provider, provName, baseURL, model string, log core.EventLog, ui core.UI, gate core.Gate) core.SessionConfig {
	e := b.e
	ui = core.NewRedactingUI(ui, e.red.Redact)
	log = core.NewUILog(log, ui)
	toolset := tools.SessionTools(e.cfg.WebTools, e.cfg.SearchBaseURL)
	b.Farmer() // sets b.farmer
	toolset = append(toolset, suggestFarm{ui: ui, available: b.farmer.Available, tree: b.farmer.TreeReady})
	exec := &core.Executor{
		Reg:    core.NewRegistry(toolset...),
		Policy: e.policy, Gate: gate, Log: log, Filter: e.red.Filter, Redact: e.red.Redact,
	}
	meta := e.sessionMeta(model)
	meta["provider"], meta["base_url"] = provName, baseURL
	return core.SessionConfig{
		Provider: prov, Model: model, System: e.pc.system,
		Exec: exec, Log: log, UI: ui, MaxTurns: e.cfg.MaxTurns,
		Budget: e.budget(model), Meta: meta,
		Compact:   &core.CompactConfig{Reserve: e.cfg.CompactReserve, Keep: e.cfg.CompactKeep, Model: e.cfg.CompactModel},
		MaxOutput: e.cfg.MaxOutputTokens,
	}
}

func (b *chatBackend) live(provName, path, model string, log core.EventLog, s *core.Session, bud *core.Budget, rec chatRecord) *tui.Live {
	return &tui.Live{
		Session: s, ID: filepath.Base(filepath.Dir(path)), Path: path, Model: model, Provider: provName, Budget: bud,
		Close: func(status string, code int) error {
			return errors.Join(s.Close(status, code), rec.close(), log.Close())
		},
	}
}

// chatRecord is a chat session's record and its heartbeat. A zero value (the
// record could not be written) does nothing: records are advisory, and the
// chat has no stderr line to report the failure on.
type chatRecord struct {
	h    *sessions.Handle
	stop context.CancelFunc
}

// record registers the chat session whose log is path.
func (b *chatBackend) record(path string) chatRecord {
	ctx, stop := context.WithCancel(context.Background())
	h, err := registerRecord(ctx, b.e.root, "chat", path, "", b.e.red.Redact)
	if err != nil {
		stop()
		return chatRecord{}
	}
	return chatRecord{h: h, stop: stop}
}

// attach wires the record into a session config.
func (r chatRecord) attach(c *core.SessionConfig, root string) {
	if r.h == nil {
		return
	}
	c.Exec.Touched = touchHook(r.h, root)
	c.Meta["record_path"] = r.h.Path()
}

func (r chatRecord) close() error {
	if r.h == nil {
		return nil
	}
	r.stop()
	return r.h.Close()
}

// Sessions lists this project's saved sessions, newest first, leaving out
// the one at path exclude.
func (b *chatBackend) Sessions(exclude string) ([]core.SessionInfo, error) {
	all, err := core.ListSessions(filepath.Join(b.e.home, sessionsDir), b.e.root, sessionListLimit)
	if err != nil {
		return nil, err
	}
	if exclude == "" {
		return all, nil
	}
	exclude = filepath.Clean(exclude)
	return slices.DeleteFunc(all, func(i core.SessionInfo) bool { return filepath.Clean(i.Path) == exclude }), nil
}

// Models offers the configured model (bare or qualified), the models with
// stored overrides, the context-window catalog qualified by its provider,
// and every other configured provider's current model.
func (b *chatBackend) Models() []string {
	var names []string
	for _, m := range append(b.e.layers.ModelNames(), b.e.cfg.Model) {
		names = append(names, b.qualified(m))
	}
	if b.e.cfg.Provider == settings.ProviderOpenRouter {
		for _, id := range settings.CatalogModels() {
			names = append(names, b.qualified(id))
		}
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// qualified prefixes model with its provider unless it already names one.
// The legacy custom default is left bare: "custom/…" names no endpoint.
func (b *chatBackend) qualified(model string) string {
	if _, _, ok := settings.SplitModelRef(model, b.e.layers.Merged().KnownProvider); ok {
		return model
	}
	if b.e.cfg.Provider != "" && b.e.cfg.Provider != settings.ProviderCustom {
		return b.e.cfg.Provider + "/" + model
	}
	return model
}

// ProviderOf names the provider a model reference resolves to: for the
// picker's cross-provider refusal. Bare ids resolve to the default provider.
func (b *chatBackend) ProviderOf(model string) string {
	name, _, _, err := b.providerFor(model)
	if err != nil {
		return ""
	}
	return name
}

// BareModel resolves a model reference to the bare id on the provider it
// names; a ref with no known provider is a bare id itself (OpenRouter ids
// carry slashes). Unresolvable refs come back unchanged, so a bad switch
// fails at the provider instead of silently here.
func (b *chatBackend) BareModel(ref string) string {
	_, _, bare, err := b.providerFor(ref)
	if err != nil {
		return ref
	}
	return bare
}

// Window is model's context window, DefaultContextWindow (known=false)
// when neither the catalog nor models.<name>.context_window has it.
func (b *chatBackend) Window(model string) (int, bool) {
	return b.e.window(model)
}

// SetDefaultModel stores model as the default in the global config.json, so a
// new jas starts on it. The picked reference is stored verbatim: a qualified
// one ("vllm/qwen3") keeps naming its provider, a bare id stays on the default.
// /settings defaults to local scope; Ctrl+S keeps writing global for compat.
func (b *chatBackend) SetDefaultModel(model string) error {
	if err := b.writeSetting(settings.ScopeGlobal, "model", model); err != nil {
		return err
	}
	// Keep the reference the user picked (e.g. "openrouter/gpt-5.2"), not the
	// bare id Build resolves for the wire.
	b.e.cfg.Model = model
	return nil
}

// DefaultSettingsScope is the write scope /settings uses when none is given:
// local, or global when this session ignores the project's settings
// (untrusted) and a local write would do nothing.
func (b *chatBackend) DefaultSettingsScope() string {
	if !b.e.settingsTrusted {
		return string(settings.ScopeGlobal)
	}
	return string(settings.ScopeLocal)
}

// ListSettings returns every plain key's effective value and source, then
// model overrides, for /settings.
func (b *chatBackend) ListSettings() []tui.SettingLine {
	eff := settings.Resolve(b.e.flags, os.Getenv, b.e.layers)
	var out []tui.SettingLine
	for _, k := range settings.Keys {
		v := eff[k.Name]
		raw := v.Raw
		if raw == "" {
			raw = "(not set)"
		}
		out = append(out, tui.SettingLine{Key: k.Name, Value: raw, Source: string(v.Source), Options: k.Options})
	}
	for _, n := range b.e.layers.ModelNames() {
		w, src, ok := b.e.layers.ModelSource(n)
		if !ok {
			continue
		}
		out = append(out, tui.SettingLine{
			Key: "models." + n + ".context_window", Value: fmt.Sprintf("%d", w), Source: string(src),
		})
	}
	return out
}

// GetSetting returns one effective setting (plain key or models.*).
func (b *chatBackend) GetSetting(key string) (tui.SettingLine, error) {
	if spec, plain := settings.LookupKey(key); plain {
		v := settings.Resolve(b.e.flags, os.Getenv, b.e.layers)[key]
		raw := v.Raw
		if raw == "" {
			raw = "(not set)"
		}
		return tui.SettingLine{Key: key, Value: raw, Source: string(v.Source), Options: spec.Options}, nil
	}
	name, ok := modelKeyName(key)
	if !ok {
		return tui.SettingLine{}, fmt.Errorf("unknown setting %q", key)
	}
	w, src, found := b.e.layers.ModelSource(name)
	if !found {
		return tui.SettingLine{}, fmt.Errorf("%s is not set", key)
	}
	return tui.SettingLine{Key: key, Value: fmt.Sprintf("%d", w), Source: string(src)}, nil
}

func modelKeyName(key string) (string, bool) {
	const prefix, suffix = "models.", ".context_window"
	if !strings.HasPrefix(key, prefix) || !strings.HasSuffix(key, suffix) {
		return "", false
	}
	name := strings.TrimSuffix(strings.TrimPrefix(key, prefix), suffix)
	return name, name != ""
}

// SetSetting writes key=value to scope (global|project|local) and refreshes
// the effective Run config. Empty scope uses DefaultSettingsScope.
func (b *chatBackend) SetSetting(scope, key, value string) error {
	s, err := parseWriteScope(scope, b.DefaultSettingsScope())
	if err != nil {
		return err
	}
	return b.writeSetting(s, key, value)
}

// UnsetSetting removes key from scope and refreshes the effective Run config.
func (b *chatBackend) UnsetSetting(scope, key string) error {
	s, err := parseWriteScope(scope, b.DefaultSettingsScope())
	if err != nil {
		return err
	}
	return b.unsetSetting(s, key)
}

func parseWriteScope(scope, def string) (settings.Scope, error) {
	if scope == "" {
		scope = def
	}
	return settings.ParseScope(scope)
}

func (b *chatBackend) writeSetting(scope settings.Scope, key, value string) error {
	path, err := scope.Path(b.e.home, b.e.root)
	if err != nil {
		return err
	}
	if err := settings.CheckWriteScope(key, scope); err != nil {
		return err
	}
	if err := b.e.projectWritable(scope); err != nil {
		return err
	}
	next, err := b.e.files.File(scope).Set(key, value)
	if err != nil {
		return err
	}
	if err := saveSettingsFile(b.e.root, b.e.home, scope, path, next); err != nil {
		return err
	}
	b.e.setFile(scope, next)
	return b.reconfigure()
}

func (b *chatBackend) unsetSetting(scope settings.Scope, key string) error {
	path, err := scope.Path(b.e.home, b.e.root)
	if err != nil {
		return err
	}
	if err := b.e.projectWritable(scope); err != nil {
		return err
	}
	next, err := b.e.files.File(scope).Unset(key)
	if err != nil {
		return err
	}
	if err := saveSettingsFile(b.e.root, b.e.home, scope, path, next); err != nil {
		return err
	}
	b.e.setFile(scope, next)
	return b.reconfigure()
}

// reconfigure rebuilds e.cfg from flags + env + layers after a settings write.
func (b *chatBackend) reconfigure() error {
	cfg, err := settings.Resolve(b.e.flags, os.Getenv, b.e.layers).Build()
	if err != nil {
		return err
	}
	level, err := core.ParseLevel(cfg.Approve)
	if err != nil {
		return err
	}
	b.e.cfg, b.e.level = cfg, level
	return nil
}

// SkillNames lists every skill the user can invoke, including those hidden
// from the model.
func (b *chatBackend) SkillNames() []string {
	names := make([]string, 0, len(b.e.pc.skills))
	for _, s := range b.e.pc.skills {
		names = append(names, s.Name)
	}
	slices.Sort(names)
	return names
}

// ContextFiles lists the context file and project skills that were offered
// to this session; Loaded is the trust decision.
func (b *chatBackend) ContextFiles() []tui.ContextFile {
	files := make([]tui.ContextFile, len(b.e.pc.files))
	for i, f := range b.e.pc.files {
		files[i] = tui.ContextFile{Path: f.Path, Loaded: f.Loaded}
	}
	return files
}

// SkillFiles maps each skill's SKILL.md path to its name, so the chat can
// show the model loading one as a skill row.
func (b *chatBackend) SkillFiles() map[string]string {
	files := make(map[string]string, len(b.e.pc.skills))
	for _, s := range b.e.pc.skills {
		files[s.Location] = s.Name
	}
	return files
}

// SkillPrompt is the user message that invokes skill name with text.
func (b *chatBackend) SkillPrompt(name, text string) (string, bool) {
	i := slices.IndexFunc(b.e.pc.skills, func(s skills.Skill) bool { return s.Name == name })
	if i < 0 {
		return "", false
	}
	s := b.e.pc.skills[i]
	p := fmt.Sprintf("Use the \"%s\" skill for this request. Its instructions (%s):\n\n%s", s.Name, s.Location, strings.TrimSpace(string(s.Content)))
	if text = strings.TrimSpace(text); text != "" {
		p += "\n\n" + text
	}
	return b.e.red.Redact(p), true
}

// Reload rediscovers skills and project context, then swaps the system
// prompt and skill read roots into the live session. ask is the trust
// prompt (nil skips untrusted content).
func (b *chatBackend) Reload(s *core.Session, ask func(string) string) ([]string, error) {
	notices, roots, err := b.e.reloadContext(ask)
	if err != nil {
		return nil, err
	}
	files := make([]map[string]any, len(b.e.pc.files))
	for i, f := range b.e.pc.files {
		files[i] = map[string]any{"path": f.Path, "loaded": f.Loaded, "sha256": f.SHA256}
	}
	if err := s.SetSystem(b.e.pc.system, map[string]any{"context_files": files, "skills": b.e.skillList}); err != nil {
		return notices, err
	}
	s.SetReadRoots(roots)
	return notices, nil
}

// Shell runs the owner's ! command in the project root with the owner's own
// environment: unlike the agent's shell tool, the owner typed it. The chat
// redacts its output before showing or sending it.
func (b *chatBackend) Shell(ctx context.Context, command string, out io.Writer) (int, error) {
	return tools.RunShell(ctx, b.e.root, command, os.Environ(), out)
}
