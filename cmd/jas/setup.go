package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/providers"
	"github.com/jasonli0226/jas/settings"
	"github.com/jasonli0226/jas/skills"
)

// env is everything a session needs that is settled before its log opens:
// settings, the key and redactor, the project and its trust decision, and
// the provider. `jas run` and the chat both build one with prepare.
type env struct {
	cfg    settings.Run
	layers settings.Layers // what the session resolves from (see Layers.ForProject)
	files  settings.Layers // the config files as written, for settings writes
	// settingsTrusted is the trust decision the settings were resolved under.
	// /reload may change pc.trusted; settings follow it at the next start.
	settingsTrusted bool
	flags           map[string]string // CLI flags from prepare; kept for mid-session reconfigure
	level           core.Level
	key             string
	auth            map[string]settings.AuthEntry // every stored key, for any provider
	root, home      string
	red             *core.Redactor
	policy          core.ExecPolicy
	pc              projectContext
	skillList       []skillMeta
	stdin           *bufio.Reader // shared by the trust prompt and jas run's approver
	interactive     bool
	trust           bool // --trust-project: reload keeps auto-trusting
	provider        providers.Provider
}

// authKeys returns every secret the auth file can put in a request: literal
// keys and the current values of env references, so all of them are
// redacted no matter which provider a session picks.
func authKeys(auth map[string]settings.AuthEntry, env func(string) string) []string {
	keys := make([]string, 0, len(auth))
	for _, e := range auth {
		if e.Key != "" {
			keys = append(keys, e.Key)
		}
		if name := strings.TrimPrefix(e.Env, "$"); name != "" {
			keys = append(keys, env(name))
		}
	}
	return keys
}

// prepare resolves settings from flags, env and the Claude-style config
// layers (global / project / local), the API key, the project root, skills
// and the project trust decision. It sets reportRedactor once the key is
// known and prints warnings to stderr.
func prepare(ctx context.Context, flags map[string]string, trust bool) (*env, error) {
	home, err := settingsHome()
	if err != nil {
		return nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getwd: %w", err)
	}
	root := core.FindRoot(cwd)
	files, err := settings.LoadLayers(home, root)
	if err != nil {
		return nil, err
	}
	// Trust is decided before any setting is resolved: project and local
	// config live in the repository, so they apply only once it is trusted.
	var early []string // printed once the redactor exists
	deny := denyPatterns(home)
	denied := core.ExecPolicy{Root: root, Deny: deny}.Denied
	userHome, _ := os.UserHomeDir()     // no home: user skills are skipped
	userConfig, _ := os.UserConfigDir() // no config dir: manual skills are skipped
	if err := syncManualSkills(); err != nil {
		early = append(early, "manual skills: "+err.Error())
	}
	projectSkills, pw := skills.Discover(skills.ProjectSources(root), denied)
	userSkills, uw := skills.Discover(skills.UserSources(userHome, userConfig), denied)
	early = append(append(early, pw...), uw...)
	stdin := bufio.NewReader(os.Stdin)
	interactive := term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stderr.Fd()))
	ask := func(p string) string { return askLine(ctx, stdin, os.Stderr, p) }
	pt, err := decideProjectTrust(root, home, projectSkills, interactive, trust, ask)
	if err != nil {
		return nil, err
	}
	layers, lw := files.ForProject(pt.trusted)
	early = append(early, lw...)

	cfg, err := settings.Resolve(flags, os.Getenv, layers).Build()
	if err != nil {
		return nil, usageErr("%v", err)
	}
	level, err := core.ParseLevel(cfg.Approve)
	if err != nil {
		return nil, usageErr("%v", err)
	}
	auth, err := loadAuthForRun(filepath.Join(home, authFile), cfg.Provider)
	if err != nil {
		return nil, err
	}
	key, _, err := settings.ResolveKey(cfg.Provider, os.Getenv, auth)
	if err != nil {
		return nil, usageErr("%v", err)
	}

	policy := core.ExecPolicy{Root: root, MaxOutput: cfg.MaxToolOutput, RedactSkip: cfg.RedactSkipPaths, Deny: deny}
	red := newRedactor(append([]string{key}, authKeys(auth, os.Getenv)...)...)
	reportRedactor = red
	warn := func(w string) { fmt.Fprintln(os.Stderr, core.Sanitize(red.Redact("jas: "+w))) }
	for _, w := range append(early, cfg.Warnings...) {
		warn(w)
	}
	pc := buildProjectContext(pt, projectSkills, userSkills, red.Redact, warn)
	skillList, skillDirs := listedSkills(pc.skills)
	policy.ReadRoots = skillDirs
	return &env{
		cfg: cfg, layers: layers, files: files, settingsTrusted: pt.trusted, flags: flags, level: level, key: key, auth: auth, root: root, home: home, red: red,
		policy: policy, pc: pc, skillList: skillList, stdin: stdin, interactive: interactive, trust: trust,
		provider: &providers.OpenAI{BaseURL: cfg.BaseURL, APIKey: key, IdleTimeout: sseIdleTimeout},
	}, nil
}

// denyPatterns are the paths no tool or skill discovery may read.
func denyPatterns(home string) []string {
	return []string{homeDenyPattern(home), sessionsDenyPattern, farmWorktreesDenyPattern}
}

// saveSettingsFile writes a settings file the owner changed through jas. A
// project or local file is part of the trust bundle, so when the project's
// trust was remembered it is re-recorded for the new bytes: the owner's own
// edit does not ask again, while anyone else's edit still does.
func saveSettingsFile(root, home string, scope settings.Scope, path string, f settings.File) error {
	if scope == settings.ScopeGlobal {
		return f.Save(path)
	}
	store := trustStore(home)
	denied := core.ExecPolicy{Root: root, Deny: denyPatterns(home)}.Denied
	remembered, err := store.Check(root, currentTrustBundle(root, denied))
	if err != nil {
		return err
	}
	if err := f.Save(path); err != nil {
		return err
	}
	if !remembered {
		return nil
	}
	return store.Trust(root, currentTrustBundle(root, denied))
}

// projectWritable refuses a mid-session write to the project or local file
// of an untrusted project: the session ignores those files, so the change
// would silently do nothing.
func (e *env) projectWritable(scope settings.Scope) error {
	if scope == settings.ScopeGlobal || e.settingsTrusted {
		return nil
	}
	return fmt.Errorf("project not trusted, so this session ignores %s config: restart with --trust-project (or answer always) to use it, or set it globally", scope)
}

// setFile records a settings write and recomputes what the session reads.
func (e *env) setFile(scope settings.Scope, f settings.File) {
	e.files = e.files.WithFile(scope, f)
	e.layers, _ = e.files.ForProject(e.settingsTrusted) // user-only keys were warned about at start
}

// window returns the context window for model and whether it is known.
func (e *env) window(model string) (int, bool) {
	return settings.ContextWindow(model, e.layers.ContextOverrides())
}

// budget is a fresh context budget for model.
func (e *env) budget(model string) *core.Budget {
	w, _ := e.window(model)
	return &core.Budget{Window: w, ElideAt: e.cfg.ElideAt, Keep: e.cfg.KeepToolResults}
}

// warnUnknownWindow prints jas run's notice when model's window is a guess.
func (e *env) warnUnknownWindow(model string) {
	if window, known := e.window(model); !known {
		fmt.Fprintln(os.Stderr, e.red.Redact(fmt.Sprintf("jas: unknown context window for model %q; assuming %d tokens (set models.%s.context_window)", model, window, model)))
	}
}

// sessionMeta is the session_started / session_resumed payload jas adds.
func (e *env) sessionMeta(model string) map[string]any {
	window, known := e.window(model)
	return map[string]any{
		"provider": e.cfg.Provider, "base_url": e.cfg.BaseURL, "approve": e.level.String(), "root": e.root, "context_files": e.pc.files, "skills": e.skillList,
		"context_window": window, "window_known": known, "web_tools": e.cfg.WebTools,
	}
}
