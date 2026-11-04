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
	cfg         settings.Run
	layers      settings.Layers
	flags       map[string]string // CLI flags from prepare; kept for mid-session reconfigure
	level       core.Level
	key         string
	auth        map[string]settings.AuthEntry // every stored key, for any provider
	root, home  string
	red         *core.Redactor
	policy      core.ExecPolicy
	pc          projectContext
	skillList   []skillMeta
	stdin       *bufio.Reader // shared by the trust prompt and jas run's approver
	interactive bool
	trust       bool // --trust-project: reload keeps auto-trusting
	provider    providers.Provider
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
	layers, err := settings.LoadLayers(home, root)
	if err != nil {
		return nil, err
	}
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

	policy := core.ExecPolicy{Root: root, MaxOutput: cfg.MaxToolOutput, RedactSkip: cfg.RedactSkipPaths, Deny: []string{homeDenyPattern(home), sessionsDenyPattern, farmWorktreesDenyPattern}}
	red := newRedactor(append([]string{key}, authKeys(auth, os.Getenv)...)...)
	reportRedactor = red
	stdin := bufio.NewReader(os.Stdin)
	interactive := term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stderr.Fd()))
	warn := func(w string) { fmt.Fprintln(os.Stderr, core.Sanitize(red.Redact("jas: "+w))) }
	userHome, _ := os.UserHomeDir()     // no home: user skills are skipped
	userConfig, _ := os.UserConfigDir() // no config dir: manual skills are skipped
	if err := syncManualSkills(); err != nil {
		warn("manual skills: " + err.Error())
	}
	projectSkills, pw := skills.Discover(skills.ProjectSources(root), policy.Denied)
	userSkills, uw := skills.Discover(skills.UserSources(userHome, userConfig), policy.Denied)
	for _, w := range append(pw, uw...) {
		warn(w)
	}
	ask := func(p string) string { return askLine(ctx, stdin, os.Stderr, p) }
	pc, err := loadProjectContext(root, home, projectSkills, userSkills, interactive, trust, ask, red.Redact, warn)
	if err != nil {
		return nil, err
	}
	skillList, skillDirs := listedSkills(pc.skills)
	policy.ReadRoots = skillDirs
	return &env{
		cfg: cfg, layers: layers, flags: flags, level: level, key: key, auth: auth, root: root, home: home, red: red,
		policy: policy, pc: pc, skillList: skillList, stdin: stdin, interactive: interactive, trust: trust,
		provider: &providers.OpenAI{BaseURL: cfg.BaseURL, APIKey: key, IdleTimeout: sseIdleTimeout},
	}, nil
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
		"base_url": e.cfg.BaseURL, "approve": e.level.String(), "root": e.root, "context_files": e.pc.files, "skills": e.skillList,
		"context_window": window, "window_known": known,
	}
}
