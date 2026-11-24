package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/settings"
	"github.com/jasonli0226/jas/tools"
)

const (
	sessionsDir    = "sessions"
	eventsFile     = "events.jsonl"
	configFile     = "config.json"
	authFile       = "auth.json"
	sseIdleTimeout = 60 * time.Second
)

type runArgs struct {
	json   bool
	trust  bool   // --trust-project: load the project context file without asking
	name   string // --name: session title, logged on session_started
	prompt string
	flags  map[string]string // setting key -> value, only flags that were passed

	maxTokens int           // --max-tokens: total token cap; 0 = none
	timeout   time.Duration // --timeout: wall-clock cap; 0 = none
	compact   bool          // --compact: auto-compaction as in the chat
	wrapUp    bool          // --wrap-up: ask for a final answer at the turn or token cap
}

// reportRedactor masks the resolved API key in top-level error messages.
var reportRedactor *core.Redactor

func runCmd(ctx context.Context, args []string) error {
	ra, err := parseRun(args)
	if err != nil {
		return err
	}
	e, err := prepare(ctx, ra.flags, ra.trust)
	if err != nil {
		return err
	}
	if ra.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, ra.timeout)
		defer cancel()
	}
	red := e.red

	path := filepath.Join(e.home, sessionsDir, core.NewSessionID(time.Now()), eventsFile)
	var tee io.Writer
	var ui core.UI = core.NewPrinter(os.Stdout, isTerminal(int(os.Stdout.Fd())))
	if ra.json {
		tee, ui = os.Stdout, core.NewQuietUI()
	}
	ui = core.NewRedactingUI(ui, red.Redact)

	// Open the log only once the session can start, so a setup failure
	// leaves no log without session_started/session_ended.
	log, err := core.OpenEventLog(path, tee, red.Redact)
	if err != nil {
		return err
	}
	defer log.Close()
	log = core.NewUILog(log, ui)
	title := ra.name
	if title == "" {
		title = ra.prompt
	}
	rec, recErr := registerRecord(ctx, e.root, "run", path, title, red.Redact)
	if recErr != nil {
		fmt.Fprintln(os.Stderr, core.Sanitize(red.Redact("jas: session record: "+recErr.Error())))
	}
	defer rec.Close()

	exec := &core.Executor{
		Reg:    core.NewRegistry(tools.SessionTools(e.cfg.WebTools, e.cfg.SearchBaseURL)...),
		Policy: e.policy,
		Gate: &core.Approver{
			Level: e.level, In: e.stdin, Out: os.Stderr, Interactive: e.interactive,
		},
		Log: log, Filter: red.Filter, Redact: red.Redact,
	}
	guardMemory(exec, e.mem, !e.interactive, red.Redact)
	if rec != nil {
		exec.Touched = touchHook(rec, e.root)
	}
	e.warnUnknownWindow(e.cfg.Model)
	meta := e.sessionMeta(e.cfg.Model)
	if ra.name != "" {
		meta["title"] = ra.name
	}
	if rec != nil {
		meta["record_path"] = rec.Path()
	}
	_, err = core.Run(ctx, core.RunConfig{
		Provider: e.provider,
		Model:    e.cfg.Model, Prompt: ra.prompt, System: e.pc.system,
		Exec: exec, Log: log, UI: ui, MaxTurns: e.cfg.MaxTurns,
		Budget:      e.budget(e.cfg.Model),
		SessionMeta: meta, MaxOutput: e.cfg.MaxOutputTokens, ReasoningEffort: e.cfg.ReasoningEffort, Tokens: ra.tokenPool(e.cfg.CachedTokenWeight),
		Compact: ra.compactConfig(e.cfg), WrapUp: ra.wrapUp,
	})
	if err == nil && !ra.json {
		fmt.Println()
	}
	return err
}

// runOnlyFlags are jas run flags that are not stored settings.
var runOnlyFlags = map[string]bool{"json": true, "trust-project": true, "name": true, "max-tokens": true, "timeout": true, "compact": true, "wrap-up": true}

// tokenPool is the --max-tokens cap, or nil when none was given.
func (ra runArgs) tokenPool(cachedWeight float64) *core.TokenPool {
	if ra.maxTokens <= 0 {
		return nil
	}
	return rootPool("run", ra.maxTokens, cachedWeight)
}

// rootPool is a token pool with no parent that charges cached prompt
// tokens at cachedWeight (cached_token_weight).
func rootPool(name string, limit int, cachedWeight float64) *core.TokenPool {
	p := core.NewTokenPool(name, limit, nil)
	p.SetCachedWeight(cachedWeight)
	return p
}

// compactConfig is the chat's compaction config when --compact was given.
func (ra runArgs) compactConfig(cfg settings.Run) *core.CompactConfig {
	if !ra.compact {
		return nil
	}
	return &core.CompactConfig{Reserve: cfg.CompactReserve, Keep: cfg.CompactKeep, Model: cfg.CompactModel}
}

// homeDenyPattern denies file tools access to jas's own state (keys, config,
// trust) wherever JAS_HOME puts it; the deny floor covers only ~/.jas.
func homeDenyPattern(home string) string {
	if r, err := filepath.EvalSymlinks(home); err == nil {
		home = r
	}
	if abs, err := filepath.Abs(home); err == nil {
		home = abs
	}
	return filepath.ToSlash(home) + "/**"
}

// newRedactor builds the process-wide secret redactor: the resolved API
// keys, from every source, plus the built-in patterns.
func newRedactor(keys ...string) *core.Redactor {
	r := &core.Redactor{}
	if exact := slices.DeleteFunc(keys, func(s string) bool { return s == "" }); len(exact) > 0 {
		r.Exact = slices.Compact(exact)
	}
	return r
}

// parseRun reads the run flags. Only flags that were passed are recorded, so
// they override env and stored config.
func parseRun(args []string) (runArgs, error) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var ra runArgs
	fs.BoolVar(&ra.json, "json", false, "emit the event stream as JSONL on stdout")
	fs.BoolVar(&ra.trust, "trust-project", false, "load the project context file without asking")
	fs.StringVar(&ra.name, "name", "", "title for the session")
	maxTokens := fs.String("max-tokens", "", "stop with exit 7 before a request that would cross this many total tokens")
	timeout := fs.String("timeout", "", "stop with exit 7 after this wall-clock duration (e.g. 30m)")
	fs.BoolVar(&ra.compact, "compact", false, "summarize older history when the context fills up, as the chat does")
	fs.BoolVar(&ra.wrapUp, "wrap-up", false, "at the turn or token cap, ask for a final answer instead of stopping without one")
	for _, k := range settings.Keys {
		if k.Flag {
			fs.String(strings.ReplaceAll(k.Name, "_", "-"), "", k.Name+" (overrides "+k.EnvName()+" and stored config)")
		}
	}
	if err := fs.Parse(args); err != nil {
		return ra, usageErr("%v", err)
	}
	if fs.NArg() != 1 || strings.TrimSpace(fs.Arg(0)) == "" {
		return ra, usageErr(`expected exactly one task argument: jas run [flags] "task"`)
	}
	ra.prompt = fs.Arg(0)
	var err error
	if *maxTokens != "" {
		if ra.maxTokens, err = strconv.Atoi(*maxTokens); err != nil || ra.maxTokens <= 0 {
			return ra, usageErr("--max-tokens must be a positive integer, got %q", *maxTokens)
		}
	}
	if *timeout != "" {
		if ra.timeout, err = time.ParseDuration(*timeout); err != nil || ra.timeout <= 0 {
			return ra, usageErr("--timeout must be a positive duration such as 30m, got %q", *timeout)
		}
	}
	if ra.name, err = parseName(fs, ra.name); err != nil {
		return ra, err
	}
	ra.flags = map[string]string{}
	fs.Visit(func(f *flag.Flag) {
		if !runOnlyFlags[f.Name] {
			ra.flags[strings.ReplaceAll(f.Name, "-", "_")] = f.Value.String()
		}
	})
	return ra, nil
}

// settingsHome returns the jas home dir ($JAS_HOME or ~/.jas).
func settingsHome() (string, error) {
	h := settings.Home()
	if h == "" {
		return "", errors.New("cannot locate home directory: set JAS_HOME")
	}
	return h, nil
}

// loadAuthForRun loads auth.json only when key resolution may need it:
// never when JAS_API_KEY is set. For a provider that may run keyless, an
// unreadable auth.json is a warning rather than a failure.
func loadAuthForRun(path, provider string) (map[string]settings.AuthEntry, error) {
	if os.Getenv(settings.EnvAPIKey) != "" {
		return nil, nil
	}
	auth, err := settings.LoadAuth(path)
	if err != nil && !settings.KeyRequired(provider) {
		fmt.Fprintf(os.Stderr, "jas: warning: ignoring stored keys: %v\n", err)
		return nil, nil
	}
	return auth, err
}
