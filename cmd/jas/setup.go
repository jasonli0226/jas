package main

import (
	"bufio"
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/memory"
	"github.com/jasonli0226/jas/providers"
	"github.com/jasonli0226/jas/sessions"
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
	profile         string          // active profile name
	profileSrc      settings.Source // the layer that chose it
	prof            profileState
	mem             memoryState
}

// memoryState is the session's memory: its dirs, the resolved setting and
// the two indexes loaded at start (or at the last /reload), their text
// already redacted. The zero value is off.
type memoryState struct {
	dirs             memory.Dirs
	mode             memory.Mode
	hideProject      bool
	profile, project memory.Index
}

// on reports whether the session has memory: a Memory section, the lift and
// the write guard.
func (m memoryState) on() bool {
	return m.mode != memory.ModeOff && m.dirs.Profile != ""
}

// memoryKey is the memory setting's name in settings.Keys.
const memoryKey = "memory"

// memorySetting is the owner's memory value (JAS_MEMORY, else the profile
// layer, else global) and the project's (local, else project); "" is unset.
// The project value can only narrow the owner's (memory.ResolveMode).
func memorySetting(l settings.Layers, getenv func(string) string) (base, project string) {
	spec, _ := settings.LookupKey(memoryKey)
	return cmp.Or(getenv(spec.EnvName()), l.Profile.Values[memoryKey], l.Global.Values[memoryKey]),
		cmp.Or(l.Local.Values[memoryKey], l.Project.Values[memoryKey])
}

// loadMemory resolves the memory setting for profile in root and loads both
// indexes, redacted. It returns p with the memory lift set and the memory
// dir appended to ReadRoots; with memory off the lift is cleared and
// ReadRoots left as given. Memory never fails a session: a
// project root it cannot key, or a lift the policy refuses, turns memory
// off with a warning, and an unreadable index loads empty with one.
func loadMemory(p core.ExecPolicy, home, profile, root string, layers settings.Layers,
	redact func(string) string, warn func(string)) (core.ExecPolicy, memoryState) {
	p.ProfileMemory = ""
	off := memoryState{mode: memory.ModeOff}
	mode, hide := memory.ResolveMode(memorySetting(layers, os.Getenv))
	if mode == memory.ModeOff {
		return p, off
	}
	dirs, err := memoryDirs(home, profile, root)
	if err != nil {
		warn(err.Error() + "; memory is off for this session")
		return p, off
	}
	p.ProfileMemory = dirs.Profile
	if why := p.ProfileMemoryIgnored(); why != "" {
		warn(why)
		p.ProfileMemory = ""
		return p, off
	}
	if !p.ProfileMemoryLifted() {
		warn(unliftedMemory(dirs.Profile) + "; memory is off for this session")
		p.ProfileMemory = ""
		return p, off
	}
	m := memoryState{dirs: dirs, mode: mode, hideProject: hide}
	if m.profile, m.project, err = memory.LoadIndexes(dirs, hide); err != nil { // both come back empty
		warn(err.Error() + "; memory indexes not loaded")
	}
	for _, why := range []string{m.profile.Ignored, m.project.Ignored} {
		if why != "" {
			warn(why)
		}
	}
	m.profile.Text, m.project.Text = redact(m.profile.Text), redact(m.project.Text)
	p.ReadRoots = append(slices.Clip(p.ReadRoots), dirs.Profile)
	return p, m
}

// memoryDirs are profile's memory dirs for the project at root, keyed by
// its main checkout with symlinks resolved, so every spelling of the
// project path shares one project folder.
func memoryDirs(home, profile, root string) (memory.Dirs, error) {
	main := sessions.MainRoot(root)
	if r, err := filepath.EvalSymlinks(main); err == nil {
		main = r
	}
	return memory.DirsFor(settings.ProfileDir(home, profile), main)
}

// unliftedMemory says why the profile memory at dir gets no exception when
// the policy gives no reason (ProfileMemoryLifted false, Ignored "").
func unliftedMemory(dir string) string {
	return "profile memory " + dir + " is not a folder jas can use (a file, or missing behind a link)"
}

// newMemoryGuard is the write guard for m, or nil when memory is off.
// headless sessions (no one can approve a write) get memory read-only.
func newMemoryGuard(m memoryState, headless bool, redact func(string) string) *memory.Guard {
	if !m.on() {
		return nil
	}
	return &memory.Guard{Dirs: m.dirs, Mode: m.mode, Headless: headless, Redact: redact}
}

// guardMemory sets x's WritePrecheck and WriteGuard to newMemoryGuard's
// checks (both nil when memory is off).
func guardMemory(x *core.Executor, m memoryState, headless bool, redact func(string) string) {
	x.WritePrecheck, x.WriteGuard = nil, nil
	if g := newMemoryGuard(m, headless, redact); g != nil {
		x.WritePrecheck, x.WriteGuard = g.Precheck, g.Check
	}
}

// memoryMeta is the session_started / session_resumed memory object, or nil
// when memory is off. project_dir is "" when the project hides its memory.
func (m memoryState) meta() map[string]any {
	if !m.on() {
		return nil
	}
	project := m.dirs.Project
	if m.hideProject {
		project = ""
	}
	return map[string]any{
		"profile_dir": m.dirs.Profile, "project_dir": project,
		"index_bytes": len(m.profile.Text) + len(m.project.Text),
		"truncated":   m.profile.Truncated || m.project.Truncated,
	}
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
	// Project skills join that decision; user skills wait for the profile,
	// which project config can choose.
	var early []string // printed once the redactor exists
	if err := syncManualSkills(); err != nil {
		early = append(early, "manual skills: "+err.Error())
	}
	projectSkills, pw := skills.Discover(skills.ProjectSources(root), core.ExecPolicy{Root: root, Deny: denyPatterns(home)}.Denied)
	early = append(early, pw...)
	in := newSetupInput(ctx, home, root, files, early)
	pt, err := decideProjectTrust(root, home, projectSkills, in.interactive, trust, in.ask)
	if err != nil {
		return nil, err
	}
	in.trusted = pt.trusted
	var userSkills []skills.Skill
	in.afterProfile = func(policy core.ExecPolicy, prof profileState) []string {
		found, uw := discoverUserSkills(policy, home, prof)
		userSkills = found
		dirs := append([]string{policy.ProfileSkills}, policy.InheritedSkills...)
		return append(uw, skillsMigrationWarning(home, prof.name, dirs, found)...)
	}
	e, err := resolveEnv(flags, in)
	if err != nil {
		return nil, err
	}
	e.trust = trust
	pc := buildProjectContext(pt, projectSkills, userSkills, e.prof, e.red.Redact, e.warn)
	skillList, skillDirs := listedSkills(pc.skills)
	e.policy.ReadRoots = skillDirs
	e.policy, e.mem = loadMemory(e.policy, home, e.profile, root, e.layers, e.red.Redact, e.warn)
	e.pc, e.skillList = withMemory(pc, e.mem, !e.interactive), skillList
	return e, nil
}

// prepareNoProject is prepare for a session that belongs to no project
// (jas dream): the same settings, profile, key, redactor and provider, from
// the global and profile layers only. There is no trust prompt, no project
// skills or context file, and no memory section; root is used as given.
//
// keyless skips the API key requirement, for commands that call no model: a
// missing or unreadable key leaves e.key empty and the provider unusable.
func prepareNoProject(ctx context.Context, flags map[string]string, root string, keyless bool) (*env, error) {
	home, err := settingsHome()
	if err != nil {
		return nil, err
	}
	files, err := settings.LoadLayers(home, root)
	if err != nil {
		return nil, err
	}
	in := newSetupInput(ctx, home, root, files, nil)
	in.keyless = keyless
	return resolveEnv(flags, in)
}

// setupInput is what resolveEnv starts from: the jas home, the session root,
// the config files, whether project config applies, the warnings gathered so
// far, and the terminal the session talks to.
type setupInput struct {
	home, root  string
	files       settings.Layers
	trusted     bool
	early       []string // printed once the redactor exists
	stdin       *bufio.Reader
	interactive bool
	ask         func(prompt string) string
	// keyless: no model call follows, so a missing key is not an error.
	keyless bool
	// afterProfile, when set, runs once the profile is loaded, before the
	// settings are built; its warnings print with the others.
	afterProfile func(policy core.ExecPolicy, prof profileState) []string
}

func newSetupInput(ctx context.Context, home, root string, files settings.Layers, early []string) setupInput {
	stdin := bufio.NewReader(os.Stdin)
	return setupInput{
		home: home, root: root, files: files, early: early, stdin: stdin,
		interactive: isTerminal(int(os.Stdin.Fd())) && isTerminal(int(os.Stderr.Fd())), // tests replace isTerminal
		ask:         func(p string) string { return askLine(ctx, stdin, os.Stderr, p) },
	}
}

// resolveEnv loads the profile, resolves the settings, approval level, key
// and redactor, and builds the provider. It sets reportRedactor and prints
// every warning gathered so far. The env has no project context or memory.
func resolveEnv(flags map[string]string, in setupInput) (*env, error) {
	home, files, early := in.home, in.files, in.early
	layers, _ := files.ForProject(in.trusted) // warned below, once the profile layer is in
	name, profileSrc := settings.ResolveProfile(flags, os.Getenv, layers)
	settings.EnsureDefaultProfile(home)
	prof, profW, err := loadProfile(home, name, profileSrc)
	if err != nil {
		return nil, err
	}
	early = append(early, profW...)
	files = files.WithProfileChain(prof.name, prof.layer, prof.parentFiles())
	layers, lw := files.ForProject(in.trusted)
	early = append(early, lw...)
	policy := profilePolicy(core.ExecPolicy{Root: in.root, Deny: denyPatterns(home)}, home, prof)
	if in.afterProfile != nil {
		early = append(early, in.afterProfile(policy, prof)...)
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
	if err != nil && !in.keyless {
		return nil, err
	}
	key, _, err := settings.ResolveKey(cfg.Provider, os.Getenv, auth)
	if err != nil && !in.keyless {
		return nil, usageErr("%v", err)
	}

	policy.MaxOutput, policy.RedactSkip = cfg.MaxToolOutput, cfg.RedactSkipPaths
	red := newRedactor(append([]string{key}, authKeys(auth, os.Getenv)...)...)
	reportRedactor = red
	e := &env{
		cfg: cfg, layers: layers, files: files, settingsTrusted: in.trusted, flags: flags, level: level, key: key, auth: auth,
		root: in.root, home: home, red: red, policy: policy, stdin: in.stdin, interactive: in.interactive,
		provider: &providers.OpenAI{BaseURL: cfg.BaseURL, APIKey: key, IdleTimeout: sseIdleTimeout},
		profile:  prof.name, profileSrc: profileSrc, prof: prof, mem: memoryState{mode: memory.ModeOff},
	}
	for _, w := range append(early, cfg.Warnings...) {
		e.warn(w)
	}
	return e, nil
}

// warn prints a redacted, sanitized jas warning to stderr.
func (e *env) warn(w string) {
	fmt.Fprintln(os.Stderr, core.Sanitize(e.red.Redact("jas: "+w)))
}

// profilePolicy sets the read exceptions for ps's skills: the active
// profile's skills directory, its parents' (nearest first), and the exact
// home deny pattern the lifts drop.
func profilePolicy(p core.ExecPolicy, home string, ps profileState) core.ExecPolicy {
	p.ProfileSkills = filepath.Join(settings.ProfileDir(home, ps.name), "skills")
	p.InheritedSkills = ps.inheritedSkillDirs()
	p.HomeDeny = homeDenyPattern(home)
	return p
}

// discoverUserSkills finds the user-level skills: the chain's (see
// discoverChainSkills), then the built-in manual, which a same-named chain
// skill shadows with a warning. All are read under the profile read scope,
// which lifts the jas home deny for the chain's skills directories only. A
// skills directory the lift refuses (a link into the jas home) is skipped
// with one warning.
func discoverUserSkills(policy core.ExecPolicy, home string, ps profileState) ([]skills.Skill, []string) {
	denied := policy.ProfileScope().Denied
	srcs, warns := profileSkillSources(policy, home)
	parts := ps.parts()
	metas := slices.Clone(parts)
	slices.Reverse(metas) // folder order: the active profile first
	chain, cw := discoverChainSkills(srcs, metas, home, denied)
	manual, mw := skills.Discover(skills.BuiltinSources(manualDir()), denied)
	found, sw := skills.Merge(chain, manual)
	return found, slices.Concat(warns, cw, mw, sw)
}

// profileSkillSources is one skills source per chain folder: the active
// profile's, then each parent's, nearest first. A folder the read exception
// refuses gets no source (nil at its place) and a warning.
func profileSkillSources(policy core.ExecPolicy, home string) ([][]skills.Source, []string) {
	srcs := make([][]skills.Source, 0, 1+len(policy.InheritedSkills))
	var warns []string
	if why := policy.ProfileSkillsIgnored(); why != "" {
		srcs, warns = append(srcs, nil), append(warns, why)
	} else {
		srcs = append(srcs, skills.ProfileSources(policy.ProfileSkills, home))
	}
	for _, dir := range policy.InheritedSkills {
		one := policy
		one.InheritedSkills = []string{dir}
		if why := one.InheritedSkillsIgnored(); len(why) > 0 {
			srcs, warns = append(srcs, nil), append(warns, why...)
			continue
		}
		srcs = append(srcs, skills.ProfileSources(dir, home))
	}
	return srcs, warns
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
	if scope == settings.ScopeGlobal || scope == settings.ScopeProfile || e.settingsTrusted {
		return nil
	}
	return fmt.Errorf("project not trusted, so this session ignores %s config: restart with --trust-project (or answer always) to use it, or set it globally", scope)
}

// setFile records a settings write and recomputes what the session reads. A
// profile write replaces the active profile's layer (and the copy a /reload
// keeps).
func (e *env) setFile(scope settings.Scope, f settings.File) {
	if scope == settings.ScopeProfile {
		e.files = e.files.WithProfile(e.activeProfile(), f)
		e.prof.layer = f
	} else {
		e.files = e.files.WithFile(scope, f)
	}
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
	meta := map[string]any{
		"profile": e.activeProfile(), "provider": e.cfg.Provider, "base_url": e.cfg.BaseURL, "approve": e.level.String(), "root": e.root, "context_files": e.pc.files, "skills": e.skillList,
		"context_window": window, "window_known": known, "web_tools": e.cfg.WebTools,
	}
	if mem := e.mem.meta(); mem != nil {
		meta["memory"] = mem
	}
	return meta
}

// skillsMigrationWarning is the one-time notice for users whose skills sit in
// the folders earlier jas versions read; it marks itself shown.
func skillsMigrationWarning(home, profile string, profileSkillsDirs []string, found []skills.Skill) []string {
	userHome, _ := os.UserHomeDir()
	userConfig, _ := os.UserConfigDir()
	n := migrationNotice(home, profile, userHome, userConfig, countProfileSkills(found, profileSkillsDirs))
	if n == "" {
		return nil
	}
	markSkillsNotice(home, profile)
	return []string{n}
}
