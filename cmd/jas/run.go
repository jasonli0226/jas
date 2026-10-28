package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/providers"
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
	trust  bool // --trust-project: load the project context file without asking
	prompt string
	flags  map[string]string // setting key -> value, only flags that were passed
}

// reportRedactor masks the resolved API key in top-level error messages.
var reportRedactor *core.Redactor

func runCmd(ctx context.Context, args []string) error {
	ra, err := parseRun(args)
	if err != nil {
		return err
	}
	home, err := settingsHome()
	if err != nil {
		return err
	}
	file, err := settings.LoadFile(filepath.Join(home, configFile))
	if err != nil {
		return err
	}
	cfg, err := settings.Resolve(ra.flags, os.Getenv, file).Build()
	if err != nil {
		return usageErr("%v", err)
	}
	level, err := core.ParseLevel(cfg.Approve)
	if err != nil {
		return usageErr("%v", err)
	}
	auth, err := loadAuthForRun(filepath.Join(home, authFile), cfg.Provider)
	if err != nil {
		return err
	}
	key, _, err := settings.ResolveKey(cfg.Provider, os.Getenv, auth)
	if err != nil {
		return usageErr("%v", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	root := core.FindRoot(cwd)

	path := filepath.Join(home, sessionsDir, core.NewSessionID(time.Now()), eventsFile)
	var tee io.Writer
	var ui core.UI = core.NewPrinter(os.Stdout)
	if ra.json {
		tee, ui = os.Stdout, core.NewQuietUI()
	}
	policy := core.ExecPolicy{Root: root, MaxOutput: cfg.MaxToolOutput, RedactSkip: cfg.RedactSkipPaths}
	red := newRedactor(key, policy.RedactSkip)
	reportRedactor = red
	log, err := core.OpenEventLog(path, tee, red.Redact)
	if err != nil {
		return err
	}
	defer log.Close()
	log = core.NewUILog(log, core.NewRedactingUI(ui, red.Redact))

	stdin := bufio.NewReader(os.Stdin)
	interactive := term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stderr.Fd()))
	system, ctxFiles, err := loadProjectContext(ctx, root, home, interactive, ra.trust, stdin, os.Stderr, red.Redact)
	if err != nil {
		return err
	}

	exec := &core.Executor{
		Reg:    core.NewRegistry(append(tools.ReadOnly(), tools.Mutating()...)...),
		Policy: policy,
		Gate: &core.Approver{
			Level: level, In: stdin, Out: os.Stderr, Interactive: interactive,
		},
		Log: log, Filter: red.Filter,
	}
	window, known := settings.ContextWindow(cfg.Model, file.ContextOverrides())
	if !known {
		fmt.Fprintln(os.Stderr, red.Redact(fmt.Sprintf("jas: unknown context window for model %q; assuming %d tokens (set models.%s.context_window)", cfg.Model, window, cfg.Model)))
	}
	_, err = core.Run(ctx, core.RunConfig{
		Provider: &providers.OpenAI{BaseURL: cfg.BaseURL, APIKey: key, IdleTimeout: sseIdleTimeout},
		Model:    cfg.Model, Prompt: ra.prompt, System: system,
		Exec: exec, Log: log, UI: ui, MaxTurns: cfg.MaxTurns,
		Budget: &core.Budget{Window: window, ElideAt: cfg.ElideAt, Keep: cfg.KeepToolResults},
		SessionMeta: map[string]any{
			"base_url": cfg.BaseURL, "approve": level.String(), "root": root, "context_files": ctxFiles,
			"context_window": window, "window_known": known,
		},
	})
	if err == nil && !ra.json {
		fmt.Println()
	}
	return err
}

// newRedactor builds the process-wide secret redactor: the resolved API key
// (from any source) plus the built-in patterns.
func newRedactor(key string, skip []string) *core.Redactor {
	r := &core.Redactor{SkipPaths: skip}
	if key != "" {
		r.Exact = []string{key}
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
	ra.flags = map[string]string{}
	fs.Visit(func(f *flag.Flag) {
		if f.Name != "json" && f.Name != "trust-project" {
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
